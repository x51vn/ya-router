// codex_provider.go — OpenAI Codex backend provider implementation.
//
// Two credential modes are supported:
//   - "chatgpt" / "device_code": uses ChatGPT-backed Codex credentials from
//     the official Codex auth store (with legacy config fallback). Requests go
//     to chatgpt.com/backend-api/responses and require chatgpt-account-id.
//   - "api_key": uses a user-supplied sk-... API key sent to api.openai.com/v1.
package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	authpkg "github.com/x51vn/github-copilot-svcs/internal/auth"
	"github.com/x51vn/github-copilot-svcs/internal/config"
	"github.com/x51vn/github-copilot-svcs/internal/httputil"
	"github.com/x51vn/github-copilot-svcs/internal/types"
)

const (
	defaultChatGPTBaseURL  = "https://chatgpt.com/backend-api/codex/"
	defaultPlatformBaseURL = "https://api.openai.com/v1"

	DefaultChatGPTBaseURL = defaultChatGPTBaseURL
)

// CodexProvider implements Provider for the OpenAI Codex / API backend.
type CodexProvider struct {
	Cfg           *config.Config
	Mu            sync.Mutex
	Cb            *httputil.CircuitBreaker
	Cache         *ModelCache
	AccountCursor int
	// AuthBroken is set when a permanent, unrecoverable refresh failure occurs
	// (e.g. refresh_token_reused). Once set, all requests fail immediately with
	// a 503 until the process is restarted with fresh credentials.
	AuthBroken    bool
	AuthBrokenErr error
	// ProxyExecutor is nil in production. Tests may set it to intercept outbound
	// requests and return a synthetic response, bypassing real HTTP calls.
	ProxyExecutor func(ctx context.Context, r *http.Request, body []byte, cap Capability) (*http.Response, string, error)
	// RefreshExecutor is nil in production. Tests may set it to intercept
	// CodexRefreshToken calls, enabling control of the token-refresh issuer and
	// retry delay without modifying package-level state.
	RefreshExecutor func(auth *config.CodexAuthState, save func() error) error
}

// setChatGPTHeaders sets authentication headers for chatgpt.com/backend-api/.
func setChatGPTHeaders(req *http.Request, token, accountID string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", authpkg.CodexUserAgent)
	if accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	} else {
		log.Printf("[codex] warning: no account_id — chatgpt.com requests may fail; re-run 'auth codex'")
	}
}

// setPlatformHeaders sets standard Platform API headers (api.openai.com).
func setPlatformHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", authpkg.CodexUserAgent)
}

// NewCodexProvider constructs a CodexProvider from cfg.
func NewCodexProvider(cfg *config.Config) *CodexProvider {
	p := &CodexProvider{
		Cfg:   cfg,
		Cb:    httputil.NewCircuitBreaker(time.Duration(cfg.Timeouts.CircuitBreaker) * time.Second),
		Cache: NewModelCache(DefaultModelCacheTTL),
	}
	p.AccountCursor = p.FirstHealthyCodexAccount()
	if path, err := authpkg.OfficialAuthJSONPath(); err == nil {
		if info, statErr := os.Stat(path); statErr == nil {
			log.Printf("[codex] official auth path: %s (size=%d, mode=%s)", path, info.Size(), info.Mode())
		} else {
			log.Printf("[codex] official auth path: %s (not found: %v)", path, statErr)
		}
	}
	return p
}

func (p *CodexProvider) ID() ProviderID { return ProviderCodex }
func (p *CodexProvider) Name() string   { return "OpenAI Codex" }
func (p *CodexProvider) Capabilities() []Capability {
	return []Capability{CapabilityChat, CapabilityEmbeddings}
}

func (p *CodexProvider) ActiveAccount() *config.CodexAccount {
	accounts := p.Cfg.Providers.Codex.Accounts
	if len(accounts) == 0 {
		return nil
	}
	if p.AccountCursor >= len(accounts) {
		p.AccountCursor = 0
	}
	return &p.Cfg.Providers.Codex.Accounts[p.AccountCursor]
}

func (p *CodexProvider) authState() *config.CodexAuthState {
	if acc := p.ActiveAccount(); acc != nil {
		return &acc.Auth
	}
	return &p.Cfg.Providers.Codex.Auth
}

func (p *CodexProvider) codexCooldownSeconds() int64 {
	if s := p.Cfg.Providers.Codex.AccountCooldownSeconds; s > 0 {
		return int64(s)
	}
	return 300
}

func (p *CodexProvider) IsCodexAccountInCooldown(acc *config.CodexAccount) bool {
	if acc.LastLimitedAt == 0 {
		return false
	}
	return time.Now().Unix()-acc.LastLimitedAt < p.codexCooldownSeconds()
}

func (p *CodexProvider) FirstHealthyCodexAccount() int {
	accounts := p.Cfg.Providers.Codex.Accounts
	for i := range accounts {
		if !p.IsCodexAccountInCooldown(&accounts[i]) {
			return i
		}
	}
	return 0
}

func (p *CodexProvider) AdvanceCodexAccount() bool {
	accounts := p.Cfg.Providers.Codex.Accounts
	if len(accounts) <= 1 {
		return false
	}
	accounts[p.AccountCursor].LastLimitedAt = time.Now().Unix()
	next := (p.AccountCursor + 1) % len(accounts)
	for i := 0; i < len(accounts); i++ {
		idx := (next + i) % len(accounts)
		if !p.IsCodexAccountInCooldown(&accounts[idx]) {
			p.AccountCursor = idx
			return true
		}
	}
	return false
}

func (p *CodexProvider) save() error {
	return config.SaveConfig(p.Cfg)
}

// authCredentials returns the current bearer token, account_id, and mode under lock.
func (p *CodexProvider) authCredentials() (token, accountID string, chatgpt bool) {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	auth := p.authState()
	if authpkg.IsAPIKeyMode(auth.Mode) {
		key, _, err := authpkg.ResolveCodexAPIKey(auth)
		if err != nil {
			log.Printf("[codex] API key resolution failed: %v", err)
			return "", "", false
		}
		return key, "", false
	}
	// chatgpt / device_code mode: bearer token from device auth.
	return auth.AccessToken, auth.AccountID, true
}

// currentToken returns the current access token under lock.
func (p *CodexProvider) currentToken() string {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	return p.authState().AccessToken
}

// reloadTokenFromDisk re-reads the config file and updates the in-memory
// Codex auth state.  This allows the running proxy to pick up tokens
// written by a separate "auth codex" CLI invocation.
func (p *CodexProvider) reloadTokenFromDisk() {
	fresh, err := config.LoadConfig()
	if err != nil {
		log.Printf("[codex] config reload failed: %v", err)
		return
	}
	new := &fresh.Providers.Codex.Auth
	old := p.authState()
	if new.AccessToken != "" && new.AccessToken != old.AccessToken {
		log.Printf("[codex] detected new token on disk — reloading")
		old.AccessToken = new.AccessToken
		old.RefreshToken = new.RefreshToken
		old.ExpiresAt = new.ExpiresAt
		old.AccountID = new.AccountID
	} else if old.AccountID == "" && new.AccountID != "" {
		// Same token but account_id was subsequently added to disk (e.g. by
		// a separate 'auth codex' invocation running against the same config).
		log.Printf("[codex] updated account_id metadata from on-disk config")
		old.AccountID = new.AccountID
	}
}

// reloadFromOfficialStore reads the official Codex auth store and
// merges fresh credentials into the in-memory auth state.
func (p *CodexProvider) reloadFromOfficialStore() {
	official, err := authpkg.LoadOfficialCodexAuth()
	if err != nil {
		log.Printf("[codex] official store load failed: %v", err)
		return
	}
	if official == nil {
		return
	}
	auth := p.authState()
	if official.AccessToken != "" && official.AccessToken != auth.AccessToken {
		log.Printf("[codex] loaded fresh token from official store")
		auth.AccessToken = official.AccessToken
		auth.RefreshToken = official.RefreshToken
		auth.ExpiresAt = official.ExpiresAt
		auth.AccountID = official.AccountID
	} else if auth.AccountID == "" && official.AccountID != "" {
		auth.AccountID = official.AccountID
	}
}

// EnsureAuthenticated ensures a valid Codex credential is available.
func (p *CodexProvider) EnsureAuthenticated(_ context.Context) error {
	p.Mu.Lock()
	defer p.Mu.Unlock()

	auth := p.authState()

	// API key mode — nothing to refresh.
	if authpkg.IsAPIKeyMode(auth.Mode) {
		if key, _, err := authpkg.ResolveCodexAPIKey(auth); err == nil && key != "" {
			return nil
		}
		return fmt.Errorf("no Codex API key — run 'auth codex --api-key' first")
	}

	resolved, err := authpkg.ResolveCodexChatGPTAuth(auth)
	if err != nil {
		return fmt.Errorf("load Codex credentials: %w", err)
	}
	if resolved != nil {
		authpkg.ApplyResolvedCodexChatGPTAuth(auth, resolved)
	}
	if auth.AccessToken == "" {
		p.reloadTokenFromDisk()
		if auth.AccessToken == "" {
			return fmt.Errorf("no Codex token — run 'auth codex' first")
		}
	}
	if auth.AccountID == "" {
		p.reloadFromOfficialStore()
		if auth.AccountID == "" {
			p.reloadTokenFromDisk()
		}
		if auth.AccountID != "" {
			log.Printf("[codex] picked up account_id metadata from on-disk credentials")
		}
	}

	// Check expiry and refresh if needed.
	now := time.Now().Unix()
	if auth.ExpiresAt > 0 {
		remaining := auth.ExpiresAt - now
		threshold := int64(300)
		if remaining <= threshold {
			log.Printf("[codex] token expiring in %ds, refreshing...", remaining)
			refreshFn := authpkg.CodexRefreshToken
			if p.RefreshExecutor != nil {
				refreshFn = p.RefreshExecutor
			}
			if err := refreshFn(auth, p.save); err != nil {
				log.Printf("[codex] refresh failed: %v", err)
				if errors.Is(err, authpkg.ErrRefreshUnrecoverable) {
					p.reloadFromOfficialStore()
					freshAfterReload := auth.AccessToken != "" && auth.ExpiresAt > now+300
					if !freshAfterReload {
						p.AuthBroken = true
						p.AuthBrokenErr = err
						log.Printf("[codex] PERMANENT auth failure — re-authenticate with './github-copilot-svcs auth codex' and restart the service. All Codex requests will fail until then.")
						return fmt.Errorf("codex auth broken (re-authenticate and restart): %w", err)
					}
				} else {
					p.reloadFromOfficialStore()
					freshAfterReload := auth.AccessToken != "" && auth.ExpiresAt > now+300
					if !freshAfterReload {
						return fmt.Errorf("codex token expired and refresh failed: %w", err)
					}
				}
			} else {
				log.Printf("[codex] token refreshed, new expiry in %ds",
					auth.ExpiresAt-time.Now().Unix())
			}
		} else {
			log.Printf("[codex] token valid, expires in %dm%ds",
				remaining/60, remaining%60)
		}
	}

	// Final fallback: the device-code access_token is itself a JWT that
	// contains chatgpt_account_id.  Extract it directly so requests work
	// even when neither config.json nor ~/.codex/auth.json have the field.
	if auth.AccountID == "" && auth.AccessToken != "" {
		if aid := authpkg.ExtractAccountIDFromJWT(auth.AccessToken); aid != "" {
			auth.AccountID = aid
			log.Printf("[codex] extracted account_id metadata from access_token JWT")
			// Persist so we don't repeat this on every request.
			_ = p.save()
		}
	}

	return nil
}

// codexKnownModels is the fallback catalog used when the Codex backend
// cannot enumerate models directly. It mirrors the latest slugs exposed by
// the official Codex CLI models cache, including the 5.x family.
var codexKnownModels = []types.Model{
	{ID: "gpt-5.5", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.4", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.4-mini", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.3-codex", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.2-codex", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.1-codex-max", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.1-codex", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.2", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.1", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5-codex", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-oss-120b", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-oss-20b", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.1-codex-mini", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5-codex-mini", Object: "model", OwnedBy: "openai"},
}

func CodexKnownModelIDs() []string {
	ids := make([]string, len(codexKnownModels))
	for i, m := range codexKnownModels {
		ids[i] = m.ID
	}
	return ids
}

func (p *CodexProvider) ListModels(ctx context.Context) (*types.ModelList, error) {
	if err := p.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}
	return p.Cache.GetOrFetch(func() (*types.ModelList, error) {
		return p.fetchModels(ctx)
	})
}

// fetchModels returns the known-models list (device_code JWT lacks
// api.model.read scope, so we never call the upstream /models endpoint).
func (p *CodexProvider) fetchModels(_ context.Context) (*types.ModelList, error) {
	return p.KnownModelList(), nil
}

// knownModelList returns the canonical Codex model list by loading the
// official Codex CLI cache first and falling back to the static catalog only
// when the cache is unavailable.
func (p *CodexProvider) KnownModelList() *types.ModelList {
	now := time.Now().Unix()
	models := make([]types.Model, 0, len(codexKnownModels))

	if officialModels, err := authpkg.LoadOfficialCodexModels(); err != nil {
		log.Printf("[codex] warning: failed to load official model cache: %v", err)
	} else if len(officialModels) > 0 {
		for i := range officialModels {
			officialModels[i].Created = now
		}
		models = append(models, officialModels...)
	} else {
		for _, m := range codexKnownModels {
			m.Created = now
			models = append(models, m)
		}
	}

	seen := make(map[string]bool, len(models))
	for _, m := range models {
		seen[m.ID] = true
	}
	// Also include any routing.model_map entries targeting codex.
	for modelID, entry := range p.Cfg.Routing.ModelMap {
		if ProviderID(entry.Provider) == ProviderCodex && !seen[modelID] {
			models = append(models, types.Model{
				ID:      modelID,
				Object:  "model",
				Created: now,
				OwnedBy: "openai",
			})
		}
	}
	ml := &types.ModelList{Object: "list", Data: models}
	return ml
}

// ProxyRequest proxies chat or embeddings requests upstream.
// ChatGPT / device_code mode sends to chatgpt.com/backend-api/responses
// with the chatgpt-account-id header; api_key mode sends to api.openai.com/v1.
func (p *CodexProvider) ProxyRequest(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	cap Capability,
) error {
	p.Mu.Lock()
	if p.AuthBroken {
		p.reloadFromOfficialStore()
		auth := p.authState()
		if auth.AccessToken != "" && auth.ExpiresAt > time.Now().Unix()+300 {
			log.Printf("[codex] recovered from broken auth state via official store reload")
			p.AuthBroken = false
			p.AuthBrokenErr = nil
		}
	}
	broken := p.AuthBroken
	p.Mu.Unlock()
	if broken {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"Codex authentication is broken. Re-authenticate with 'auth codex' and restart the service.","type":"authentication_error","code":"provider_auth_broken"}}`))
		return nil
	}
	if err := p.EnsureAuthenticated(ctx); err != nil {
		log.Printf("[codex] auth failed: %v", err)
		return fmt.Errorf("codex auth: %w", err)
	}
	if !p.Cb.CanExecute() {
		log.Printf("[codex] circuit breaker OPEN — rejecting request")
		return fmt.Errorf("codex circuit breaker is open")
	}

	p.Mu.Lock()
	accounts := p.Cfg.Providers.Codex.Accounts
	maxAttempts := 1
	if len(accounts) > 1 {
		maxAttempts = len(accounts)
	}
	p.Mu.Unlock()

	var lastResp *http.Response
	retriedOn401 := false
	for attempt := 0; attempt < maxAttempts; attempt++ {
		token, accountID, chatgpt := p.authCredentials()

		// Embeddings — classic Platform API endpoint (both modes).
		if cap == CapabilityEmbeddings {
			return p.proxyClassic(ctx, w, r, body, "/embeddings", token)
		}

		// Chat: choose the transport-specific request builder.
		streaming := isStreamingRequest(body)

		var (
			responsesBody []byte
			includeUsage  bool
			upstreamURL   string
		)
		if chatgpt {
			upstreamURL = defaultChatGPTBaseURL + "responses"
			var err error
			responsesBody, includeUsage, err = buildChatGPTCodexRequest(body)
			if err != nil {
				log.Printf("[codex] chatgpt request build failed: %v — falling back to classic", err)
				return p.proxyClassic(ctx, w, r, body, "/chat/completions", token)
			}
		} else {
			upstreamURL = defaultPlatformBaseURL + "/responses"
			var err error
			responsesBody, includeUsage, err = buildPlatformResponsesRequest(body)
			if err != nil {
				log.Printf("[codex] platform request build failed: %v — falling back to classic", err)
				return p.proxyClassic(ctx, w, r, body, "/chat/completions", token)
			}
		}
		log.Printf("[codex] proxying chat → %s (body %d bytes, stream=%v, include_usage=%v, mode=%s, attempt=%d/%d)",
			upstreamURL, len(responsesBody), streaming, includeUsage,
			map[bool]string{true: "chatgpt", false: "api_key"}[chatgpt], attempt+1, maxAttempts)

		req, err := http.NewRequestWithContext(ctx, "POST",
			upstreamURL, bytes.NewBuffer(responsesBody))
		if err != nil {
			return err
		}
		if chatgpt {
			setChatGPTHeaders(req, token, accountID)
		} else {
			setPlatformHeaders(req, token)
		}

		var resp *http.Response
		if p.ProxyExecutor != nil {
			resp, _, err = p.ProxyExecutor(ctx, req, responsesBody, cap)
		} else {
			resp, err = httputil.MakeRequestWithRetry(httputil.SharedHTTPClient, req, responsesBody)
		}
		if err != nil {
			log.Printf("[codex] upstream error: %v", err)
			p.Cb.OnFailure()
			return err
		}

		log.Printf("[codex] upstream responded HTTP %d (Content-Type: %s)",
			resp.StatusCode, resp.Header.Get("Content-Type"))

		if resp.StatusCode == http.StatusUnauthorized && !retriedOn401 {
			resp.Body.Close()
			retriedOn401 = true
			maxAttempts++
			log.Printf("[codex] upstream 401 — refreshing token and retrying")
			p.Mu.Lock()
			auth := p.authState()
			auth.ExpiresAt = 1
			p.Mu.Unlock()
			if err := p.EnsureAuthenticated(ctx); err != nil {
				if errors.Is(err, authpkg.ErrRefreshUnrecoverable) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":{"message":"Codex authentication is broken. Re-authenticate with 'auth codex' and restart the service.","type":"authentication_error","code":"provider_auth_broken"}}`))
					return nil
				}
				log.Printf("[codex] 401-triggered refresh failed (transient): %v — returning 401 to client", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"upstream authentication failed and token refresh failed","type":"authentication_error","code":"unauthorized"}}`))
				return nil
			}
			log.Printf("[codex] token refreshed after 401, retrying upstream call")
			continue
		}

		if resp.StatusCode == http.StatusUnauthorized && retriedOn401 {
			log.Printf("[codex] upstream 401 persists after token refresh — passing through to client")
			p.Cb.OnFailure()
			return handleResponsesAPIResponse(w, resp, streaming, chatgpt, includeUsage)
		}

		isLimit, limitReason := isAccountLimitSignal(resp)
		if isLimit && maxAttempts > 1 {
			log.Printf("[codex] account limit signal (%s) — advancing to next account", limitReason)
			resp.Body.Close()
			lastResp = resp
			p.Mu.Lock()
			advanced := p.AdvanceCodexAccount()
			p.Mu.Unlock()
			if !advanced {
				log.Printf("[codex] all accounts exhausted")
				break
			}
			continue
		}

		if resp.StatusCode < 500 {
			p.Cb.OnSuccess()
		} else {
			log.Printf("[codex] upstream 5xx error — circuit breaker failure")
			p.Cb.OnFailure()
		}

		return handleResponsesAPIResponse(w, resp, streaming, chatgpt, includeUsage)
	}

	// All accounts exhausted — forward last rate-limit response.
	if lastResp != nil {
		log.Printf("[codex] all accounts exhausted, forwarding last %d response", lastResp.StatusCode)
		w.WriteHeader(lastResp.StatusCode)
		return nil
	}
	return fmt.Errorf("codex: all accounts exhausted with no upstream response")
}

// proxyClassic sends a request to a classic OpenAI Platform endpoint
// (embeddings or chat/completions fallback).
func (p *CodexProvider) proxyClassic(
	ctx context.Context,
	w http.ResponseWriter,
	_ *http.Request,
	body []byte,
	path string,
	token string,
) error {
	upstreamURL := defaultPlatformBaseURL + path
	log.Printf("[codex] proxying classic → %s (body %d bytes)", upstreamURL, len(body))

	req, err := http.NewRequestWithContext(ctx, "POST",
		upstreamURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	setPlatformHeaders(req, token)

	resp, err := httputil.MakeRequestWithRetry(httputil.SharedHTTPClient, req, body)
	if err != nil {
		log.Printf("[codex] upstream error: %v", err)
		p.Cb.OnFailure()
		return err
	}
	defer resp.Body.Close()

	log.Printf("[codex] upstream responded HTTP %d (Content-Type: %s)",
		resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		peekBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		log.Printf("[codex] upstream %d response: %s",
			resp.StatusCode, string(peekBody))
		resp.Body = io.NopCloser(
			io.MultiReader(bytes.NewReader(peekBody), resp.Body))
	}

	if resp.StatusCode < 500 {
		p.Cb.OnSuccess()
	} else {
		log.Printf("[codex] upstream 5xx error — circuit breaker failure")
		p.Cb.OnFailure()
	}
	return httputil.StreamResponse(w, resp)
}

func (p *CodexProvider) ProxyResponsesRequest(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	_ string,
) error {
	streaming := isStreamingRequest(body)
	return p.proxyResponsesBody(ctx, w, r, body, streaming)
}

func (p *CodexProvider) proxyResponsesBody(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	streaming bool,
) error {
	p.Mu.Lock()
	if p.AuthBroken {
		p.reloadFromOfficialStore()
		auth := p.authState()
		if auth.AccessToken != "" && auth.ExpiresAt > time.Now().Unix()+300 {
			log.Printf("[codex] recovered from broken auth state via official store reload")
			p.AuthBroken = false
			p.AuthBrokenErr = nil
		}
	}
	broken := p.AuthBroken
	p.Mu.Unlock()
	if broken {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"Codex authentication is broken. Re-authenticate with 'auth codex' and restart the service.","type":"authentication_error","code":"provider_auth_broken"}}`))
		return nil
	}
	if err := p.EnsureAuthenticated(ctx); err != nil {
		log.Printf("[codex] auth failed: %v", err)
		return fmt.Errorf("codex auth: %w", err)
	}
	if !p.Cb.CanExecute() {
		log.Printf("[codex] circuit breaker OPEN — rejecting request")
		return fmt.Errorf("codex circuit breaker is open")
	}

	p.Mu.Lock()
	accounts := p.Cfg.Providers.Codex.Accounts
	maxAttempts := 1
	if len(accounts) > 1 {
		maxAttempts = len(accounts)
	}
	p.Mu.Unlock()

	var lastResp *http.Response
	retriedOn401 := false
	for attempt := 0; attempt < maxAttempts; attempt++ {
		token, accountID, chatgpt := p.authCredentials()

		upstreamURL := defaultPlatformBaseURL + "/responses"
		responsesBody := body
		includeUsage := false
		if chatgpt {
			upstreamURL = defaultChatGPTBaseURL + "responses"
			var err error
			responsesBody, includeUsage, err = buildChatGPTCodexRequestFromResponses(body)
			if err != nil {
				log.Printf("[codex] chatgpt responses request build failed: %v", err)
				return err
			}
		}

		log.Printf("[codex] proxying responses → %s (body %d bytes, stream=%v, mode=%s, attempt=%d/%d)",
			upstreamURL, len(responsesBody), streaming,
			map[bool]string{true: "chatgpt", false: "api_key"}[chatgpt], attempt+1, maxAttempts)

		req, err := http.NewRequestWithContext(ctx, "POST", upstreamURL, bytes.NewBuffer(responsesBody))
		if err != nil {
			return err
		}
		if chatgpt {
			setChatGPTHeaders(req, token, accountID)
		} else {
			setPlatformHeaders(req, token)
		}

		var resp *http.Response
		if p.ProxyExecutor != nil {
			resp, _, err = p.ProxyExecutor(ctx, req, responsesBody, CapabilityResponses)
		} else {
			resp, err = httputil.MakeRequestWithRetry(httputil.SharedHTTPClient, req, responsesBody)
		}
		if err != nil {
			log.Printf("[codex] upstream error: %v", err)
			p.Cb.OnFailure()
			return err
		}

		log.Printf("[codex] upstream responded HTTP %d (Content-Type: %s)",
			resp.StatusCode, resp.Header.Get("Content-Type"))

		if resp.StatusCode == http.StatusUnauthorized && !retriedOn401 {
			resp.Body.Close()
			retriedOn401 = true
			maxAttempts++
			log.Printf("[codex] upstream 401 — refreshing token and retrying")
			p.Mu.Lock()
			auth := p.authState()
			auth.ExpiresAt = 1
			p.Mu.Unlock()
			if err := p.EnsureAuthenticated(ctx); err != nil {
				if errors.Is(err, authpkg.ErrRefreshUnrecoverable) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":{"message":"Codex authentication is broken. Re-authenticate with 'auth codex' and restart the service.","type":"authentication_error","code":"provider_auth_broken"}}`))
					return nil
				}
				log.Printf("[codex] 401-triggered refresh failed (transient): %v — returning 401 to client", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"upstream authentication failed and token refresh failed","type":"authentication_error","code":"unauthorized"}}`))
				return nil
			}
			log.Printf("[codex] token refreshed after 401, retrying upstream call")
			continue
		}

		if resp.StatusCode == http.StatusUnauthorized && retriedOn401 {
			log.Printf("[codex] upstream 401 persists after token refresh — passing through to client")
			p.Cb.OnFailure()
			return handleResponsesAPIResponse(w, resp, streaming, chatgpt, includeUsage)
		}

		isLimit, limitReason := isAccountLimitSignal(resp)
		if isLimit && maxAttempts > 1 {
			log.Printf("[codex] account limit signal (%s) — advancing to next account", limitReason)
			resp.Body.Close()
			lastResp = resp
			p.Mu.Lock()
			advanced := p.AdvanceCodexAccount()
			p.Mu.Unlock()
			if !advanced {
				log.Printf("[codex] all accounts exhausted")
				break
			}
			continue
		}

		if resp.StatusCode < 500 {
			p.Cb.OnSuccess()
		} else {
			log.Printf("[codex] upstream 5xx error — circuit breaker failure")
			p.Cb.OnFailure()
		}

		return handleResponsesAPIResponse(w, resp, streaming, chatgpt, includeUsage)
	}

	if lastResp != nil {
		log.Printf("[codex] all accounts exhausted, forwarding last %d response", lastResp.StatusCode)
		w.WriteHeader(lastResp.StatusCode)
		return nil
	}
	return fmt.Errorf("codex: all accounts exhausted with no upstream response")
}

// Health returns the provider's authentication state.
func (p *CodexProvider) Health(_ context.Context) ProviderHealth {
	p.Mu.Lock()
	defer p.Mu.Unlock()
	auth := p.authState()
	authenticated := auth.AccessToken != ""
	if auth.ExpiresAt > 0 {
		authenticated = authenticated && auth.ExpiresAt > time.Now().Unix()
	}
	hasRefresh := auth.RefreshToken != ""
	return ProviderHealth{Authenticated: authenticated, CanRefresh: hasRefresh}
}
