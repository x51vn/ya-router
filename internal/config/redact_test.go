package config

import "testing"

func TestRedactRemovesSecretValuesAndPreservesReferences(t *testing.T) {
	ref := &SecretReference{ID: "secret/copilot/primary", Source: "service"}
	config := &Config{Providers: Providers{
		Copilot: CopilotProvider{Auth: CopilotAuthState{GitHubToken: "github", CopilotToken: "copilot", GitHubTokenRef: ref}},
		Codex:   CodexProvider{Auth: CodexAuthState{APIKey: "key", AccessToken: "access", RefreshToken: "refresh", AccountID: "raw-account"}},
		Kilo:    KiloProvider{APIKey: "kilo", OrganizationID: "org"},
	}}
	redacted := Redact(config)
	if redacted.Providers.Copilot.Auth.GitHubToken != "" || redacted.Providers.Copilot.Auth.CopilotToken != "" || redacted.Providers.Codex.Auth.APIKey != "" || redacted.Providers.Codex.Auth.AccessToken != "" || redacted.Providers.Codex.Auth.RefreshToken != "" || redacted.Providers.Codex.Auth.AccountID != "" || redacted.Providers.Kilo.APIKey != "" || redacted.Providers.Kilo.OrganizationID != "" {
		t.Fatalf("secret value survived redaction: %+v", redacted)
	}
	if redacted.Providers.Copilot.Auth.GitHubTokenRef == nil || redacted.Providers.Copilot.Auth.GitHubTokenRef.ID != ref.ID {
		t.Fatal("secret reference was not preserved")
	}
	if config.Providers.Copilot.Auth.GitHubToken == "" {
		t.Fatal("redaction mutated the source")
	}
}
