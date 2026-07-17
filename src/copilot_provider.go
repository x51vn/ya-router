// copilot_provider.go — GitHub Copilot backend provider implementation.
package yarouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	secretpkg "github.com/duvu/ya-router/internal/secret"
)

// CopilotProvider implements Provider for the GitHub Copilot backend.
type CopilotProvider struct {
	cfg               *Config // full config; Copilot auth lives at cfg.Providers.Copilot
	auth              secretpkg.AuthController
	mu                sync.Mutex
	accountCursor     int // index into cfg.Providers.Copilot.Accounts; protected by mu
	cb                *CircuitBreaker
	mc                *CoalescingCache // ListModels request coalescing
	cache             *ModelCache      // TTL-based model list cache
	freeCatalog       *CopilotFreeCatalog
	freeModelCursor   atomic.Uint64
	freeModelResolver func(ctx context.Context) ([]Model, error)
	proxyExecutor     func(ctx context.Context, r *http.Request, body []byte, cap Capability) (*http.Response, string, error)
}

func (p *CopilotProvider) ID() ProviderID { return ProviderCopilot }
func (p *CopilotProvider) Name() string   { return "GitHub Copilot" }
func (p *CopilotProvider) Capabilities() []Capability {
	return []Capability{CapabilityChat, CapabilityEmbeddings}
}

// activeAccount returns the current pool entry. Returns nil when the pool is empty.
func (p *CopilotProvider) activeAccount() *CopilotAccount {
	accounts := p.cfg.Providers.Copilot.Accounts
	if len(accounts) == 0 {
		return nil
	}
	return &accounts[p.accountCursor]
}

// authState returns the auth state of the active account, falling back to the
// legacy single-account Auth field when the pool is empty.
func (p *CopilotProvider) authState() *CopilotAuthState {
	if acc := p.activeAccount(); acc != nil {
		return &acc.Auth
	}
	return &p.cfg.Providers.Copilot.Auth
}

func (p *CopilotProvider) save() error {
	return persistCopilotRuntimeAccount(p.cfg, p.accountCursor)
}

func (p *CopilotProvider) cooldownSeconds() int64 {
	if s := p.cfg.Providers.Copilot.AccountCooldownSeconds; s > 0 {
		return int64(s)
	}
	return 300
}

func (p *CopilotProvider) isAccountInCooldown(acc *CopilotAccount) bool {
	if acc.LastLimitedAt == 0 {
		return false
	}
	return time.Now().Unix()-acc.LastLimitedAt < p.cooldownSeconds()
}

// firstHealthyAccount returns the index of the first account not in cooldown,
// or 0 if all are in cooldown.
func (p *CopilotProvider) firstHealthyAccount() int {
	accounts := p.cfg.Providers.Copilot.Accounts
	for i := range accounts {
		if !p.isAccountInCooldown(&accounts[i]) {
			return i
		}
	}
	return 0
}

func (p *CopilotProvider) advanceAccount() bool {
	return p.advanceAccountFrom(p.accountCursor)
}

// advanceAccountFrom records the account that actually served the failed
// request. Another request may already have moved the shared cursor, so the
// response path must never assume the current cursor still identifies it.
// Caller must hold p.mu.
func (p *CopilotProvider) advanceAccountFrom(failedIndex int) bool {
	accounts := p.cfg.Providers.Copilot.Accounts
	if failedIndex < 0 || failedIndex >= len(accounts) {
		return false
	}
	accounts[failedIndex].LastLimitedAt = time.Now().Unix()
	if len(accounts) <= 1 {
		return false
	}
	size := len(accounts)
	start := p.accountCursor
	if start == failedIndex {
		start = (failedIndex + 1) % size
	}
	for i := 0; i < size; i++ {
		next := (start + i) % size
		if next == failedIndex {
			continue
		}
		if !p.isAccountInCooldown(&accounts[next]) {
			p.accountCursor = next
			return true
		}
	}
	return false
}

func (p *CopilotProvider) ListModels(ctx context.Context) (*ModelList, error) {
	raw, err := p.listRawModels(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var deduped []Model
	for _, m := range raw.Data {
		if !seen[m.ID] {
			seen[m.ID] = true
			deduped = append(deduped, m)
		}
	}
	raw.Data = deduped
	return raw, nil
}

func (p *CopilotProvider) InvalidateModelCache() {
	p.cache.Invalidate()
}

func (p *CopilotProvider) listRawModels(ctx context.Context) (*ModelList, error) {
	if err := p.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}
	ml, err := p.cache.GetOrFetch(func() (*ModelList, error) {
		key := p.mc.getRequestKey("GET", copilotAPIBase+"/models", nil)
		result := p.mc.CoalesceRequest(key, func() interface{} {
			raw, fetchErr := p.fetchModelsRaw()
			if fetchErr != nil {
				return fetchErr
			}
			return raw
		})
		if err, ok := result.(error); ok {
			return nil, err
		}
		return cloneModelList(result.(*ModelList)), nil
	})
	if err != nil {
		return nil, err
	}
	return cloneModelList(ml), nil
}

func (p *CopilotProvider) fetchModelsRaw() (*ModelList, error) {
	_, token := p.credentialSnapshot()
	if token == "" {
		return &ModelList{Object: "list", Data: nil}, nil
	}
	req, err := http.NewRequest("GET", copilotAPIBase+"/models", nil)
	if err != nil {
		return nil, err
	}
	p.setCopilotHeaders(req, token, CapabilityChat)
	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Copilot /models returned %d", resp.StatusCode)
		return nil, fmt.Errorf("copilot /models: HTTP %d", resp.StatusCode)
	}
	var ml ModelList
	if err := json.NewDecoder(resp.Body).Decode(&ml); err != nil {
		return nil, err
	}
	return &ml, nil
}

// ProxyRequest proxies a chat or embeddings request to the Copilot API.
func (p *CopilotProvider) ProxyRequest(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	cap Capability,
) error {
	resp, embedModel, _, err := p.executeProxyRequest(ctx, r, body, cap)
	if err != nil {
		return err
	}
	return p.writeProxyResponse(w, resp, cap, embedModel)
}

func (p *CopilotProvider) executeProxyRequest(
	ctx context.Context,
	r *http.Request,
	body []byte,
	cap Capability,
) (*http.Response, string, int, error) {
	if err := p.EnsureAuthenticated(ctx); err != nil {
		log.Printf("[copilot] auth failed: %v", err)
		return nil, "", -1, fmt.Errorf("copilot auth: %w", err)
	}
	if !p.cb.canExecute() {
		log.Printf("[copilot] circuit breaker OPEN — rejecting request")
		return nil, "", -1, fmt.Errorf("copilot circuit breaker is open")
	}
	accountIndex, token := p.credentialSnapshot()

	isEmbed := cap == CapabilityEmbeddings
	embedModel := ""
	if isEmbed {
		body, embedModel = normalizeEmbeddingsRequestBody(body)
	}

	targetPath := "/chat/completions"
	if isEmbed {
		targetPath = "/embeddings"
	}

	upstreamURL := copilotAPIBase + targetPath
	log.Printf("[copilot] proxying %s → %s (body %d bytes)", cap, upstreamURL, len(body))

	req, err := http.NewRequestWithContext(ctx, r.Method, upstreamURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, "", accountIndex, err
	}
	p.setCopilotHeaders(req, token, cap)

	resp, err := makeRequestWithRetry(sharedHTTPClient, req, body)
	if err != nil {
		log.Printf("[copilot] upstream error: %v", err)
		p.cb.onFailure()
		return nil, "", accountIndex, err
	}

	log.Printf("[copilot] upstream responded HTTP %d (Content-Type: %s)",
		resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		log.Printf("[copilot] upstream rejected request with HTTP %d", resp.StatusCode)
	}

	if resp.StatusCode < 500 {
		p.cb.onSuccess()
	} else {
		log.Printf("[copilot] upstream 5xx error — circuit breaker failure")
		p.cb.onFailure()
	}

	return resp, embedModel, accountIndex, nil
}

func (p *CopilotProvider) credentialSnapshot() (int, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	index := p.accountCursor
	if p.auth != nil {
		if credential, ok := p.auth.ResolveCredential("copilot/token"); ok {
			return index, credential.Value
		}
	}
	return index, p.authState().CopilotToken
}

func (p *CopilotProvider) writeProxyResponse(
	w http.ResponseWriter,
	resp *http.Response,
	cap Capability,
	embedModel string,
) error {
	defer resp.Body.Close()

	if cap == CapabilityEmbeddings && resp.Header.Get("Content-Type") != "text/event-stream" {
		return writeCopilotEmbeddingsResponse(w, resp, embedModel)
	}
	return streamResponse(w, resp)
}

func (p *CopilotProvider) ProxyFreeChatRequest(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	requestedModel string,
) error {
	accounts := p.cfg.Providers.Copilot.Accounts
	maxAccountAttempts := max(1, len(accounts))

	for accountAttempt := 0; accountAttempt < maxAccountAttempts; accountAttempt++ {
		models, err := p.resolveFreeChatModels(ctx)
		if err != nil {
			return err
		}

		attempts := p.nextFreeChatAttemptOrder(models)
		var lastLimitResp *http.Response
		lastLimitAccountIndex := -1
		modelExhausted := false

		for i, model := range attempts {
			patchedBody := patchBodyModel(body, model.ID)
			resp, embedModel, accountIndex, execErr := p.executeProxy(ctx, r, patchedBody, CapabilityChat)
			if execErr != nil {
				if i == len(attempts)-1 {
					return execErr
				}
				log.Printf("[copilot-free] model %q transport error, shifting to next model: %v", model.ID, execErr)
				continue
			}

			if isLimit, reason := isAccountLimitSignal(resp); isLimit {
				log.Printf("[copilot-free] account limit signal from model %q (HTTP %d, reason=%s)", model.ID, resp.StatusCode, reason)
				lastLimitResp = resp
				lastLimitAccountIndex = accountIndex
				modelExhausted = (i == len(attempts)-1)
				if !modelExhausted {
					resp.Body.Close()
					continue
				}
				break
			}

			if shift, reason := shouldShiftToNextCopilotModel(resp); shift {
				if i == len(attempts)-1 {
					log.Printf("[copilot-free] model %q failed and no more models remain; forwarding final upstream response", model.ID)
					return p.writeProxyResponse(w, resp, CapabilityChat, embedModel)
				}
				log.Printf("[copilot-free] model %q failed with HTTP %d; shifting to next model (reason=%s)", model.ID, resp.StatusCode, reason)
				resp.Body.Close()
				continue
			}

			if requestedModel != model.ID {
				log.Printf("[copilot-free] ignoring client model %q; selected %q", requestedModel, model.ID)
			}
			return p.writeProxyResponse(w, resp, CapabilityChat, embedModel)
		}

		if lastLimitResp != nil && modelExhausted {
			p.mu.Lock()
			advanced := p.advanceAccountFrom(lastLimitAccountIndex)
			p.mu.Unlock()
			if advanced {
				lastLimitResp.Body.Close()
				log.Printf("[copilot-free] all models rate-limited on current account; switching to next account")
				continue
			}
			log.Printf("[copilot-free] all accounts exhausted or in cooldown; forwarding rate-limit response")
			return p.writeProxyResponse(w, lastLimitResp, CapabilityChat, "")
		}
	}

	return fmt.Errorf("copilot free-model failover exhausted")
}

func (p *CopilotProvider) resolveFreeChatModels(ctx context.Context) ([]Model, error) {
	if p.freeModelResolver != nil {
		return p.freeModelResolver(ctx)
	}
	if p.freeCatalog == nil {
		p.freeCatalog = NewCopilotFreeCatalog(defaultFreeCatalogTTL)
	}

	docEligible, err := p.freeCatalog.EligibleModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("copilot free catalog unavailable: %w", err)
	}
	rawModels, err := p.listRawModels(ctx)
	if err != nil {
		return nil, err
	}
	effective := resolveEffectiveCopilotFreeModels(docEligible, rawModels)
	if len(effective) == 0 {
		return nil, fmt.Errorf("no eligible copilot free chat models available")
	}
	return effective, nil
}

func (p *CopilotProvider) nextFreeChatAttemptOrder(models []Model) []Model {
	if len(models) <= 1 {
		return append([]Model(nil), models...)
	}
	start := int(p.freeModelCursor.Add(1)-1) % len(models)
	ordered := make([]Model, 0, len(models))
	ordered = append(ordered, models[start:]...)
	ordered = append(ordered, models[:start]...)
	return ordered
}

func (p *CopilotProvider) executeProxy(
	ctx context.Context,
	r *http.Request,
	body []byte,
	cap Capability,
) (*http.Response, string, int, error) {
	accountIndex, _ := p.credentialSnapshot()
	if p.proxyExecutor != nil {
		response, embedModel, err := p.proxyExecutor(ctx, r, body, cap)
		return response, embedModel, accountIndex, err
	}
	return p.executeProxyRequest(ctx, r, body, cap)
}

func shouldShiftToNextCopilotModel(resp *http.Response) (bool, string) {
	if resp == nil {
		return false, ""
	}

	switch {
	case resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= http.StatusInternalServerError:
		return true, "transient_upstream_error"
	case resp.StatusCode == http.StatusBadRequest,
		resp.StatusCode == http.StatusNotFound,
		resp.StatusCode == http.StatusConflict:
		preview := strings.ToLower(readAndResetResponseBody(resp))
		if strings.Contains(preview, "model") &&
			(strings.Contains(preview, "unsupported") ||
				strings.Contains(preview, "not available") ||
				strings.Contains(preview, "not found") ||
				strings.Contains(preview, "unavailable")) {
			return true, "model_unavailable"
		}
	}

	return false, ""
}

func isAccountLimitSignal(resp *http.Response) (bool, string) {
	if resp == nil {
		return false, ""
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return true, "rate_limited"
	}
	if resp.StatusCode == http.StatusForbidden {
		preview := strings.ToLower(readAndResetResponseBody(resp))
		if strings.Contains(preview, "rate limit") ||
			strings.Contains(preview, "exceeded") ||
			strings.Contains(preview, "quota") {
			return true, "quota_exhausted"
		}
	}
	return false, ""
}

func readAndResetResponseBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	const previewLimit = 4 * 1024
	original := resp.Body
	body, err := io.ReadAll(io.LimitReader(original, previewLimit+1))
	resp.Body = &replayReadCloser{
		Reader: io.MultiReader(bytes.NewReader(body), original),
		closer: original,
	}
	if err != nil {
		return ""
	}
	if len(body) > previewLimit {
		body = body[:previewLimit]
	}
	return string(body)
}

type replayReadCloser struct {
	io.Reader
	closer io.Closer
}

func (body *replayReadCloser) Close() error { return body.closer.Close() }

func (p *CopilotProvider) setCopilotHeaders(req *http.Request, token string, cap Capability) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Editor-Version", "vscode/1.99.3")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.26.7")
	req.Header.Set("Copilot-Integration-Id", "vscode-chat")
	req.Header.Set("X-Initiator", "user")
	if cap == CapabilityEmbeddings {
		req.Header.Set("Openai-Intent", "embeddings")
	} else {
		req.Header.Set("Openai-Intent", "conversation-edits")
	}
}

func writeCopilotEmbeddingsResponse(w http.ResponseWriter, resp *http.Response, embedModel string) error {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		body = ensureEmbeddingsResponseCompat(body, embedModel)
	}
	copyHeaders(w, resp.Header, "Content-Length")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(resp.StatusCode)
	_, err = w.Write(body)
	return err
}

// Health returns the provider's current authentication state.
func (p *CopilotProvider) Health(_ context.Context) ProviderHealth {
	p.mu.Lock()
	defer p.mu.Unlock()
	auth := p.authState()
	hasRefresh := auth.GitHubToken != ""
	authenticated := auth.CopilotToken != "" && auth.ExpiresAt > time.Now().Unix()
	return ProviderHealth{
		Authenticated: authenticated,
		CanRefresh:    hasRefresh,
		LastRefreshAt: time.Unix(auth.ExpiresAt, 0),
	}
}
