package yarouter

import (
	"context"
	"errors"
	"net/http"
	"testing"

	secretpkg "github.com/duvu/ya-router/internal/secret"
)

func TestCopilotEnsureAuthenticatedRequiresManagedAuth(t *testing.T) {
	provider := NewCopilotProvider(defaultConfig())
	err := provider.EnsureAuthenticated(context.Background())
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("expected ProviderError, got %v", err)
	}
	if providerErr.Kind != ProviderErrorAuthRequired || providerErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("provider error = %+v", providerErr)
	}
}

func TestCopilotUsesManagedCredentialResolver(t *testing.T) {
	store := secretpkg.NewMemoryStore(nil)
	if _, err := store.Set("operator", "copilot/token", "managed-token"); err != nil {
		t.Fatal(err)
	}
	provider := NewCopilotProviderWithAuth(defaultConfig(), secretpkg.NewStoreController(store, nil))
	_, token := provider.credentialSnapshot()
	if token != "managed-token" {
		t.Fatalf("token = %q", token)
	}
	if err := provider.EnsureAuthenticated(context.Background()); err != nil {
		t.Fatalf("EnsureAuthenticated() error = %v", err)
	}
}
