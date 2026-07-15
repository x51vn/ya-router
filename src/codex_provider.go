// codex_provider.go — OpenAI Codex backend provider implementation.
//
// ChatGPT auth uses the ChatGPT/Codex backend only. API-key auth uses OpenAI
// Platform endpoints only. Credentials never cross those transport domains.
package yarouter

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultChatGPTBaseURL  = "https://chatgpt.com/backend-api/codex/"
	defaultPlatformBaseURL = "https://api.openai.com/v1"
)

// CodexProvider implements Provider for ChatGPT-backed Codex and Platform API mode.
type CodexProvider struct {
	cfg           *Config
	mu            sync.Mutex
	cb            *CircuitBreaker
	cache         *ModelCache
	accountCursor int
	// proxyExecutor is nil in production. Tests may intercept outbound requests.
	proxyExecutor func(ctx context.Context, r *http.Request, body []byte, cap Capability) (*http.Response, string, error)
}

func setChatGPTHeaders(req *http.Request, token, accountID string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", codexUserAgent)
	if accountID != "" {
		req.Header.Set("chatgpt-account-id", accountID)
	}
}

func setPlatformHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", codexUserAgent)
}

func NewCodexProvider(cfg *Config) *CodexProvider {
	p := &CodexProvider{
		cfg: cfg,
		cb: &CircuitBreaker{
			state:   CircuitClosed,
			timeout: time.Duration(cfg.Timeouts.CircuitBreaker) * time.Second,
		},
		cache: NewModelCache(defaultModelCacheTTL),
	}
	p.accountCursor = p.firstHealthyCodexAccount()
	return p
}

func (p *CodexProvider) ID() ProviderID { return ProviderCodex }
func (p *CodexProvider) Name() string   { return "OpenAI Codex" }

// Capabilities depend on the selected auth transport. ChatGPT credentials do
// not have Platform embedding scope.
func (p *CodexProvider) Capabilities() []Capability {
	p.mu.Lock()
	defer p.mu.Unlock()
	if isAPIKeyMode(p.authState().Mode) {
		return []Capability{CapabilityChat, CapabilityResponses, CapabilityEmbeddings}
	}
	return []Capability{CapabilityChat, CapabilityResponses}
}

func (p *CodexProvider) chatGPTBaseURL() string {
	base := strings.TrimSpace(p.cfg.Providers.Codex.ChatGPTBaseURL)
	if base == "" {
		base = defaultChatGPTBaseURL
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base
}

func (p *CodexProvider) activeAccount() *CodexAccount {
	accounts := p.cfg.Providers.Codex.Accounts
	if len(accounts) == 0 {
		return nil
	}
	if p.accountCursor < 0 || p.accountCursor >= len(accounts) {
		p.accountCursor = 0
	}
	return &p.cfg.Providers.Codex.Accounts[p.accountCursor]
}

func (p *CodexProvider) authState() *CodexAuthState {
	if account := p.activeAccount(); account != nil {
		return &account.Auth
	}
	return &p.cfg.Providers.Codex.Auth
}

func (p *CodexProvider) codexCooldownSeconds() int64 {
	if seconds := p.cfg.Providers.Codex.AccountCooldownSeconds; seconds > 0 {
		return int64(seconds)
	}
	return 300
}

func (p *CodexProvider) isCodexAccountInCooldown(account *CodexAccount) bool {
	if account == nil || account.LastLimitedAt == 0 {
		return false
	}
	return time.Now().Unix()-account.LastLimitedAt < p.codexCooldownSeconds()
}

func (p *CodexProvider) firstHealthyCodexAccount() int {
	accounts := p.cfg.Providers.Codex.Accounts
	for i := range accounts {
		if !p.isCodexAccountInCooldown(&accounts[i]) {
			return i
		}
	}
	return 0
}

func (p *CodexProvider) advanceCodexAccount() bool {
	return p.advanceCodexAccountFrom(p.accountCursor)
}

// advanceCodexAccountFrom marks the account that served the failed request,
// even if another concurrent response already changed the shared cursor.
// Caller must hold p.mu.
func (p *CodexProvider) advanceCodexAccountFrom(failedIndex int) bool {
	accounts := p.cfg.Providers.Codex.Accounts
	if failedIndex < 0 || failedIndex >= len(accounts) {
		return false
	}
	accounts[failedIndex].LastLimitedAt = time.Now().Unix()
	if len(accounts) <= 1 {
		return false
	}
	next := p.accountCursor
	if next == failedIndex {
		next = (failedIndex + 1) % len(accounts)
	}
	for i := 0; i < len(accounts); i++ {
		index := (next + i) % len(accounts)
		if index == failedIndex {
			continue
		}
		if !p.isCodexAccountInCooldown(&accounts[index]) {
			p.accountCursor = index
			return true
		}
	}
	return false
}

func (p *CodexProvider) save() error { return persistCodexRuntimeAccount(p.cfg, p.accountCursor) }

func (p *CodexProvider) authCredentials() (token, accountID string, chatgpt bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	credential := p.credentialAtLocked(p.accountCursor)
	return credential.token, credential.accountID, credential.chatgpt
}

type codexCredential struct {
	index     int
	token     string
	accountID string
	chatgpt   bool
}

func (p *CodexProvider) selectedCredential() codexCredential {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.credentialAtLocked(p.accountCursor)
}

func (p *CodexProvider) credentialAtLocked(index int) codexCredential {
	auth := &p.cfg.Providers.Codex.Auth
	if len(p.cfg.Providers.Codex.Accounts) > 0 && index >= 0 && index < len(p.cfg.Providers.Codex.Accounts) {
		auth = &p.cfg.Providers.Codex.Accounts[index].Auth
	}
	if isAPIKeyMode(auth.Mode) {
		key, _, err := resolveCodexAPIKey(auth)
		if err != nil {
			log.Printf("[codex] API-key resolution failed: %v", err)
			return codexCredential{index: index}
		}
		return codexCredential{index: index, token: key}
	}
	return codexCredential{index: index, token: auth.AccessToken, accountID: auth.AccountID, chatgpt: true}
}

func (p *CodexProvider) currentToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.authState().AccessToken
}

// reloadTokenFromDisk refreshes the selected account from ya-router's own config.
func (p *CodexProvider) reloadTokenFromDisk() {
	fresh, err := loadConfig()
	if err != nil {
		log.Printf("[codex] config reload failed: %v", err)
		return
	}
	current := p.activeAccount()
	if current != nil {
		for i := range fresh.Providers.Codex.Accounts {
			candidate := &fresh.Providers.Codex.Accounts[i]
			if candidate.Label == current.Label && candidate.Auth.AccessToken != "" {
				current.Auth = candidate.Auth
				return
			}
		}
		return
	}
	if fresh.Providers.Codex.Auth.AccessToken != "" {
		p.cfg.Providers.Codex.Auth = fresh.Providers.Codex.Auth
	}
}

// reloadFromOfficialStore performs read-only import and never overwrites an
// account-owned credential.
func (p *CodexProvider) reloadFromOfficialStore() {
	auth := p.authState()
	if auth.AccessToken != "" {
		return
	}
	official, err := loadOfficialCodexAuth()
	if err != nil {
		log.Printf("[codex] official credential import failed: %v", err)
		return
	}
	if official == nil || official.AccessToken == "" {
		return
	}
	auth.Mode = "chatgpt"
	auth.AccessToken = official.AccessToken
	auth.RefreshToken = official.RefreshToken
	auth.ExpiresAt = official.ExpiresAt
	auth.AccountID = official.AccountID
}

// EnsureAuthenticated validates the selected account. The provider mutex also
// provides single-flight refresh behavior for concurrent requests.
func (p *CodexProvider) EnsureAuthenticated(_ context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	auth := p.authState()

	if isAPIKeyMode(auth.Mode) {
		key, _, err := resolveCodexAPIKey(auth)
		if err == nil && key != "" {
			return nil
		}
		return newProviderError(ProviderCodex, ProviderErrorAuthRequired, http.StatusUnauthorized, false, "no Codex API key configured")
	}

	if auth.AccessToken == "" {
		p.reloadTokenFromDisk()
	}
	// The official Codex store represents one global account. Import it only for
	// legacy/single-account setups; never use it to override a pool member.
	if auth.AccessToken == "" && len(p.cfg.Providers.Codex.Accounts) <= 1 {
		p.reloadFromOfficialStore()
	}
	if auth.AccessToken == "" {
		return newProviderError(ProviderCodex, ProviderErrorAuthRequired, http.StatusUnauthorized, false, "no Codex token; run 'auth codex'")
	}
	if auth.AccountID == "" {
		auth.AccountID = extractAccountIDFromJWT(auth.AccessToken)
	}
	if auth.ExpiresAt == 0 {
		auth.ExpiresAt = extractJWTExpiry(auth.AccessToken)
	}

	now := time.Now().Unix()
	if auth.ExpiresAt > 0 && auth.ExpiresAt-now <= 300 {
		if err := codexRefreshToken(auth, p.save); err != nil {
			if auth.ExpiresAt <= now {
				return newProviderError(ProviderCodex, ProviderErrorAuthRequired, http.StatusUnauthorized, false, "Codex token expired and refresh failed: %v", err)
			}
			log.Printf("[codex] proactive refresh failed; current token remains valid: %v", err)
		}
	}
	return nil
}

func (p *CodexProvider) forceRefreshAccount(index int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	auth := &p.cfg.Providers.Codex.Auth
	if len(p.cfg.Providers.Codex.Accounts) > 0 {
		if index < 0 || index >= len(p.cfg.Providers.Codex.Accounts) {
			return fmt.Errorf("Codex account index %d is no longer valid", index)
		}
		auth = &p.cfg.Providers.Codex.Accounts[index].Auth
	}
	if isAPIKeyMode(auth.Mode) {
		return fmt.Errorf("API-key credentials do not refresh")
	}
	return codexRefreshToken(auth, func() error { return persistCodexRuntimeAccount(p.cfg, index) })
}

// codexKnownModels is a compatibility manifest. Model-map entries are merged
// so operators can add rollout models without rebuilding the binary.
var codexKnownModels = []Model{
	{ID: "gpt-5.4-mini", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.3-codex", Object: "model", OwnedBy: "openai"},
	{ID: "gpt-5.4", Object: "model", OwnedBy: "openai"},
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

func (p *CodexProvider) ListModels(ctx context.Context) (*ModelList, error) {
	if err := p.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}
	return p.cache.GetOrFetch(func() (*ModelList, error) { return p.fetchModels(ctx) })
}

func (p *CodexProvider) fetchModels(_ context.Context) (*ModelList, error) {
	return p.knownModelList(), nil
}

func (p *CodexProvider) InvalidateModelCache() {
	p.cache.Invalidate()
}

func (p *CodexProvider) knownModelList() *ModelList {
	now := time.Now().Unix()
	seen := make(map[string]bool, len(codexKnownModels))
	models := make([]Model, 0, len(codexKnownModels))
	for _, model := range codexKnownModels {
		model.Created = now
		seen[model.ID] = true
		models = append(models, model)
	}
	for modelID, entry := range p.cfg.Routing.ModelMap {
		bareModel, prefixProvider, hasPrefix := StripModelPrefix(modelID)
		if ProviderID(entry.Provider) != ProviderCodex && (!hasPrefix || prefixProvider != ProviderCodex) {
			continue
		}
		if seen[bareModel] {
			continue
		}
		models = append(models, Model{ID: bareModel, Object: "model", Created: now, OwnedBy: "openai"})
		seen[bareModel] = true
	}
	return &ModelList{Object: "list", Data: models}
}

// ProxyRequest executes Chat Completions, native Responses, or embeddings.
func (p *CodexProvider) ProxyRequest(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	capability Capability,
) error {
	p.mu.Lock()
	maxAccountAttempts := len(p.cfg.Providers.Codex.Accounts)
	if maxAccountAttempts < 1 {
		maxAccountAttempts = 1
	}
	p.mu.Unlock()

	for accountAttempt := 0; accountAttempt < maxAccountAttempts; accountAttempt++ {
		if err := p.EnsureAuthenticated(ctx); err != nil {
			return err
		}
		if !p.cb.canExecute() {
			return newProviderError(ProviderCodex, ProviderErrorUnavailable, http.StatusServiceUnavailable, true, "Codex circuit breaker is open")
		}
		credential := p.selectedCredential()
		token, accountID, chatgpt := credential.token, credential.accountID, credential.chatgpt
		if token == "" {
			return newProviderError(ProviderCodex, ProviderErrorAuthRequired, http.StatusUnauthorized, false, "Codex credential is empty")
		}

		if capability == CapabilityEmbeddings {
			if chatgpt {
				return newProviderError(ProviderCodex, ProviderErrorUnsupported, http.StatusBadRequest, false, "embeddings require OpenAI Platform API-key mode")
			}
			return p.proxyClassic(ctx, w, body, "/embeddings", token)
		}
		if capability != CapabilityChat && capability != CapabilityResponses {
			return newProviderError(ProviderCodex, ProviderErrorUnsupported, http.StatusBadRequest, false, "unsupported Codex capability %q", capability)
		}

		nativeResponses := capability == CapabilityResponses
		var upstreamBody []byte
		var clientWantsStream bool
		var includeUsage bool
		var upstreamURL string
		var buildErr error
		if chatgpt {
			upstreamURL = p.chatGPTBaseURL() + "responses"
			if nativeResponses {
				upstreamBody, clientWantsStream, buildErr = buildChatGPTNativeResponsesRequest(body)
			} else {
				upstreamBody, includeUsage, buildErr = buildChatGPTCodexRequest(body)
				clientWantsStream = isStreamingRequest(body)
			}
		} else {
			upstreamURL = defaultPlatformBaseURL + "/responses"
			if nativeResponses {
				upstreamBody, clientWantsStream, buildErr = buildPlatformNativeResponsesRequest(body)
			} else {
				upstreamBody, includeUsage, buildErr = buildPlatformResponsesRequest(body)
				clientWantsStream = isStreamingRequest(body)
			}
		}
		if buildErr != nil {
			return newProviderError(ProviderCodex, ProviderErrorInvalidRequest, http.StatusBadRequest, false, "build upstream request: %v", buildErr)
		}

		var response *http.Response
		var requestErr error
		for authAttempt := 0; authAttempt < 2; authAttempt++ {
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewBuffer(upstreamBody))
			if err != nil {
				return err
			}
			if chatgpt {
				setChatGPTHeaders(request, token, accountID)
			} else {
				setPlatformHeaders(request, token)
			}
			if p.proxyExecutor != nil {
				response, _, requestErr = p.proxyExecutor(ctx, request, upstreamBody, capability)
			} else {
				response, requestErr = makeRequestWithRetry(sharedHTTPClient, request, upstreamBody)
			}
			if requestErr != nil {
				p.cb.onFailure()
				return newProviderError(ProviderCodex, ProviderErrorTransport, http.StatusBadGateway, true, "Codex upstream request failed: %v", requestErr)
			}
			if response.StatusCode == http.StatusUnauthorized && chatgpt && authAttempt == 0 {
				response.Body.Close()
				if refreshErr := p.forceRefreshAccount(credential.index); refreshErr != nil {
					return newProviderError(ProviderCodex, ProviderErrorAuthRequired, http.StatusUnauthorized, false, "Codex authentication expired: %v", refreshErr)
				}
				p.mu.Lock()
				refreshed := p.credentialAtLocked(credential.index)
				p.mu.Unlock()
				token, accountID = refreshed.token, refreshed.accountID
				continue
			}
			break
		}
		if response == nil {
			return newProviderError(ProviderCodex, ProviderErrorTransport, http.StatusBadGateway, true, "Codex returned no response")
		}

		limited, reason := isAccountLimitSignal(response)
		if limited && maxAccountAttempts > 1 {
			p.mu.Lock()
			advanced := p.advanceCodexAccountFrom(credential.index)
			p.mu.Unlock()
			if advanced {
				log.Printf("[codex] account limit signal (%s); advancing account", reason)
				response.Body.Close()
				continue
			}
		}

		if response.StatusCode >= 500 {
			p.cb.onFailure()
		} else {
			p.cb.onSuccess()
		}
		if nativeResponses {
			return handleNativeResponsesAPIResponse(w, response, clientWantsStream, chatgpt)
		}
		return handleResponsesAPIResponse(w, response, clientWantsStream, chatgpt, includeUsage)
	}
	return newProviderError(ProviderCodex, ProviderErrorRateLimit, http.StatusTooManyRequests, true, "all Codex accounts are rate limited")
}

// proxyClassic is used only with Platform API-key credentials.
func (p *CodexProvider) proxyClassic(ctx context.Context, w http.ResponseWriter, body []byte, path, token string) error {
	upstreamURL := defaultPlatformBaseURL + path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	setPlatformHeaders(request, token)
	response, err := makeRequestWithRetry(sharedHTTPClient, request, body)
	if err != nil {
		p.cb.onFailure()
		return newProviderError(ProviderCodex, ProviderErrorTransport, http.StatusBadGateway, true, "Platform upstream request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 && response.StatusCode < 500 {
		log.Printf("[codex] Platform upstream rejected request with HTTP %d", response.StatusCode)
	}
	if response.StatusCode >= 500 {
		p.cb.onFailure()
	} else {
		p.cb.onSuccess()
	}
	return streamResponse(w, response)
}

func (p *CodexProvider) Health(_ context.Context) ProviderHealth {
	p.mu.Lock()
	defer p.mu.Unlock()
	auth := p.authState()
	if isAPIKeyMode(auth.Mode) {
		key, _, err := resolveCodexAPIKey(auth)
		return ProviderHealth{Authenticated: err == nil && key != "", CanRefresh: false}
	}
	authenticated := auth.AccessToken != ""
	if auth.ExpiresAt > 0 {
		authenticated = authenticated && auth.ExpiresAt > time.Now().Unix()
	}
	return ProviderHealth{Authenticated: authenticated, CanRefresh: auth.RefreshToken != ""}
}
