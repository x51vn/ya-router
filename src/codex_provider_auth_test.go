package yarouter

import (
	"context"
	"testing"

	secretpkg "github.com/duvu/ya-router/internal/secret"
)

func TestCodexUsesManagedAPIKeyResolver(t *testing.T) {
	cfg := defaultConfig()
	cfg.Providers.Codex.Auth.Mode = "api_key"
	store := secretpkg.NewMemoryStore(nil)
	if _, err := store.Set("operator", "codex/api_key", "managed-api-key"); err != nil {
		t.Fatal(err)
	}
	provider := NewCodexProviderWithAuth(cfg, secretpkg.NewStoreController(store, nil))
	if err := provider.EnsureAuthenticated(context.Background()); err != nil {
		t.Fatalf("EnsureAuthenticated() error = %v", err)
	}
	token, _, chatgpt := provider.authCredentials()
	if token != "managed-api-key" || chatgpt {
		t.Fatalf("credential = %q, chatgpt=%t", token, chatgpt)
	}
}

func TestCodexUsesManagedChatGPTCredentialResolver(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	cfg := defaultConfig()
	cfg.Providers.Codex.Auth.Mode = "chatgpt"
	store := secretpkg.NewMemoryStore(nil)
	if _, err := store.Set("operator", "codex/access_token", "managed-access-token"); err != nil {
		t.Fatal(err)
	}
	provider := NewCodexProviderWithAuth(cfg, secretpkg.NewStoreController(store, nil))
	if err := provider.EnsureAuthenticated(context.Background()); err != nil {
		t.Fatal("managed ChatGPT credential was not accepted")
	}
	token, _, chatgpt := provider.authCredentials()
	if token != "managed-access-token" || !chatgpt {
		t.Fatal("managed ChatGPT credential was not selected")
	}
}
