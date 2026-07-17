package yarouter

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

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
	var legacy legacyV0Config
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil, fmt.Errorf("failed to parse config.example.json: %w", err)
	}
	return migrateV0ToV1(&legacy), nil
}

func mergeConfigs(existing, defaults *Config, mode ConfigMigrationMode) *Config {
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

func mergeTimeouts(destination, source *TimeoutsConfig) {
	if destination.HTTPClient == 0 {
		destination.HTTPClient = source.HTTPClient
	}
	if destination.ServerRead == 0 {
		destination.ServerRead = source.ServerRead
	}
	if destination.ServerWrite == 0 {
		destination.ServerWrite = source.ServerWrite
	}
	if destination.ServerIdle == 0 {
		destination.ServerIdle = source.ServerIdle
	}
	if destination.ProxyContext == 0 {
		destination.ProxyContext = source.ProxyContext
	}
	if destination.CircuitBreaker == 0 {
		destination.CircuitBreaker = source.CircuitBreaker
	}
	if destination.KeepAlive == 0 {
		destination.KeepAlive = source.KeepAlive
	}
	if destination.TLSHandshake == 0 {
		destination.TLSHandshake = source.TLSHandshake
	}
	if destination.DialTimeout == 0 {
		destination.DialTimeout = source.DialTimeout
	}
	if destination.IdleConnTimeout == 0 {
		destination.IdleConnTimeout = source.IdleConnTimeout
	}
}

func ensureCodexModelMap(cfg *Config) bool {
	if cfg.Routing.ModelMap == nil {
		cfg.Routing.ModelMap = make(map[string]ModelMapEntry)
	}
	added := false
	for _, model := range codexKnownModels {
		if _, exists := cfg.Routing.ModelMap[model.ID]; exists {
			continue
		}
		cfg.Routing.ModelMap[model.ID] = ModelMapEntry{Provider: string(ProviderCodex)}
		fmt.Printf("  model_map: added %q → codex\n", model.ID)
		added = true
	}
	return added
}

func migrateConfig(mode ConfigMigrationMode) error {
	if mode == ConfigMigrationNone {
		return nil
	}
	existing, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load existing config: %w", err)
	}
	defaults, err := loadDefaultConfigFromExample()
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
	merged := mergeConfigs(existing, defaults, mode)
	if err := saveConfig(merged); err != nil {
		return fmt.Errorf("failed to save migrated config: %w", err)
	}
	fmt.Printf("Config migration completed (mode: %s)\n", mode)
	return nil
}
