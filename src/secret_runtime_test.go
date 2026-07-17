package yarouter

import (
	"path/filepath"
	"testing"

	secretpkg "github.com/duvu/ya-router/internal/secret"
)

func TestDaemonSecretStorePersistsManagedCredentials(t *testing.T) {
	oldConfigPath := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldConfigPath })

	store, err := newDaemonSecretStore(defaultConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Set("operator", "kilo/api_key", "managed-value"); err != nil {
		t.Fatal(err)
	}

	reopened, err := newDaemonSecretStore(defaultConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	value, source, ok := reopened.Resolve("kilo/api_key")
	if !ok || value != "managed-value" || source != secretpkg.SourceManaged {
		t.Fatalf("managed credential after restart = %q, %s, %t", value, source, ok)
	}
}
