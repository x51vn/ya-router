package yarouter

import "fmt"

func saveConfig(cfg *Config) error {
	configWriteMu.Lock()
	defer configWriteMu.Unlock()
	return saveConfigLocked(cfg)
}

func saveConfigLocked(cfg *Config) error {
	if manager := currentConfigState(); manager != nil {
		snapshot := manager.Snapshot()
		_, err := manager.Apply(snapshot.Revision, cfg, cfg, "runtime", false)
		return err
	}
	path, err := getConfigPath()
	if err != nil {
		return err
	}
	manager, err := openConfigState(path, "ya")
	if err != nil {
		return err
	}
	defer manager.Close()
	snapshot := manager.Snapshot()
	_, err = manager.Apply(snapshot.Revision, cfg, cfg, "ya", false)
	return err
}

// persistCopilotRuntimeAccount merges only runtime-owned authentication and
// cooldown state into the latest desired configuration.
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
		candidate := &latest.Providers.Copilot.Accounts[index]
		if sameAccount(sourceAccount.ID, sourceAccount.Label, candidate.ID, candidate.Label) {
			candidate.Auth = sourceAccount.Auth
			candidate.LastLimitedAt = sourceAccount.LastLimitedAt
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
		candidate := &latest.Providers.Codex.Accounts[index]
		if sameAccount(sourceAccount.ID, sourceAccount.Label, candidate.ID, candidate.Label) {
			candidate.Auth = sourceAccount.Auth
			candidate.LastLimitedAt = sourceAccount.LastLimitedAt
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
