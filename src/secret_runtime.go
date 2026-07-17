// secret_runtime.go wires the daemon-owned SecretStore (YA-TUI-07) into the
// managed runtime. Environment and official-store credentials are registered
// read-only so a managed write can never silently shadow them, and control
// clients only ever see redacted metadata.
package yarouter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	controlpkg "github.com/duvu/ya-router/internal/control"
	secretpkg "github.com/duvu/ya-router/internal/secret"
)

const secretStorePathEnv = "YA_ROUTER_SECRETS_PATH"

// secretAuditBridge forwards SecretStore audit events into the existing control
// audit sink without ever carrying a secret value.
type secretAuditBridge struct {
	sink controlpkg.AuditSink
}

func (bridge secretAuditBridge) Record(event secretpkg.AuditEvent) {
	if bridge.sink == nil {
		return
	}
	bridge.sink.Record(controlpkg.AuditEvent{
		Timestamp: event.Timestamp,
		Actor:     event.Actor,
		Action:    "secret_" + event.Action,
		Target:    event.SecretID + " (" + string(event.Source) + ")",
		Result:    "applied",
	})
}

// newDaemonSecretStore builds the SecretStore and seeds read-only environment
// credentials discovered from the process environment. Inline legacy config
// values are registered read-only under legacy source so their posture is
// visible but they can be superseded by a managed write only when they are not
// environment-provided.
func newDaemonSecretStore(config *Config, auditSink controlpkg.AuditSink) (*secretpkg.MemoryStore, error) {
	path, err := configuredSecretStorePath()
	if err != nil {
		return nil, err
	}
	store, err := secretpkg.OpenFileStore(path, secretAuditBridge{sink: auditSink})
	if err != nil {
		return nil, fmt.Errorf("open daemon secret store: %w", err)
	}

	// Environment-provided credentials are highest precedence and read-only.
	if err := registerEnvSecret(store, "codex/api_key", "OPENAI_API_KEY"); err != nil {
		return nil, err
	}
	if err := registerEnvSecret(store, "kilo/api_key", "KILO_API_KEY"); err != nil {
		return nil, err
	}
	if err := registerEnvSecret(store, "kilo/organization_id", kiloOrganizationIDEnv); err != nil {
		return nil, err
	}

	// Inline legacy config credentials are visible as legacy source so an
	// operator can see they exist and migrate them into the managed store.
	if config != nil {
		if key := strings.TrimSpace(config.Providers.Codex.Auth.APIKey); key != "" {
			if err := store.RegisterReadOnly("codex/api_key_legacy", key, secretpkg.SourceLegacyConfig); err != nil {
				return nil, err
			}
		}
		if key := strings.TrimSpace(config.Providers.Kilo.APIKey); key != "" {
			if err := store.RegisterReadOnly("kilo/api_key_legacy", key, secretpkg.SourceLegacyConfig); err != nil {
				return nil, err
			}
		}
	}
	return store, nil
}

func configuredSecretStorePath() (string, error) {
	if path := strings.TrimSpace(os.Getenv(secretStorePathEnv)); path != "" {
		return path, nil
	}
	configPath, err := getConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(configPath), "secrets.json"), nil
}

func registerEnvSecret(store *secretpkg.MemoryStore, slot, envName string) error {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return store.RegisterReadOnly(slot, value, secretpkg.SourceEnvironment)
	}
	return nil
}
