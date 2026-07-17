// kilo_provider.go — Kilo AI Gateway provider implementation.
package yarouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	secretpkg "github.com/duvu/ya-router/internal/secret"
)

const (
	defaultKiloGatewayBaseURL = "https://api.kilo.ai/api/gateway"
	kiloAPIKeyEnv             = "KILO_API_KEY"
	kiloOrganizationIDEnv     = "KILO_ORG_ID"
	kiloGatewayBaseURLEnv     = "KILO_GATEWAY_BASE_URL"
	kiloAutoFreeModel         = "kilo-auto/free"
)

// KiloProvider implements the OpenAI-compatible Kilo AI Gateway. Anonymous
// mode is deliberately limited to free model IDs; authenticated mode can use
// the full catalog allowed by the account and provider configuration.
type KiloProvider struct {
	cfg    *Config
	auth   secretpkg.AuthController
	cb     *CircuitBreaker
	cache  *ModelCache
	client *http.Client

	freeMu     sync.RWMutex
	freeModels map[string]struct{}
}

func NewKiloProvider(cfg *Config) *KiloProvider {
	return NewKiloProviderWithAuth(cfg, nil)
}

func NewKiloProviderWithAuth(cfg *Config, auth secretpkg.AuthController) *KiloProvider {
	client := sharedHTTPClient
	if client == nil {
		client = &http.Client{Timeout: time.Duration(cfg.Timeouts.HTTPClient) * time.Second}
	}
	return &KiloProvider{
		cfg:  cfg,
		auth: auth,
		cb: &CircuitBreaker{
			state:   CircuitClosed,
			timeout: time.Duration(cfg.Timeouts.CircuitBreaker) * time.Second,
		},
		cache:      NewModelCache(defaultModelCacheTTL),
		client:     client,
		freeModels: map[string]struct{}{kiloAutoFreeModel: {}},
	}
}

func (p *KiloProvider) ID() ProviderID { return ProviderKilo }
func (p *KiloProvider) Name() string   { return "Kilo Gateway" }

// Kilo supports OpenAI Chat Completions and native Responses passthrough.
// Embeddings are not advertised because they are not part of the documented
// public Gateway API contract.
func (p *KiloProvider) Capabilities() []Capability {
	return []Capability{CapabilityChat, CapabilityResponses}
}

func (p *KiloProvider) apiKey() (string, string) {
	if p.auth != nil {
		if credential, ok := p.auth.ResolveCredential("kilo/api_key"); ok {
			return credential.Value, string(credential.Source)
		}
	}
	if key := strings.TrimSpace(os.Getenv(kiloAPIKeyEnv)); key != "" {
		return key, kiloAPIKeyEnv
	}
	if key := strings.TrimSpace(p.cfg.Providers.Kilo.APIKey); key != "" {
		return key, "config"
	}
	return "", "anonymous"
}

func (p *KiloProvider) organizationID() string {
	if p.auth != nil {
		if credential, ok := p.auth.ResolveCredential("kilo/organization_id"); ok {
			return credential.Value
		}
	}
	if organizationID := strings.TrimSpace(os.Getenv(kiloOrganizationIDEnv)); organizationID != "" {
		return organizationID
	}
	return strings.TrimSpace(p.cfg.Providers.Kilo.OrganizationID)
}

func (p *KiloProvider) baseURL() (string, error) {
	raw := strings.TrimSpace(os.Getenv(kiloGatewayBaseURLEnv))
	if raw == "" {
		raw = strings.TrimSpace(p.cfg.Providers.Kilo.BaseURL)
	}
	if raw == "" {
		raw = defaultKiloGatewayBaseURL
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("invalid Kilo Gateway base URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("Kilo Gateway base URL must not contain credentials, query, or fragment")
	}
	if parsed.Scheme == "http" && !isLoopbackHostname(parsed.Hostname()) {
		return "", fmt.Errorf("Kilo Gateway base URL must use HTTPS unless it is loopback")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

func isLoopbackHostname(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func (p *KiloProvider) EnsureAuthenticated(_ context.Context) error {
	if _, err := p.baseURL(); err != nil {
		return newProviderError(ProviderKilo, ProviderErrorInvalidRequest, http.StatusInternalServerError, false, "%v", err)
	}
	key, _ := p.apiKey()
	if key != "" || p.cfg.Providers.Kilo.AllowAnonymous {
		return nil
	}
	return newProviderError(ProviderKilo, ProviderErrorAuthRequired, http.StatusUnauthorized, false,
		"no Kilo API key configured and anonymous access is disabled")
}

func (p *KiloProvider) ListModels(ctx context.Context) (*ModelList, error) {
	if err := p.EnsureAuthenticated(ctx); err != nil {
		return nil, err
	}
	return p.cache.GetOrFetch(func() (*ModelList, error) { return p.fetchModels(ctx) })
}

type kiloModelResponse struct {
	Data []kiloModel `json:"data"`
}

type kiloModel struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Created   int64  `json:"created"`
	OwnedBy   string `json:"owned_by"`
	Name      string `json:"name"`
	IsFree    bool   `json:"isFree"`
	IsFreeAlt bool   `json:"is_free"`
	Pricing   struct {
		Prompt     string `json:"prompt"`
		Completion string `json:"completion"`
	} `json:"pricing"`
}

func (p *KiloProvider) fetchModels(ctx context.Context) (*ModelList, error) {
	baseURL, err := p.baseURL()
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	p.setHeaders(request, nil)

	response, err := p.client.Do(request)
	if err != nil {
		return nil, newProviderError(ProviderKilo, ProviderErrorTransport, http.StatusBadGateway, true,
			"Kilo model discovery failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, newProviderError(ProviderKilo, kiloErrorKind(response.StatusCode), response.StatusCode,
			response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500,
			"Kilo model discovery returned HTTP %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 16*1024*1024))
	if err != nil {
		return nil, newProviderError(ProviderKilo, ProviderErrorTransport, http.StatusBadGateway, true,
			"read Kilo model catalog: %v", err)
	}
	var payload kiloModelResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, newProviderError(ProviderKilo, ProviderErrorTransport, http.StatusBadGateway, false,
			"decode Kilo model catalog: %v", err)
	}

	key, _ := p.apiKey()
	models := make([]Model, 0, len(payload.Data)+1)
	seen := make(map[string]bool, len(payload.Data)+1)
	discoveredFree := map[string]struct{}{kiloAutoFreeModel: {}}
	for _, item := range payload.Data {
		item.ID = strings.TrimSpace(item.ID)
		free := isFreeKiloCatalogModel(item)
		if item.ID == "" || (key == "" && !free) {
			continue
		}
		if free {
			discoveredFree[strings.ToLower(item.ID)] = struct{}{}
		}
		object := item.Object
		if object == "" {
			object = "model"
		}
		created := item.Created
		if created == 0 {
			created = time.Now().Unix()
		}
		models = append(models, Model{
			ID: item.ID, Object: object, Created: created, OwnedBy: "kilo", Name: item.Name,
		})
		seen[item.ID] = true
	}
	if key == "" && p.cfg.Providers.Kilo.AllowAnonymous && !seen[kiloAutoFreeModel] {
		models = append(models, Model{
			ID: kiloAutoFreeModel, Object: "model", Created: time.Now().Unix(), OwnedBy: "kilo", Name: "Kilo Auto Free",
		})
	}
	p.replaceFreeModels(discoveredFree)

	modelList := &ModelList{Object: "list", Data: models}
	if key == "" {
		// Unlike authenticated providers, anonymous discovery must never
		// synthesize allowlisted entries that the upstream did not identify as
		// free. That would advertise a paid model that ProxyRequest rejects.
		return filterDiscoveredModels(modelList, p.cfg.Providers.Kilo.AllowedModels), nil
	}
	return filterAllowedModels(modelList, p.cfg.Providers.Kilo.AllowedModels), nil
}

func filterDiscoveredModels(modelList *ModelList, allowedModels []string) *ModelList {
	if len(allowedModels) == 0 {
		return modelList
	}
	filtered := make([]Model, 0, len(modelList.Data))
	for _, model := range modelList.Data {
		if isModelAllowedWithPrefix(model.ID, allowedModels) {
			filtered = append(filtered, model)
		}
	}
	return &ModelList{Object: "list", Data: filtered}
}

func isFreeKiloCatalogModel(model kiloModel) bool {
	if model.IsFree || model.IsFreeAlt || isAnonymousKiloModel(model.ID) {
		return true
	}
	prompt, promptErr := strconv.ParseFloat(model.Pricing.Prompt, 64)
	completion, completionErr := strconv.ParseFloat(model.Pricing.Completion, 64)
	return promptErr == nil && completionErr == nil && prompt == 0 && completion == 0
}

func isAnonymousKiloModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == kiloAutoFreeModel || model == "openrouter/free" || strings.HasSuffix(model, ":free")
}

func (p *KiloProvider) replaceFreeModels(models map[string]struct{}) {
	p.freeMu.Lock()
	p.freeModels = models
	p.freeMu.Unlock()
}

func (p *KiloProvider) allowsAnonymousModel(model string) bool {
	if isAnonymousKiloModel(model) {
		return true
	}
	p.freeMu.RLock()
	_, ok := p.freeModels[strings.ToLower(strings.TrimSpace(model))]
	p.freeMu.RUnlock()
	return ok
}

func kiloErrorKind(status int) ProviderErrorKind {
	switch status {
	case http.StatusBadRequest:
		return ProviderErrorInvalidRequest
	case http.StatusUnauthorized:
		return ProviderErrorAuthRequired
	case http.StatusPaymentRequired, http.StatusForbidden:
		return ProviderErrorEntitlement
	case http.StatusTooManyRequests:
		return ProviderErrorRateLimit
	default:
		if status >= 500 {
			return ProviderErrorUnavailable
		}
		return ProviderErrorTransport
	}
}

func (p *KiloProvider) ProxyRequest(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	capability Capability,
) error {
	if err := p.EnsureAuthenticated(ctx); err != nil {
		return err
	}
	if capability != CapabilityChat && capability != CapabilityResponses {
		return newProviderError(ProviderKilo, ProviderErrorUnsupported, http.StatusBadRequest, false,
			"unsupported Kilo capability %q", capability)
	}
	model := extractModelFromBody(body)
	if model == "" {
		return newProviderError(ProviderKilo, ProviderErrorInvalidRequest, http.StatusBadRequest, false,
			"Kilo request requires a model")
	}
	key, _ := p.apiKey()
	if key == "" && !p.allowsAnonymousModel(model) {
		return newProviderError(ProviderKilo, ProviderErrorAuthRequired, http.StatusUnauthorized, false,
			"Kilo model %q requires KILO_API_KEY; anonymous mode is limited to free model IDs", model)
	}
	if !p.cb.canExecute() {
		return newProviderError(ProviderKilo, ProviderErrorUnavailable, http.StatusServiceUnavailable, true,
			"Kilo circuit breaker is open")
	}

	baseURL, err := p.baseURL()
	if err != nil {
		return err
	}
	path := "/chat/completions"
	if capability == CapabilityResponses {
		path = "/responses"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	p.setHeaders(request, r)

	response, err := makeRequestWithRetry(p.client, request, body)
	if err != nil {
		p.cb.onFailure()
		return newProviderError(ProviderKilo, ProviderErrorTransport, http.StatusBadGateway, true,
			"Kilo upstream request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 500 {
		p.cb.onFailure()
	} else {
		p.cb.onSuccess()
	}
	return streamResponse(w, response)
}

func (p *KiloProvider) setHeaders(request *http.Request, clientRequest *http.Request) {
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "ya-router/"+version)
	request.Header.Set("HTTP-Referer", "https://github.com/duvu/ya-router")
	request.Header.Set("X-Title", "ya-router")
	request.Header.Set("X-KiloCode-EditorName", "ya-router "+version)
	if key, _ := p.apiKey(); key != "" {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	if organizationID := p.organizationID(); organizationID != "" {
		request.Header.Set("X-KiloCode-OrganizationId", organizationID)
	}
	if clientRequest == nil {
		return
	}
	for _, header := range []string{
		"Idempotency-Key",
		"X-KiloCode-Mode",
		"X-KiloCode-TaskId",
		"X-KiloCode-Parent-TaskId",
	} {
		if value := strings.TrimSpace(clientRequest.Header.Get(header)); value != "" {
			request.Header.Set(header, value)
		}
	}
	// Organization policy is server-owned and cannot be overridden by callers.
	if organizationID := p.organizationID(); organizationID != "" {
		request.Header.Set("X-KiloCode-OrganizationId", organizationID)
		if projectID := strings.TrimSpace(clientRequest.Header.Get("X-KiloCode-ProjectId")); projectID != "" {
			request.Header.Set("X-KiloCode-ProjectId", projectID)
		}
	}
}

func (p *KiloProvider) InvalidateModelCache() {
	p.cache.Invalidate()
	p.replaceFreeModels(map[string]struct{}{kiloAutoFreeModel: {}})
}

func (p *KiloProvider) Health(_ context.Context) ProviderHealth {
	_, baseErr := p.baseURL()
	key, _ := p.apiKey()
	ready := baseErr == nil && (key != "" || p.cfg.Providers.Kilo.AllowAnonymous)
	health := ProviderHealth{Authenticated: ready, CanRefresh: false}
	if baseErr != nil {
		health.LastError = baseErr.Error()
	}
	return health
}
