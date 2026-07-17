package yarouter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	defaultLogFilePath      = "logs/ya-router.log"
	defaultLogFileSizeMiB   = 5
	defaultRetainedLogFiles = 2
)

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
	setLegacyTimeouts(&cfg.Timeouts, v0.Timeouts.HTTPClient, v0.Timeouts.ServerRead,
		v0.Timeouts.ServerWrite, v0.Timeouts.ServerIdle, v0.Timeouts.ProxyContext,
		v0.Timeouts.CircuitBreaker, v0.Timeouts.KeepAlive, v0.Timeouts.TLSHandshake,
		v0.Timeouts.DialTimeout, v0.Timeouts.IdleConnTimeout)
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

func defaultConfig() *Config {
	cfg := &Config{Port: 7071, ConfigVersion: currentConfigVersion}
	cfg.Routing.DefaultModel = "gpt-5-mini"
	cfg.Routing.DefaultProvider = string(ProviderCopilot)
	cfg.Routing.VirtualModels = defaultVirtualModels()
	cfg.Providers.Copilot.Enabled = true
	cfg.Providers.Copilot.Auth.Mode = "device_code"
	cfg.Providers.Codex.Enabled = false
	cfg.Providers.Codex.Auth.Mode = "device_code"
	cfg.Providers.Kilo.Enabled = false
	cfg.Providers.Kilo.AllowAnonymous = true
	setDefaultLogging(cfg)
	setDefaultTimeouts(cfg)
	return cfg
}

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
	if cfg.Routing.VirtualModels == nil {
		cfg.Routing.VirtualModels = defaultVirtualModels()
	}
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
	setDefaultLogging(cfg)
	setDefaultTimeouts(cfg)
}

func setDefaultLogging(cfg *Config) {
	if strings.TrimSpace(cfg.Logging.FilePath) == "" {
		cfg.Logging.FilePath = defaultLogFilePath
	}
	if cfg.Logging.MaxFileSizeMiB <= 0 {
		cfg.Logging.MaxFileSizeMiB = defaultLogFileSizeMiB
	}
	if cfg.Logging.RetainedFiles <= 0 {
		cfg.Logging.RetainedFiles = defaultRetainedLogFiles
	}
}

func defaultVirtualModels() map[string]VirtualModelConfig {
	return map[string]VirtualModelConfig{
		"thiendu": {
			Strategy: "priority",
			Targets: []string{
				"github/gpt-5-mini",
				"codex/gpt-5.4-mini",
				"kilo/kilo-auto/free",
			},
		},
	}
}

func normalizeCopilotAccounts(provider *CopilotProviderConfig) {
	if len(provider.Accounts) == 0 && provider.Auth.GitHubToken != "" {
		provider.Accounts = []CopilotAccount{{ID: stableAccountID("copilot", "primary"), Label: "primary", Auth: provider.Auth}}
		provider.Auth = CopilotAuthState{}
	}
	ensureCopilotAccountIDs(provider.Accounts)
}

func normalizeCodexAccounts(provider *CodexProviderConfig) {
	if len(provider.Accounts) == 0 && (provider.Auth.AccessToken != "" || provider.Auth.APIKey != "") {
		provider.Accounts = []CodexAccount{{ID: stableAccountID("codex", "primary"), Label: "primary", Auth: provider.Auth}}
		provider.Auth = CodexAuthState{}
	}
	ensureCodexAccountIDs(provider.Accounts)
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

func setDefaultTimeouts(cfg *Config) {
	timeouts := &cfg.Timeouts
	if timeouts.HTTPClient == 0 {
		timeouts.HTTPClient = 300
	}
	if timeouts.ServerRead == 0 {
		timeouts.ServerRead = 30
	}
	if timeouts.ServerWrite == 0 {
		timeouts.ServerWrite = 300
	}
	if timeouts.ServerIdle == 0 {
		timeouts.ServerIdle = 120
	}
	if timeouts.ProxyContext == 0 {
		timeouts.ProxyContext = 300
	}
	if timeouts.CircuitBreaker == 0 {
		timeouts.CircuitBreaker = 30
	}
	if timeouts.KeepAlive == 0 {
		timeouts.KeepAlive = 30
	}
	if timeouts.TLSHandshake == 0 {
		timeouts.TLSHandshake = 10
	}
	if timeouts.DialTimeout == 0 {
		timeouts.DialTimeout = 10
	}
	if timeouts.IdleConnTimeout == 0 {
		timeouts.IdleConnTimeout = 90
	}
}
