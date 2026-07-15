// config.go — application configuration with V0→V1 migration.
package yarouter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	configschema "github.com/duvu/ya-router/internal/config"
)

const currentConfigVersion = 1

type RoutingConfig = configschema.Routing
type ModelMapEntry = configschema.ModelMapEntry
type CopilotAuthState = configschema.CopilotAuthState
type CopilotAccount = configschema.CopilotAccount
type CopilotProviderConfig = configschema.CopilotProvider
type CodexAuthState = configschema.CodexAuthState
type CodexAccount = configschema.CodexAccount
type CodexProviderConfig = configschema.CodexProvider
type KiloProviderConfig = configschema.KiloProvider
type ProvidersConfig = configschema.Providers
type TimeoutsConfig = configschema.Timeouts
type Config = configschema.Config

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
	configPathEnv  = "YA_ROUTER_CONFIG_PATH"
	configDirEnv   = "YA_ROUTER_CONFIG_DIR"
)

// configPathOverride lets tests redirect config I/O to a temp path.
var configPathOverride string

// configWriteMu serializes all in-process config writes. YA-TUI-03 will add the
// cross-process daemon lock and revisions; this guard prevents provider refresh
// callbacks in the current process from sharing or replacing the same temp file.
var configWriteMu sync.Mutex

func getConfigPath() (string, error) {
	if configPathOverride != "" {
		return configPathOverride, nil
	}
	if customPath := strings.TrimSpace(os.Getenv(configPathEnv)); customPath != "" {
		if err := os.MkdirAll(filepath.Dir(customPath), 0o700); err != nil {
			return "", err
		}
		return customPath, nil
	}
	if customDir := strings.TrimSpace(os.Getenv(configDirEnv)); customDir != "" {
		dir := filepath.Join(customDir, configFileName)
		if err := os.MkdirAll(customDir, 0o700); err != nil {
			return "", err
		}
		return dir, nil
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
func loadConfig() (*Config, error) {
	path, err := getConfigPath()
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultConfig(), nil
		}
		return nil, fmt.Errorf("open config: %w", err)
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
	cfg.Routing.DefaultProvider = string(ProviderCopilot)

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
	cfg.Routing.DefaultProvider = string(ProviderCopilot)
	cfg.Providers.Copilot.Enabled = true
	cfg.Providers.Copilot.Auth.Mode = "device_code"
	// allowed_models left empty = allow all models from upstream
	cfg.Providers.Codex.Enabled = false
	cfg.Providers.Codex.Auth.Mode = "device_code"
	cfg.Providers.Kilo.Enabled = false
	cfg.Providers.Kilo.AllowAnonymous = true
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
		cfg.Routing.DefaultProvider = string(ProviderCopilot)
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
		c.Accounts = []CopilotAccount{{ID: stableAccountID("copilot", "primary"), Label: "primary", Auth: c.Auth}}
		c.Auth = CopilotAuthState{}
	}
	ensureCopilotAccountIDs(c.Accounts)
}

func normalizeCodexAccounts(c *CodexProviderConfig) {
	if len(c.Accounts) == 0 && (c.Auth.AccessToken != "" || c.Auth.APIKey != "") {
		c.Accounts = []CodexAccount{{ID: stableAccountID("codex", "primary"), Label: "primary", Auth: c.Auth}}
		c.Auth = CodexAuthState{}
	}
	ensureCodexAccountIDs(c.Accounts)
}

func ensureCopilotAccountIDs(accounts []CopilotAccount) {
	seen := make(map[string]int, len(accounts))
	for index := range accounts {
		if accounts[index].ID != "" {
			continue
		}
		label := strings.TrimSpace(accounts[index].Label)
		occurrence := seen[label]
		seen[label]++
		accounts[index].ID = stableAccountID("copilot", fmt.Sprintf("%s\x00%d", label, occurrence))
	}
}

func ensureCodexAccountIDs(accounts []CodexAccount) {
	seen := make(map[string]int, len(accounts))
	for index := range accounts {
		if accounts[index].ID != "" {
			continue
		}
		label := strings.TrimSpace(accounts[index].Label)
		occurrence := seen[label]
		seen[label]++
		accounts[index].ID = stableAccountID("codex", fmt.Sprintf("%s\x00%d", label, occurrence))
	}
}

func stableAccountID(provider, identity string) string {
	digest := sha256.Sum256([]byte(provider + "\x00" + identity))
	return "acct_" + hex.EncodeToString(digest[:12])
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
func saveConfig(cfg *Config) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	return saveConfigLocked(cfg)
}

func saveConfigLocked(cfg *Config) error {
	path, err := getConfigPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	keepTemp := true
	defer func() {
		_ = f.Close()
		if keepTemp {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename temp config: %w", err)
	}
	keepTemp = false
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open config directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync config directory: %w", err)
	}
	return nil
}

// persistCopilotRuntimeAccount merges only runtime-owned authentication and
// cooldown state into the latest config. Provider instances hold immutable
// runtime config snapshots, so writing their whole snapshot would overwrite
// unrelated configuration or credentials refreshed by another provider.
func persistCopilotRuntimeAccount(source *Config, accountIndex int) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	latest, err := loadConfig()
	if err != nil {
		return err
	}
	if len(source.Providers.Copilot.Accounts) == 0 {
		latest.Providers.Copilot.Auth = source.Providers.Copilot.Auth
		return saveConfigLocked(latest)
	}
	if accountIndex < 0 || accountIndex >= len(source.Providers.Copilot.Accounts) {
		return fmt.Errorf("persist Copilot account: invalid account index %d", accountIndex)
	}
	sourceAccount := source.Providers.Copilot.Accounts[accountIndex]
	for index := range latest.Providers.Copilot.Accounts {
		if sameAccount(sourceAccount.ID, sourceAccount.Label, latest.Providers.Copilot.Accounts[index].ID, latest.Providers.Copilot.Accounts[index].Label) {
			latest.Providers.Copilot.Accounts[index].Auth = sourceAccount.Auth
			latest.Providers.Copilot.Accounts[index].LastLimitedAt = sourceAccount.LastLimitedAt
			return saveConfigLocked(latest)
		}
	}
	return fmt.Errorf("persist Copilot account %q: account no longer exists", sourceAccount.ID)
}

func persistCodexRuntimeAccount(source *Config, accountIndex int) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	latest, err := loadConfig()
	if err != nil {
		return err
	}
	if len(source.Providers.Codex.Accounts) == 0 {
		latest.Providers.Codex.Auth = source.Providers.Codex.Auth
		return saveConfigLocked(latest)
	}
	if accountIndex < 0 || accountIndex >= len(source.Providers.Codex.Accounts) {
		return fmt.Errorf("persist Codex account: invalid account index %d", accountIndex)
	}
	sourceAccount := source.Providers.Codex.Accounts[accountIndex]
	for index := range latest.Providers.Codex.Accounts {
		if sameAccount(sourceAccount.ID, sourceAccount.Label, latest.Providers.Codex.Accounts[index].ID, latest.Providers.Codex.Accounts[index].Label) {
			latest.Providers.Codex.Accounts[index].Auth = sourceAccount.Auth
			latest.Providers.Codex.Accounts[index].LastLimitedAt = sourceAccount.LastLimitedAt
			return saveConfigLocked(latest)
		}
	}
	return fmt.Errorf("persist Codex account %q: account no longer exists", sourceAccount.ID)
}

func sameAccount(sourceID, sourceLabel, candidateID, candidateLabel string) bool {
	if sourceID != "" && candidateID != "" {
		return sourceID == candidateID
	}
	return sourceLabel != "" && sourceLabel == candidateLabel
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
		if merged.Providers.Kilo.AllowedModels == nil {
			merged.Providers.Kilo.AllowedModels = defaults.Providers.Kilo.AllowedModels
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

// ensureCodexModelMap adds codex known models to routing.model_map if not
// already present. Returns true if any entries were added (caller should save).
func ensureCodexModelMap(cfg *Config) bool {
	if cfg.Routing.ModelMap == nil {
		cfg.Routing.ModelMap = make(map[string]ModelMapEntry)
	}
	added := false
	for _, m := range codexKnownModels {
		if _, exists := cfg.Routing.ModelMap[m.ID]; !exists {
			cfg.Routing.ModelMap[m.ID] = ModelMapEntry{Provider: string(ProviderCodex)}
			fmt.Printf("  model_map: added %q → codex\n", m.ID)
			added = true
		}
	}
	return added
}

// migrateConfig upgrades the on-disk config according to mode.
func migrateConfig(mode ConfigMigrationMode) error {
	if mode == ConfigMigrationNone {
		return nil
	}
	existingConfig, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load existing config: %w", err)
	}
	defConfig, err := loadDefaultConfigFromExample()
	if err != nil {
		return fmt.Errorf("failed to load default config: %w", err)
	}
	path, err := getConfigPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	mergedConfig := mergeConfigs(existingConfig, defConfig, mode)
	if err := saveConfig(mergedConfig); err != nil {
		return fmt.Errorf("failed to save migrated config: %w", err)
	}
	fmt.Printf("Config migration completed (mode: %s)\n", mode)
	return nil
}
