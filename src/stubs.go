package main

// stubs.go — thin aliases and wrappers so that src/*_test.go can compile
// without any business logic from the old flat src/ layout.
// All real implementations live in internal/; this file just re-exports them
// under the original names expected by the test suite.

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/x51vn/github-copilot-svcs/internal/auth"
	"github.com/x51vn/github-copilot-svcs/internal/config"
	"github.com/x51vn/github-copilot-svcs/internal/httputil"
	"github.com/x51vn/github-copilot-svcs/internal/provider"
	"github.com/x51vn/github-copilot-svcs/internal/proxy"
	"github.com/x51vn/github-copilot-svcs/internal/types"
)

// ── config types ──────────────────────────────────────────────────────────────
type (
	Config                = config.Config
	ProvidersConfig       = config.ProvidersConfig
	CopilotProviderConfig = config.CopilotProviderConfig
	CopilotAccount        = config.CopilotAccount
	CopilotAuthState      = config.CopilotAuthState
	CodexAccount          = config.CodexAccount
	CodexAuthState        = config.CodexAuthState
	ModelMapEntry         = config.ModelMapEntry
	RoutingConfig         = config.RoutingConfig
	ConfigMigrationMode   = config.ConfigMigrationMode
)

const (
	ConfigMigrationNone     = config.ConfigMigrationNone
	ConfigMigrationMerge    = config.ConfigMigrationMerge
	ConfigMigrationOverride = config.ConfigMigrationOverride
)

var configPathOverride string

func defaultConfig() *Config { return config.DefaultConfig() }
func loadConfig() (*Config, error) {
	*config.ConfigPathOverride = configPathOverride
	return config.LoadConfig()
}
func saveConfig(cfg *Config) error {
	*config.ConfigPathOverride = configPathOverride
	return config.SaveConfig(cfg)
}
func applyConfigDefaults(cfg *Config) { config.ApplyConfigDefaults(cfg) }
func mergeConfigs(e, d *Config, m ConfigMigrationMode) *Config {
	return config.MergeConfigs(e, d, m)
}
func migrateConfig(mode ConfigMigrationMode) error { return config.MigrateConfig(mode) }
func ensureCodexModelMap(cfg *Config, ids []string) {
	config.EnsureCodexModelMap(cfg, ids)
}
func normalizeCopilotAccounts(c *CopilotProviderConfig)    { config.NormalizeCopilotAccounts(c) }
func normalizeCodexAccounts(c *config.CodexProviderConfig) { config.NormalizeCodexAccounts(c) }
func setDefaultTimeouts(cfg *Config)                       { config.SetDefaultTimeouts(cfg) }
func loadDefaultConfigFromExample() (*Config, error)       { return config.LoadDefaultConfigFromExample() }

// ── provider/types types ──────────────────────────────────────────────────────
type (
	Model     = types.Model
	ModelList = types.ModelList
)

// ── provider types ────────────────────────────────────────────────────────────
type (
	ProviderID          = provider.ProviderID
	Capability          = provider.Capability
	ProviderHealth      = provider.ProviderHealth
	Provider            = provider.Provider
	ProviderRegistry    = provider.ProviderRegistry
	ModelCache          = provider.ModelCache
	ModelRouter         = proxy.ModelRouter
	CircuitBreaker      = httputil.CircuitBreaker
	CircuitBreakerState = httputil.CircuitBreakerState
	CoalescingCache     = httputil.CoalescingCache
	WorkerPool          = httputil.WorkerPool
	CopilotProvider     = provider.CopilotProvider
	CodexProvider       = provider.CodexProvider
	CopilotFreeCatalog  = provider.CopilotFreeCatalog
)

const (
	ProviderCopilot      = provider.ProviderCopilot
	ProviderCodex        = provider.ProviderCodex
	CapabilityChat       = provider.CapabilityChat
	CapabilityEmbeddings = provider.CapabilityEmbeddings
	CapabilityResponses  = provider.CapabilityResponses

	circuitBreakerFailureThreshold = httputil.CircuitBreakerFailureThreshold
	CircuitBreakerFailureThreshold = httputil.CircuitBreakerFailureThreshold
	CircuitClosed                  = httputil.CircuitClosed
	DefaultModelCacheTTL           = provider.DefaultModelCacheTTL
)

// Keep the old lowercase name for tests that reference it directly.
const defaultModelCacheTTL = DefaultModelCacheTTL

// ── httputil vars ─────────────────────────────────────────────────────────────
// sharedHTTPClient mirrors httputil.SharedHTTPClient; tests may reassign it.
var sharedHTTPClient *http.Client

func init() {
	if httputil.SharedHTTPClient == nil {
		httputil.SharedHTTPClient = &http.Client{}
	}
	sharedHTTPClient = httputil.SharedHTTPClient
}

// ── auth constants/errors ─────────────────────────────────────────────────────
var errRefreshUnrecoverable = auth.ErrRefreshUnrecoverable
var baseRetryDelay = 2
var codexAuthIssuer = "https://auth.openai.com"

const maxRefreshRetries = auth.MaxRefreshRetries

type officialTokenData = auth.OfficialTokenData

func isUnrecoverableRefreshError(body []byte) bool { return auth.IsUnrecoverableRefreshError(body) }

const (
	codexCredentialSourceEnv           = auth.CodexCredentialSourceEnv
	codexCredentialSourceOfficialStore = auth.CodexCredentialSourceOfficialStore
	codexCredentialSourceProxyConfig   = auth.CodexCredentialSourceProxyConfig
)

type resolvedCodexChatGPTAuth = auth.ResolvedCodexChatGPTAuth

// ── auth functions ────────────────────────────────────────────────────────────
func codexRefreshToken(a *CodexAuthState, save func() error) error {
	*auth.CodexAuthIssuer = codexAuthIssuer
	*auth.BaseRetryDelay = baseRetryDelay
	if sharedHTTPClient != nil {
		httputil.SharedHTTPClient = sharedHTTPClient
	}
	return auth.CodexRefreshToken(a, save)
}
func isChatGPTMode(mode string) bool { return auth.IsChatGPTMode(mode) }
func resolveCodexAPIKey(a *CodexAuthState) (string, string, error) {
	return auth.ResolveCodexAPIKey(a)
}
func resolveCodexChatGPTAuth(a *CodexAuthState) (*auth.ResolvedCodexChatGPTAuth, error) {
	return auth.ResolveCodexChatGPTAuth(a)
}
func extractAccountIDFromJWT(tok string) string       { return auth.ExtractAccountIDFromJWT(tok) }
func loadOfficialCodexModels() ([]types.Model, error) { return auth.LoadOfficialCodexModels() }

// ── provider functions ────────────────────────────────────────────────────────
func newProviderRegistry() *ProviderRegistry      { return provider.NewProviderRegistry() }
func NewProviderRegistry() *ProviderRegistry      { return provider.NewProviderRegistry() }
func newModelCache(ttl time.Duration) *ModelCache { return provider.NewModelCache(ttl) }
func NewModelCache(ttl time.Duration) *ModelCache { return provider.NewModelCache(ttl) }
func newCopilotProvider(cfg *Config) *CopilotProvider {
	return provider.NewCopilotProvider(cfg)
}
func NewCopilotProvider(cfg *Config) *CopilotProvider { return provider.NewCopilotProvider(cfg) }
func newCodexProvider(cfg *Config) *CodexProvider {
	return provider.NewCodexProvider(cfg)
}
func NewCodexProvider(cfg *Config) *CodexProvider { return provider.NewCodexProvider(cfg) }

func modelsHandler(registry *ProviderRegistry, cfg *Config) http.HandlerFunc {
	return provider.ModelsHandler(registry, cfg)
}
func filterAllowedModels(ml *ModelList, allowed []string) *ModelList {
	return provider.FilterAllowedModels(ml, allowed)
}
func isModelAllowed(model string, allowed []string) bool {
	return provider.IsModelAllowed(model, allowed)
}
func intersectNormalizedModelNames(left, right []string) []string {
	return provider.IntersectNormalizedModelNames(left, right)
}
func cloneModelList(ml *ModelList) *ModelList { return provider.CloneModelList(ml) }
func isAccountLimitSignal(resp *http.Response) (bool, string) {
	return provider.IsAccountLimitSignal(resp)
}

// ── transform/adapter functions ───────────────────────────────────────────────
func extractModelFromBody(body []byte) string         { return provider.ExtractModelFromBody(body) }
func patchBodyModel(body []byte, model string) []byte { return provider.PatchBodyModel(body, model) }
func buildChatCompletionsRequestFromResponses(body []byte) ([]byte, bool, error) {
	return provider.BuildChatCompletionsRequestFromResponses(body)
}
func buildChatGPTCodexRequest(chatBody []byte) ([]byte, bool, error) {
	return provider.BuildChatGPTCodexRequest(chatBody)
}
func buildChatGPTCodexRequestFromResponses(respBody []byte) ([]byte, bool, error) {
	return provider.BuildChatGPTCodexRequestFromResponses(respBody)
}
func buildPlatformResponsesRequest(chatBody []byte) ([]byte, bool, error) {
	return provider.BuildPlatformResponsesRequest(chatBody)
}
func normalizeContentParts(content json.RawMessage) json.RawMessage {
	return provider.NormalizeContentParts(content)
}
func responsesToChatCompletion(respBody []byte) ([]byte, error) {
	return provider.ResponsesToChatCompletion(respBody)
}
func chatCompletionToResponses(chatBody []byte) ([]byte, error) {
	return provider.ChatCompletionToResponses(chatBody)
}
func chatCompletionStreamToResponses(body []byte) []byte {
	return provider.ChatCompletionStreamToResponses(body)
}
func extractMessages(v json.RawMessage) (string, json.RawMessage, error) {
	return provider.ExtractMessages(v)
}
func streamOptionsIncludeUsage(raw map[string]json.RawMessage) bool {
	return provider.StreamOptionsIncludeUsage(raw)
}
func streamResponsesAsChat(w http.ResponseWriter, resp *http.Response, includeUsage bool) error {
	return provider.StreamResponsesAsChat(w, resp, includeUsage)
}
func isStreamingRequest(body []byte) bool { return provider.IsStreamingRequestBody(body) }
func convertToolsForResponses(raw json.RawMessage) json.RawMessage {
	return provider.ConvertToolsForResponses(raw)
}
func normalizeEmbeddingsRequestBody(body []byte) ([]byte, string) {
	return provider.NormalizeEmbeddingsRequestBody(body)
}
func ensureEmbeddingsResponseCompat(body []byte, model string) []byte {
	return provider.EnsureEmbeddingsResponseCompat(body, model)
}

// ── copilot_free_catalog helpers ──────────────────────────────────────────────
func parseCopilotPlanFreeModels(pageHTML string) ([]string, error) {
	return provider.ParseCopilotPlanFreeModels(pageHTML)
}
func parseZeroPremiumModels(pageHTML string) ([]string, error) {
	return provider.ParseZeroPremiumModels(pageHTML)
}
func resolveEffectiveCopilotFreeModels(eligible []string, upstream *ModelList) []types.Model {
	return provider.ResolveEffectiveCopilotFreeModels(eligible, upstream)
}
func cellHasIncludedMarker(raw string) bool { return provider.CellHasIncludedMarker(raw) }

// ── proxy functions ───────────────────────────────────────────────────────────
func processProxyRequest(
	registry *ProviderRegistry,
	router *ModelRouter,
	cfg *Config,
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
) error {
	return proxy.ProcessProxyRequest(registry, router, cfg, w, r, ctx)
}

func capabilityFromPath(path string) (Capability, error) {
	return proxy.CapabilityFromPath(path)
}

func healthHandler(w http.ResponseWriter, r *http.Request) { proxy.HealthHandler(w, r) }

// ── httputil ──────────────────────────────────────────────────────────────────
func newCircuitBreaker(timeout time.Duration) *CircuitBreaker {
	return httputil.NewCircuitBreaker(timeout)
}
func newCircuitBreakerWithState(state CircuitBreakerState, timeout time.Duration) *CircuitBreaker {
	return httputil.NewCircuitBreakerWithState(state, timeout)
}
func newCoalescingCache() *CoalescingCache { return httputil.NewCoalescingCache() }
func NewCoalescingCache() *CoalescingCache { return httputil.NewCoalescingCache() }

func NewWorkerPool(workers int) *WorkerPool { return httputil.NewWorkerPool(workers) }

func makeRequestWithRetry(client *http.Client, req *http.Request, body []byte) (*http.Response, error) {
	return httputil.MakeRequestWithRetry(client, req, body)
}

func ProviderPrefix(id ProviderID) string  { return provider.ProviderPrefix(id) }
func ProviderOwnedBy(id ProviderID) string { return provider.ProviderOwnedBy(id) }
func AddModelPrefix(id ProviderID, modelID string) string {
	return provider.AddModelPrefix(id, modelID)
}
func StripModelPrefix(modelID string) (string, ProviderID, bool) {
	return provider.StripModelPrefix(modelID)
}

func NewModelRouter(registry *ProviderRegistry, routing RoutingConfig) *ModelRouter {
	return proxy.NewModelRouter(registry, routing)
}

type officialCodexAuthJSON = auth.OfficialCodexAuthJSON
