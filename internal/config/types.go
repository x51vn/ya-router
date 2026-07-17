// Package config defines the persisted configuration schema.
package config

// SecretReference identifies a credential managed by a SecretStore. The raw
// value is intentionally not part of this structure.
type SecretReference struct {
	ID       string `json:"id"`
	Source   string `json:"source,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// Routing controls how the model router dispatches requests.
type Routing struct {
	DefaultModel          string                   `json:"default_model"`
	DefaultProvider       string                   `json:"default_provider"`
	ShowUnavailableModels bool                     `json:"show_unavailable_models"`
	ModelMap              map[string]ModelMapEntry `json:"model_map,omitempty"`
	// ClaudeAliases projects Claude Code-discoverable model IDs onto canonical
	// ya-router provider-prefixed models without changing the OpenAI catalog.
	ClaudeAliases map[string]string `json:"claude_aliases,omitempty"`
	// VirtualModels defines umbrella/virtual model IDs (for example
	// "router/auto") that resolve to exactly one active provider-prefixed
	// target selected deterministically before dispatch. This is
	// selection-before-dispatch, not cross-provider failover.
	VirtualModels map[string]VirtualModel `json:"virtual_models,omitempty"`
}

// ModelMapEntry explicitly maps a model name to a provider and optional upstream alias.
type ModelMapEntry struct {
	Provider      string `json:"provider"`
	UpstreamModel string `json:"upstream_model,omitempty"`
}

// VirtualModelStrategyPriority selects the first routable target in configured
// order. It is the only strategy supported in v1.
const VirtualModelStrategyPriority = "priority"

// VirtualModel is one umbrella model. In v1 it carries a single strategy and an
// ordered list of canonical provider-prefixed target model IDs.
type VirtualModel struct {
	Strategy string   `json:"strategy"`
	Targets  []string `json:"targets"`
}

// CopilotAuthState holds persisted Copilot authentication state. Direct secret
// fields remain readable for V1 compatibility; managed control-plane writes use
// the corresponding references once YA-TUI-07 installs a SecretStore.
type CopilotAuthState struct {
	Mode            string           `json:"mode"`
	GitHubToken     string           `json:"github_token,omitempty"`
	GitHubTokenRef  *SecretReference `json:"github_token_ref,omitempty"`
	CopilotToken    string           `json:"copilot_token,omitempty"`
	CopilotTokenRef *SecretReference `json:"copilot_token_ref,omitempty"`
	ExpiresAt       int64            `json:"expires_at,omitempty"`
	RefreshIn       int64            `json:"refresh_in,omitempty"`
}

// CopilotAccount is one entry in the Copilot account pool.
type CopilotAccount struct {
	ID            string           `json:"id,omitempty"`
	Label         string           `json:"label"`
	Auth          CopilotAuthState `json:"auth"`
	LastLimitedAt int64            `json:"last_limited_at,omitempty"`
}

// CopilotProvider holds config for the GitHub Copilot provider.
type CopilotProvider struct {
	Enabled                bool             `json:"enabled"`
	Auth                   CopilotAuthState `json:"auth"`
	Accounts               []CopilotAccount `json:"accounts,omitempty"`
	AccountCooldownSeconds int              `json:"account_cooldown_seconds,omitempty"`
	AllowedModels          []string         `json:"allowed_models"`
}

// CodexAuthState holds auth configuration and persisted token state.
type CodexAuthState struct {
	Mode            string           `json:"mode"`
	APIKey          string           `json:"api_key,omitempty"`
	APIKeyRef       *SecretReference `json:"api_key_ref,omitempty"`
	AccessToken     string           `json:"access_token,omitempty"`
	AccessTokenRef  *SecretReference `json:"access_token_ref,omitempty"`
	RefreshToken    string           `json:"refresh_token,omitempty"`
	RefreshTokenRef *SecretReference `json:"refresh_token_ref,omitempty"`
	ExpiresAt       int64            `json:"expires_at,omitempty"`
	AccountID       string           `json:"account_id,omitempty"`
}

// CodexAccount is one entry in the Codex account pool.
type CodexAccount struct {
	ID            string         `json:"id,omitempty"`
	Label         string         `json:"label"`
	Auth          CodexAuthState `json:"auth"`
	LastLimitedAt int64          `json:"last_limited_at,omitempty"`
}

// CodexProvider holds config for the OpenAI Codex provider.
type CodexProvider struct {
	Enabled                bool           `json:"enabled"`
	Auth                   CodexAuthState `json:"auth"`
	Accounts               []CodexAccount `json:"accounts,omitempty"`
	AccountCooldownSeconds int            `json:"account_cooldown_seconds,omitempty"`
	AllowedModels          []string       `json:"allowed_models"`
	ChatGPTBaseURL         string         `json:"chatgpt_base_url,omitempty"`
}

// KiloProvider holds settings for the OpenAI-compatible Kilo Gateway.
type KiloProvider struct {
	Enabled           bool             `json:"enabled"`
	AllowAnonymous    bool             `json:"allow_anonymous"`
	APIKey            string           `json:"api_key,omitempty"`
	APIKeyRef         *SecretReference `json:"api_key_ref,omitempty"`
	OrganizationID    string           `json:"organization_id,omitempty"`
	OrganizationIDRef *SecretReference `json:"organization_id_ref,omitempty"`
	BaseURL           string           `json:"base_url,omitempty"`
	AllowedModels     []string         `json:"allowed_models"`
}

// Providers groups all provider configurations.
type Providers struct {
	Copilot CopilotProvider `json:"copilot"`
	Codex   CodexProvider   `json:"codex"`
	Kilo    KiloProvider    `json:"kilo"`
}

// Timeouts holds all timeout values in seconds.
type Timeouts struct {
	HTTPClient      int `json:"http_client"`
	ServerRead      int `json:"server_read"`
	ServerWrite     int `json:"server_write"`
	ServerIdle      int `json:"server_idle"`
	ProxyContext    int `json:"proxy_context"`
	CircuitBreaker  int `json:"circuit_breaker"`
	KeepAlive       int `json:"keep_alive"`
	TLSHandshake    int `json:"tls_handshake"`
	DialTimeout     int `json:"dial_timeout"`
	IdleConnTimeout int `json:"idle_conn_timeout"`
}

// Logging controls the shared application log outputs and local retention.
type Logging struct {
	FilePath       string `json:"file_path"`
	MaxFileSizeMiB int    `json:"max_file_size_mib"`
	RetainedFiles  int    `json:"retained_files"`
}

// Config is the top-level application configuration (V1 schema).
type Config struct {
	Port          int       `json:"port"`
	ConfigVersion int       `json:"config_version"`
	EnablePprof   bool      `json:"enable_pprof"`
	Logging       Logging   `json:"logging"`
	Routing       Routing   `json:"routing"`
	Providers     Providers `json:"providers"`
	Timeouts      Timeouts  `json:"timeouts"`
}
