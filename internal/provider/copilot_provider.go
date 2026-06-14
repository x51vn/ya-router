// copilot_provider.go — GitHub Copilot backend provider implementation.
package provider

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

	authpkg "github.com/x51vn/github-copilot-svcs/internal/auth"
	"github.com/x51vn/github-copilot-svcs/internal/config"
	"github.com/x51vn/github-copilot-svcs/internal/httputil"
	"github.com/x51vn/github-copilot-svcs/internal/types"
)

const (
	copilotAPIBase = "https://api.githubcopilot.com"
	userAgent      = "github-copilot-svcs/1.0"
)

// CopilotProvider implements Provider for the GitHub Copilot backend.
type CopilotProvider struct {
	Cfg               *config.Config
	Mu                sync.Mutex
	AccountCursor     int
	Cb                *httputil.CircuitBreaker
	Mc                *httputil.CoalescingCache
	Cache             *ModelCache
	FreeCatalog       *CopilotFreeCatalog
	freeModelCursor   atomic.Uint64
	FreeModelResolver func(ctx context.Context) ([]types.Model, error)
	ProxyExecutor     func(ctx context.Context, r *http.Request, body []byte, cap Capability) (*http.Response, string, error)
}

// NewCopilotProvider constructs a CopilotProvider backed by cfg.
func NewCopilotProvider(cfg *config.Config) *CopilotProvider {
	timeout := time.Duration(cfg.Timeouts.CircuitBreaker) * time.Second
	p := &CopilotProvider{
		Cfg:         cfg,
		Cb:          httputil.NewCircuitBreaker(timeout),
		Mc:          httputil.NewCoalescingCache(),
		Cache:       NewModelCache(DefaultModelCacheTTL),
		FreeCatalog: NewCopilotFreeCatalog(defaultFreeCatalogTTL),
	}
	p.AccountCursor = p.FirstHealthyAccount()
	return p
}

func (p *CopilotProvider) ID() ProviderID { return ProviderCopilot }
func (p *CopilotProvider) Name() string   { return "GitHub Copilot" }
func (p *CopilotProvider) Capabilities() []Capability {
	return []Capability{CapabilityChat, CapabilityEmbeddings}
}

// activeAccount returns the current pool entry. Returns nil when the pool is empty.
func (p *CopilotProvider) ActiveAccount() *config.CopilotAccount {
	accounts := p.Cfg.Providers.Copilot.Accounts
	if len(accounts) == 0 {
		return nil
	}
	return &accounts[p.AccountCursor]
}

// authState returns the auth state of the active account, falling back to the
// legacy single-account Auth field when the pool is empty.
func (p *CopilotProvider) authState() *config.CopilotAuthState {
	if acc := p.ActiveAccount(); acc != nil {
		return &acc.Auth
	}
	return &p.Cfg.Providers.Copilot.Auth
}

func (p *CopilotProvider) save() error {
	return config.SaveConfig(p.Cfg)
}

func (p *CopilotProvider) cooldownSeconds() int64 {
	if s := p.Cfg.Providers.Copilot.AccountCooldownSeconds; s > 0 {
		return int64(s)
	}
	return 300
}

func (p *CopilotProvider) isAccountInCooldown(acc *config.CopilotAccount) bool {
	if acc.LastLimitedAt == 0 {
		return false
	}
	return time.Now().Unix()-acc.LastLimitedAt < p.cooldownSeconds()
}

// firstHealthyAccount returns the index of the first account not in cooldown,
// or 0 if all are in cooldown.
func (p *CopilotProvider) FirstHealthyAccount() int {
	accounts := p.Cfg.Providers.Copilot.Accounts
	for i := range accounts {
		if !p.isAccountInCooldown(&accounts[i]) {
			return i
		}
	}
	return 0
}

// advanceAccount marks the current account as rate-limited, then advances the
// cursor to the next non-cooldown account in the pool.
// Returns true if a new healthy account was found, false if all are exhausted.
func (p *CopilotProvider) AdvanceAccount() bool {
	accounts := p.Cfg.Providers.Copilot.Accounts
	if len(accounts) <= 1 {
		return false
	}
	accounts[p.AccountCursor].LastLimitedAt = time.Now().Unix()
	size := len(accounts)
	for i := 1; i < size; i++ {
		next := (p.AccountCursor + i) % size
		if !p.isAccountInCooldown(&accounts[next]) {
			p.AccountCursor = next
			return true
		}
	}
	return false
}

// EnsureAuthenticated ensures the Copilot bearer token is valid.
func (p *CopilotProvider) EnsureAuthenticated(ctx context.Context) error {
	p.Mu.Lock()
	defer p.Mu.Unlock()

	copilotAuth := p.authState()
	if copilotAuth.CopilotToken == "" {
		return authpkg.CopilotAuthenticate(copilotAuth, p.save)
	}

	now := time.Now().Unix()
	threshold := int64(300) // 5 min default
	if copilotAuth.RefreshIn > 0 {
		if t := copilotAuth.RefreshIn / 5; t > threshold {
			threshold = t
		}
	}
	if copilotAuth.ExpiresAt-now <= threshold {
		if err := authpkg.CopilotRefreshToken(copilotAuth, p.save); err != nil {
			log.Printf("Copilot refresh failed, re-authenticating: %v", err)
			return authpkg.CopilotAuthenticate(copilotAuth, p.save)
		}
	}
	return nil
}

func (p *CopilotProvider) ListModels(ctx context.Context) (*types.ModelList, error) {
	raw, err := p.listRawModels(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var deduped []types.Model
	for _, m := range raw.Data {
		if !seen[m.ID] {
			seen[m.ID] = true
			deduped = append(deduped, m)
		}
	}
	raw.Data = deduped
	return raw, nil
}

func (p *CopilotProvider) listRawModels(ctx context.Context) (*types.ModelList, error) {
	if err := p.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}
	ml, err := p.Cache.GetOrFetch(func() (*types.ModelList, error) {
		key := p.Mc.GetRequestKey("GET", copilotAPIBase+"/models", nil)
		result := p.Mc.CoalesceRequest(key, func() interface{} {
			raw, fetchErr := p.fetchModelsRaw()
			if fetchErr != nil {
				return fetchErr
			}
			return raw
		})
		if err, ok := result.(error); ok {
			return nil, err
		}
		return cloneModelList(result.(*types.ModelList)), nil
	})
	if err != nil {
		return nil, err
	}
	return cloneModelList(ml), nil
}

func (p *CopilotProvider) fetchModelsRaw() (*types.ModelList, error) {
	token := p.authState().CopilotToken
	if token == "" {
		return &types.ModelList{Object: "list", Data: nil}, nil
	}
	req, err := http.NewRequest("GET", copilotAPIBase+"/models", nil)
	if err != nil {
		return nil, err
	}
	p.setCopilotHeaders(req, token, CapabilityChat)
	resp, err := httputil.SharedHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Printf("Copilot /models returned %d", resp.StatusCode)
		return nil, fmt.Errorf("copilot /models: HTTP %d", resp.StatusCode)
	}
	var ml types.ModelList
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
	resp, embedModel, err := p.executeProxyRequest(ctx, r, body, cap)
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
) (*http.Response, string, error) {
	if err := p.EnsureAuthenticated(ctx); err != nil {
		log.Printf("[copilot] auth failed: %v", err)
		return nil, "", fmt.Errorf("copilot auth: %w", err)
	}
	if !p.Cb.CanExecute() {
		log.Printf("[copilot] circuit breaker OPEN — rejecting request")
		return nil, "", fmt.Errorf("copilot circuit breaker is open")
	}

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
		return nil, "", err
	}
	p.setCopilotHeaders(req, p.authState().CopilotToken, cap)

	resp, err := httputil.MakeRequestWithRetry(httputil.SharedHTTPClient, req, body)
	if err != nil {
		log.Printf("[copilot] upstream error: %v", err)
		p.Cb.OnFailure()
		return nil, "", err
	}

	log.Printf("[copilot] upstream responded HTTP %d (Content-Type: %s)",
		resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		peekBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Printf("[copilot] upstream %d response: %s", resp.StatusCode, string(peekBody))
		resp.Body = io.NopCloser(io.MultiReader(bytes.NewReader(peekBody), resp.Body))
	}

	if resp.StatusCode < 500 {
		p.Cb.OnSuccess()
	} else {
		log.Printf("[copilot] upstream 5xx error — circuit breaker failure")
		p.Cb.OnFailure()
	}

	return resp, embedModel, nil
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
	return httputil.StreamResponse(w, resp)
}

func (p *CopilotProvider) ProxyFreeChatRequest(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	requestedModel string,
) error {
	accounts := p.Cfg.Providers.Copilot.Accounts
	maxAccountAttempts := max(1, len(accounts))

	for accountAttempt := 0; accountAttempt < maxAccountAttempts; accountAttempt++ {
		models, err := p.resolveFreeChatModels(ctx)
		if err != nil {
			return err
		}

		attempts := p.nextFreeChatAttemptOrder(models)
		var lastLimitResp *http.Response
		modelExhausted := false

		for i, model := range attempts {
			patchedBody := patchBodyModel(body, model.ID)
			resp, embedModel, execErr := p.executeProxy(ctx, r, patchedBody, CapabilityChat)
			if execErr != nil {
				if i == len(attempts)-1 {
					return execErr
				}
				log.Printf("[copilot-free] model %q transport error, shifting to next model: %v", model.ID, execErr)
				continue
			}

			if isLimit, preview := isAccountLimitSignal(resp); isLimit {
				log.Printf("[copilot-free] account limit signal from model %q (HTTP %d): %q", model.ID, resp.StatusCode, preview)
				resp.Body.Close()
				lastLimitResp = resp
				modelExhausted = (i == len(attempts)-1)
				if !modelExhausted {
					continue
				}
				break
			}

			if shift, preview := shouldShiftToNextCopilotModel(resp); shift {
				if i == len(attempts)-1 {
					log.Printf("[copilot-free] model %q failed and no more models remain; forwarding final upstream response", model.ID)
					return p.writeProxyResponse(w, resp, CapabilityChat, embedModel)
				}
				log.Printf("[copilot-free] model %q failed with HTTP %d; shifting to next model. body=%q", model.ID, resp.StatusCode, preview)
				resp.Body.Close()
				continue
			}

			if requestedModel != model.ID {
				log.Printf("[copilot-free] ignoring client model %q; selected %q", requestedModel, model.ID)
			}
			return p.writeProxyResponse(w, resp, CapabilityChat, embedModel)
		}

		if lastLimitResp != nil && modelExhausted {
			p.Mu.Lock()
			advanced := p.AdvanceAccount()
			p.Mu.Unlock()
			if advanced {
				log.Printf("[copilot-free] all models rate-limited on current account; switching to next account")
				continue
			}
			log.Printf("[copilot-free] all accounts exhausted or in cooldown; forwarding rate-limit response")
			return p.writeProxyResponse(w, lastLimitResp, CapabilityChat, "")
		}
	}

	return fmt.Errorf("copilot free-model failover exhausted")
}

func (p *CopilotProvider) resolveFreeChatModels(ctx context.Context) ([]types.Model, error) {
	if p.FreeModelResolver != nil {
		return p.FreeModelResolver(ctx)
	}
	if p.FreeCatalog == nil {
		p.FreeCatalog = NewCopilotFreeCatalog(defaultFreeCatalogTTL)
	}

	docEligible, err := p.FreeCatalog.EligibleModels(ctx)
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

func (p *CopilotProvider) nextFreeChatAttemptOrder(models []types.Model) []types.Model {
	if len(models) <= 1 {
		return append([]types.Model(nil), models...)
	}
	start := int(p.freeModelCursor.Add(1)-1) % len(models)
	ordered := make([]types.Model, 0, len(models))
	ordered = append(ordered, models[start:]...)
	ordered = append(ordered, models[:start]...)
	return ordered
}

func (p *CopilotProvider) executeProxy(
	ctx context.Context,
	r *http.Request,
	body []byte,
	cap Capability,
) (*http.Response, string, error) {
	if p.ProxyExecutor != nil {
		return p.ProxyExecutor(ctx, r, body, cap)
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
		return true, readAndResetResponseBody(resp)
	case resp.StatusCode == http.StatusBadRequest,
		resp.StatusCode == http.StatusNotFound,
		resp.StatusCode == http.StatusConflict:
		preview := strings.ToLower(readAndResetResponseBody(resp))
		if strings.Contains(preview, "model") &&
			(strings.Contains(preview, "unsupported") ||
				strings.Contains(preview, "not available") ||
				strings.Contains(preview, "not found") ||
				strings.Contains(preview, "unavailable")) {
			return true, preview
		}
	}

	return false, ""
}

func isAccountLimitSignal(resp *http.Response) (bool, string) {
	if resp == nil {
		return false, ""
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return true, readAndResetResponseBody(resp)
	}
	if resp.StatusCode == http.StatusForbidden {
		preview := strings.ToLower(readAndResetResponseBody(resp))
		if strings.Contains(preview, "rate limit") ||
			strings.Contains(preview, "exceeded") ||
			strings.Contains(preview, "quota") {
			return true, preview
		}
	}
	return false, ""
}

func readAndResetResponseBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return string(body)
}

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
	httputil.CopyHeaders(w, resp.Header, "Content-Length")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(resp.StatusCode)
	_, err = w.Write(body)
	return err
}

// Health returns the provider's current authentication state.
func (p *CopilotProvider) Health(_ context.Context) ProviderHealth {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	copilotAuth := p.authState()
	hasRefresh := copilotAuth.GitHubToken != ""
	authenticated := copilotAuth.CopilotToken != "" && copilotAuth.ExpiresAt > time.Now().Unix()
	return ProviderHealth{
		Authenticated: authenticated,
		CanRefresh:    hasRefresh,
		LastRefreshAt: time.Unix(copilotAuth.ExpiresAt, 0),
	}
}
