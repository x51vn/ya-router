// config.go — application configuration with V0→V1 migration.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
)

const currentConfigVersion = 1

// RoutingConfig controls how the model router dispatches requests.
type RoutingConfig struct {
	DefaultModel          string                   `json:"default_model"`
	DefaultProvider       string                   `json:"default_provider"`
	ShowUnavailableModels bool                     `json:"show_unavailable_models"`
	ModelMap              map[string]ModelMapEntry `json:"model_map,omitempty"`
}

// ModelMapEntry explicitly maps a model name to a provider and optional upstream alias.
type ModelMapEntry struct {
	Provider      string `json:"provider"`
	UpstreamModel string `json:"upstream_model,omitempty"`
}

// CopilotAuthState holds persisted Copilot authentication state.
type CopilotAuthState struct {
	Mode         string `json:"mode"`
	GitHubToken  string `json:"github_token,omitempty"`
	CopilotToken string `json:"copilot_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	RefreshIn    int64  `json:"refresh_in,omitempty"`
}

// CopilotAccount is one entry in the Copilot account pool.
// Label uniquely identifies the account for CLI operations.
// LastLimitedAt is the Unix timestamp when this account was last rate-limited;
// used to enforce the per-account cooldown window.
type CopilotAccount struct {
	Label         string           `json:"label"`
	Auth          CopilotAuthState `json:"auth"`
	LastLimitedAt int64            `json:"last_limited_at,omitempty"`
}

// CopilotProviderConfig holds config for the GitHub Copilot provider.
//
// Accounts is the multi-account pool. If Accounts is empty but Auth is
// non-zero, the service promotes Auth to Accounts[0] (label "primary")
// automatically for backward compatibility.
//
// AccountCooldownSeconds is how long (in seconds) a rate-limited account
// is skipped before being considered healthy again. Default 300.
type CopilotProviderConfig struct {
	Enabled                bool             `json:"enabled"`
	Auth                   CopilotAuthState `json:"auth"`
	Accounts               []CopilotAccount `json:"accounts,omitempty"`
	AccountCooldownSeconds int              `json:"account_cooldown_seconds,omitempty"`
	AllowedModels          []string         `json:"allowed_models"`
}

// CodexAuthState holds auth configuration and persisted token state
// for the OpenAI Codex provider.
//
// Mode selects the transport and credential source:
//   - "chatgpt" / "device_code" / "chatgpt_device_auth": ChatGPT backend.
//     Reads credentials from the official Codex auth store (~/.codex/auth.json).
//   - "api_key": OpenAI Platform API with a user-supplied API key.
type CodexAuthState struct {
	Mode         string `json:"mode"`
	APIKey       string `json:"api_key,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
}

// CodexAccount is one entry in the Codex account pool.
// Label uniquely identifies the account for CLI operations.
// LastLimitedAt is the Unix timestamp when this account was last rate-limited;
// used to enforce the per-account cooldown window.
type CodexAccount struct {
	Label         string         `json:"label"`
	Auth          CodexAuthState `json:"auth"`
	LastLimitedAt int64          `json:"last_limited_at,omitempty"`
}

// CodexProviderConfig holds config for the OpenAI Codex provider.
//
// Accounts is the multi-account pool. If Accounts is empty but Auth.Mode is
// non-empty, the service promotes Auth to Accounts[0] (label "primary")
// automatically for backward compatibility.
//
// AccountCooldownSeconds is how long (in seconds) a rate-limited account
// is skipped before being considered healthy again. Default 300.
type CodexProviderConfig struct {
	Enabled                bool           `json:"enabled"`
	Auth                   CodexAuthState `json:"auth"`
	Accounts               []CodexAccount `json:"accounts,omitempty"`
	AccountCooldownSeconds int            `json:"account_cooldown_seconds,omitempty"`
	AllowedModels          []string       `json:"allowed_models"`
	ChatGPTBaseURL         string         `json:"chatgpt_base_url,omitempty"`
}

// ProvidersConfig groups all provider configurations.
type ProvidersConfig struct {
	Copilot CopilotProviderConfig `json:"copilot"`
	Codex   CodexProviderConfig   `json:"codex"`
}

// TimeoutsConfig holds all timeout values (seconds).
type TimeoutsConfig struct {
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

// Config is the top-level application configuration (V1 schema).
type Config struct {
	Port          int             `json:"port"`
	ConfigVersion int             `json:"config_version"`
	EnablePprof   bool            `json:"enable_pprof"`
	Routing       RoutingConfig   `json:"routing"`
	Providers     ProvidersConfig `json:"providers"`
	Timeouts      TimeoutsConfig  `json:"timeouts"`
}

// legacyV0Config is used only for reading and migrating pre-V1 config files.
type legacyV0Config struct {
	Port          int      `json:"port"`
	ConfigVersion int      `json:"config_version"`
	GitHubToken   string   `json:"github_token"`
	CopilotToken  string   `json:"copilot_token"`
	ExpiresAt     int64    `json:"expires_at"`
	RefreshIn     int64    `json:"refresh_in"`
	AllowedModels []string `json:"allowed_models"`
	DefaultModel  string   `json:"default_model"`
	EnablePprof   bool     `json:"enable_pprof"`
	Timeouts      struct {
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
	} `json:"timeouts"`
}

// ConfigMigrationMode controls startup migration behaviour.
type ConfigMigrationMode string

const (
	ConfigMigrationNone     ConfigMigrationMode = "none"
	ConfigMigrationMerge    ConfigMigrationMode = "merge"
	ConfigMigrationOverride ConfigMigrationMode = "override"
)

const (
	configDirName  = ".local/share/github-copilot-svcs"
	configFileName = "config.json"
)

// configPathOverride lets tests redirect config I/O to a temp path.
var configPathOverride string

var ConfigPathOverride = &configPathOverride

func GetConfigPath() (string, error) {
	if configPathOverride != "" {
		return configPathOverride, nil
	}
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(usr.HomeDir, configDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

// loadConfig reads config from disk, auto-migrating V0 files to V1.
func LoadConfig() (*Config, error) {
	path, err := GetConfigPath()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return defaultConfig(), nil
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}

	var probe struct {
		Version int `json:"config_version"`
	}
	_ = json.Unmarshal(raw, &probe)

	if probe.Version >= 1 {
		var cfg Config
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
		applyConfigDefaults(&cfg)
		return &cfg, nil
	}

	// V0: parse legacy flat format and migrate to V1.
	var v0 legacyV0Config
	if err := json.Unmarshal(raw, &v0); err != nil {
		return nil, err
	}
	return migrateV0ToV1(&v0), nil
}

// migrateV0ToV1 converts the legacy flat config into the V1 schema.
func migrateV0ToV1(v0 *legacyV0Config) *Config {
	cfg := defaultConfig()
	if v0.Port != 0 {
		cfg.Port = v0.Port
	}
	cfg.EnablePprof = v0.EnablePprof

	cfg.Providers.Copilot.Enabled = true
	cfg.Providers.Copilot.Auth.Mode = "device_code"
	cfg.Providers.Copilot.Auth.GitHubToken = v0.GitHubToken
	cfg.Providers.Copilot.Auth.CopilotToken = v0.CopilotToken
	cfg.Providers.Copilot.Auth.ExpiresAt = v0.ExpiresAt
	cfg.Providers.Copilot.Auth.RefreshIn = v0.RefreshIn
	if v0.AllowedModels != nil {
		cfg.Providers.Copilot.AllowedModels = v0.AllowedModels
	}

	if v0.DefaultModel != "" {
		cfg.Routing.DefaultModel = v0.DefaultModel
	}
	cfg.Routing.DefaultProvider = "copilot"

	t := &v0.Timeouts
	setLegacyTimeouts(&cfg.Timeouts, t.HTTPClient, t.ServerRead, t.ServerWrite,
		t.ServerIdle, t.ProxyContext, t.CircuitBreaker, t.KeepAlive,
		t.TLSHandshake, t.DialTimeout, t.IdleConnTimeout)
	normalizeCopilotAccounts(&cfg.Providers.Copilot)
	normalizeCodexAccounts(&cfg.Providers.Codex)
	return cfg
}

func setLegacyTimeouts(dst *TimeoutsConfig, httpClient, serverRead, serverWrite,
	serverIdle, proxyContext, circuitBreaker, keepAlive, tlsHandshake,
	dialTimeout, idleConnTimeout int) {
	if httpClient != 0 {
		dst.HTTPClient = httpClient
	}
	if serverRead != 0 {
		dst.ServerRead = serverRead
	}
	if serverWrite != 0 {
		dst.ServerWrite = serverWrite
	}
	if serverIdle != 0 {
		dst.ServerIdle = serverIdle
	}
	if proxyContext != 0 {
		dst.ProxyContext = proxyContext
	}
	if circuitBreaker != 0 {
		dst.CircuitBreaker = circuitBreaker
	}
	if keepAlive != 0 {
		dst.KeepAlive = keepAlive
	}
	if tlsHandshake != 0 {
		dst.TLSHandshake = tlsHandshake
	}
	if dialTimeout != 0 {
		dst.DialTimeout = dialTimeout
	}
	if idleConnTimeout != 0 {
		dst.IdleConnTimeout = idleConnTimeout
	}
}

// defaultConfig returns a fresh V1 config with all defaults applied.
func defaultConfig() *Config {
	cfg := &Config{
		Port:          7071,
		ConfigVersion: currentConfigVersion,
	}
	cfg.Routing.DefaultModel = "gpt-5-mini"
	cfg.Routing.DefaultProvider = "copilot"
	cfg.Providers.Copilot.Enabled = true
	cfg.Providers.Copilot.Auth.Mode = "device_code"
	// allowed_models left empty = allow all models from upstream
	cfg.Providers.Codex.Enabled = false
	cfg.Providers.Codex.Auth.Mode = "device_code"
	setDefaultTimeouts(cfg)
	return cfg
}

// applyConfigDefaults fills in zero-valued fields on a loaded V1 config.
func applyConfigDefaults(cfg *Config) {
	if cfg.Port == 0 {
		cfg.Port = 7071
	}
	if cfg.Routing.DefaultModel == "" {
		cfg.Routing.DefaultModel = "gpt-5-mini"
	}
	if cfg.Routing.DefaultProvider == "" {
		cfg.Routing.DefaultProvider = "copilot"
	}
	// allowed_models: empty = allow all models from upstream
	if cfg.Providers.Codex.Auth.Mode == "" {
		cfg.Providers.Codex.Auth.Mode = "device_code"
	}
	if cfg.Providers.Copilot.AccountCooldownSeconds == 0 {
		cfg.Providers.Copilot.AccountCooldownSeconds = 300
	}
	normalizeCopilotAccounts(&cfg.Providers.Copilot)
	if cfg.Providers.Codex.AccountCooldownSeconds == 0 {
		cfg.Providers.Codex.AccountCooldownSeconds = 300
	}
	normalizeCodexAccounts(&cfg.Providers.Codex)
	setDefaultTimeouts(cfg)
}

// normalizeCopilotAccounts promotes the legacy top-level Auth field into the
// Accounts pool when Accounts is empty and Auth has a GitHub token.
// This ensures single-account configs remain valid without operator changes.
func normalizeCopilotAccounts(c *CopilotProviderConfig) {
	if len(c.Accounts) == 0 && c.Auth.GitHubToken != "" {
		c.Accounts = []CopilotAccount{{Label: "primary", Auth: c.Auth}}
		c.Auth = CopilotAuthState{}
	}
}

func normalizeCodexAccounts(c *CodexProviderConfig) {
	if len(c.Accounts) == 0 && (c.Auth.AccessToken != "" || c.Auth.APIKey != "") {
		c.Accounts = []CodexAccount{{Label: "primary", Auth: c.Auth}}
		c.Auth = CodexAuthState{}
	}
}

// setDefaultTimeouts fills zero-valued timeout fields with sensible defaults.
func setDefaultTimeouts(cfg *Config) {
	t := &cfg.Timeouts
	if t.HTTPClient == 0 {
		t.HTTPClient = 300
	}
	if t.ServerRead == 0 {
		t.ServerRead = 30
	}
	if t.ServerWrite == 0 {
		t.ServerWrite = 300
	}
	if t.ServerIdle == 0 {
		t.ServerIdle = 120
	}
	if t.ProxyContext == 0 {
		t.ProxyContext = 300
	}
	if t.CircuitBreaker == 0 {
		t.CircuitBreaker = 30
	}
	if t.KeepAlive == 0 {
		t.KeepAlive = 30
	}
	if t.TLSHandshake == 0 {
		t.TLSHandshake = 10
	}
	if t.DialTimeout == 0 {
		t.DialTimeout = 10
	}
	if t.IdleConnTimeout == 0 {
		t.IdleConnTimeout = 90
	}
}

// saveConfig writes cfg to disk atomically with 0600 permissions.
func SaveConfig(cfg *Config) error {
	path, err := GetConfigPath()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename temp config: %w", err)
	}
	return nil
}

// loadDefaultConfigFromExample loads a default config from config.example.json if it exists.
func loadDefaultConfigFromExample() (*Config, error) {
	examplePath := "config.example.json"
	if _, err := os.Stat(examplePath); err != nil {
		return defaultConfig(), nil
	}
	file, err := os.Open(examplePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open config.example.json: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	var probe struct {
		Version int `json:"config_version"`
	}
	_ = json.Unmarshal(raw, &probe)
	if probe.Version >= 1 {
		var cfg Config
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config.example.json: %w", err)
		}
		applyConfigDefaults(&cfg)
		return &cfg, nil
	}
	var v0 legacyV0Config
	if err := json.Unmarshal(raw, &v0); err != nil {
		return nil, fmt.Errorf("failed to parse config.example.json: %w", err)
	}
	return migrateV0ToV1(&v0), nil
}

// mergeConfigs merges defaults into existing config per the given mode.
func mergeConfigs(existing *Config, defaults *Config, mode ConfigMigrationMode) *Config {
	switch mode {
	case ConfigMigrationOverride:
		return defaults
	case ConfigMigrationMerge:
		merged := *existing
		if merged.Port == 0 {
			merged.Port = defaults.Port
		}
		if merged.Routing.DefaultModel == "" {
			merged.Routing.DefaultModel = defaults.Routing.DefaultModel
		}
		if merged.Routing.DefaultProvider == "" {
			merged.Routing.DefaultProvider = defaults.Routing.DefaultProvider
		}
		if merged.Providers.Copilot.AllowedModels == nil {
			merged.Providers.Copilot.AllowedModels = defaults.Providers.Copilot.AllowedModels
		}
		mergeTimeouts(&merged.Timeouts, &defaults.Timeouts)
		return &merged
	default:
		return existing
	}
}

func mergeTimeouts(dst *TimeoutsConfig, src *TimeoutsConfig) {
	if dst.HTTPClient == 0 {
		dst.HTTPClient = src.HTTPClient
	}
	if dst.ServerRead == 0 {
		dst.ServerRead = src.ServerRead
	}
	if dst.ServerWrite == 0 {
		dst.ServerWrite = src.ServerWrite
	}
	if dst.ServerIdle == 0 {
		dst.ServerIdle = src.ServerIdle
	}
	if dst.ProxyContext == 0 {
		dst.ProxyContext = src.ProxyContext
	}
	if dst.CircuitBreaker == 0 {
		dst.CircuitBreaker = src.CircuitBreaker
	}
	if dst.KeepAlive == 0 {
		dst.KeepAlive = src.KeepAlive
	}
	if dst.TLSHandshake == 0 {
		dst.TLSHandshake = src.TLSHandshake
	}
	if dst.DialTimeout == 0 {
		dst.DialTimeout = src.DialTimeout
	}
	if dst.IdleConnTimeout == 0 {
		dst.IdleConnTimeout = src.IdleConnTimeout
	}
}

func EnsureCodexModelMap(cfg *Config, modelIDs []string) bool {
	if cfg.Routing.ModelMap == nil {
		cfg.Routing.ModelMap = make(map[string]ModelMapEntry)
	}
	added := false
	for _, id := range modelIDs {
		if _, exists := cfg.Routing.ModelMap[id]; !exists {
			cfg.Routing.ModelMap[id] = ModelMapEntry{Provider: "codex"}
			fmt.Printf("  model_map: added %q → codex\n", id)
			added = true
		}
	}
	return added
}

// migrateConfig upgrades the on-disk config according to mode.
func MigrateConfig(mode ConfigMigrationMode) error {
	if mode == ConfigMigrationNone {
		return nil
	}
	existingConfig, err := LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load existing config: %w", err)
	}
	defConfig, err := loadDefaultConfigFromExample()
	if err != nil {
		return fmt.Errorf("failed to load default config: %w", err)
	}
	path, err := GetConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	mergedConfig := mergeConfigs(existingConfig, defConfig, mode)
	if err := SaveConfig(mergedConfig); err != nil {
		return fmt.Errorf("failed to save migrated config: %w", err)
	}
	fmt.Printf("Config migration completed (mode: %s)\n", mode)
	return nil
}

// GetRuntimeStatePath returns the path to a runtime state file alongside the config file.
func GetRuntimeStatePath(fileName string) (string, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(configPath), fileName), nil
}

// Exported wrappers for test access.
func DefaultConfig() *Config          { return defaultConfig() }
func ApplyConfigDefaults(cfg *Config) { applyConfigDefaults(cfg) }
func MergeConfigs(e *Config, d *Config, m ConfigMigrationMode) *Config {
	return mergeConfigs(e, d, m)
}
func NormalizeCopilotAccounts(c *CopilotProviderConfig) { normalizeCopilotAccounts(c) }
func NormalizeCodexAccounts(c *CodexProviderConfig)     { normalizeCodexAccounts(c) }
func SetDefaultTimeouts(cfg *Config)                    { setDefaultTimeouts(cfg) }
func LoadDefaultConfigFromExample() (*Config, error)    { return loadDefaultConfigFromExample() }
