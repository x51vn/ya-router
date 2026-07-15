package config

// Redact returns a deep copy safe for control-plane read models. Raw secret
// values are removed while non-secret metadata and SecretReference values are
// preserved.
func Redact(source *Config) *Config {
	cloned := Clone(source)
	if cloned == nil {
		return nil
	}
	redactCopilotAuth(&cloned.Providers.Copilot.Auth)
	for index := range cloned.Providers.Copilot.Accounts {
		redactCopilotAuth(&cloned.Providers.Copilot.Accounts[index].Auth)
	}
	redactCodexAuth(&cloned.Providers.Codex.Auth)
	for index := range cloned.Providers.Codex.Accounts {
		redactCodexAuth(&cloned.Providers.Codex.Accounts[index].Auth)
	}
	cloned.Providers.Kilo.APIKey = ""
	cloned.Providers.Kilo.OrganizationID = ""
	return cloned
}

func redactCopilotAuth(auth *CopilotAuthState) {
	auth.GitHubToken = ""
	auth.CopilotToken = ""
}

func redactCodexAuth(auth *CodexAuthState) {
	auth.APIKey = ""
	auth.AccessToken = ""
	auth.RefreshToken = ""
	auth.AccountID = ""
}
