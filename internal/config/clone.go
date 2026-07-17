package config

// Clone returns a deep copy suitable for publication in an immutable runtime
// snapshot. Secret-bearing strings are copied as values and are never logged.
func Clone(source *Config) *Config {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Routing.ModelMap = cloneModelMap(source.Routing.ModelMap)
	cloned.Routing.VirtualModels = cloneVirtualModels(source.Routing.VirtualModels)
	cloned.Providers.Copilot.Auth = cloneCopilotAuth(source.Providers.Copilot.Auth)
	cloned.Providers.Copilot.AllowedModels = cloneStrings(source.Providers.Copilot.AllowedModels)
	cloned.Providers.Copilot.Accounts = cloneCopilotAccounts(source.Providers.Copilot.Accounts)
	cloned.Providers.Codex.Auth = cloneCodexAuth(source.Providers.Codex.Auth)
	cloned.Providers.Codex.AllowedModels = cloneStrings(source.Providers.Codex.AllowedModels)
	cloned.Providers.Codex.Accounts = cloneCodexAccounts(source.Providers.Codex.Accounts)
	cloned.Providers.Kilo.APIKeyRef = cloneSecretReference(source.Providers.Kilo.APIKeyRef)
	cloned.Providers.Kilo.OrganizationIDRef = cloneSecretReference(source.Providers.Kilo.OrganizationIDRef)
	cloned.Providers.Kilo.AllowedModels = cloneStrings(source.Providers.Kilo.AllowedModels)
	return &cloned
}

func cloneModelMap(source map[string]ModelMapEntry) map[string]ModelMapEntry {
	if source == nil {
		return nil
	}
	cloned := make(map[string]ModelMapEntry, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneVirtualModels(source map[string]VirtualModel) map[string]VirtualModel {
	if source == nil {
		return nil
	}
	cloned := make(map[string]VirtualModel, len(source))
	for key, value := range source {
		value.Targets = cloneStrings(value.Targets)
		cloned[key] = value
	}
	return cloned
}

func cloneStrings(source []string) []string {
	if source == nil {
		return nil
	}
	return append([]string(nil), source...)
}

func cloneSecretReference(source *SecretReference) *SecretReference {
	if source == nil {
		return nil
	}
	cloned := *source
	return &cloned
}

func cloneCopilotAuth(source CopilotAuthState) CopilotAuthState {
	source.GitHubTokenRef = cloneSecretReference(source.GitHubTokenRef)
	source.CopilotTokenRef = cloneSecretReference(source.CopilotTokenRef)
	return source
}

func cloneCodexAuth(source CodexAuthState) CodexAuthState {
	source.APIKeyRef = cloneSecretReference(source.APIKeyRef)
	source.AccessTokenRef = cloneSecretReference(source.AccessTokenRef)
	source.RefreshTokenRef = cloneSecretReference(source.RefreshTokenRef)
	return source
}

func cloneCopilotAccounts(source []CopilotAccount) []CopilotAccount {
	if source == nil {
		return nil
	}
	cloned := append([]CopilotAccount(nil), source...)
	for index := range cloned {
		cloned[index].Auth = cloneCopilotAuth(cloned[index].Auth)
	}
	return cloned
}

func cloneCodexAccounts(source []CodexAccount) []CodexAccount {
	if source == nil {
		return nil
	}
	cloned := append([]CodexAccount(nil), source...)
	for index := range cloned {
		cloned[index].Auth = cloneCodexAuth(cloned[index].Auth)
	}
	return cloned
}
