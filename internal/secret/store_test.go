package secret

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourcePrecedence(t *testing.T) {
	// Environment must outrank managed, official, and legacy.
	best, ok := Resolve(
		Candidate{Value: "legacy", Source: SourceLegacyConfig},
		Candidate{Value: "managed", Source: SourceManaged},
		Candidate{Value: "official", Source: SourceOfficialStore},
		Candidate{Value: "env", Source: SourceEnvironment},
	)
	if !ok || best.Value != "env" {
		t.Fatalf("resolved %+v, want env", best)
	}
}

func TestResolveSkipsEmptyValues(t *testing.T) {
	best, ok := Resolve(
		Candidate{Value: "", Source: SourceEnvironment},
		Candidate{Value: "managed", Source: SourceManaged},
	)
	if !ok || best.Value != "managed" {
		t.Fatalf("resolved %+v, want managed", best)
	}
}

func TestResolveNoneConfigured(t *testing.T) {
	if _, ok := Resolve(Candidate{Source: SourceEnvironment}); ok {
		t.Fatal("expected not-configured")
	}
}

// TestManagedWriteCannotShadowEnvironment is the key security invariant:
// environment credentials are read-only and a managed Set must be refused.
func TestManagedWriteCannotShadowEnvironment(t *testing.T) {
	store := NewMemoryStore(nil)
	if err := store.RegisterReadOnly("codex/api_key", "env-secret", SourceEnvironment); err != nil {
		t.Fatal(err)
	}
	_, err := store.Set("operator", "codex/api_key", "attempted-override")
	if err == nil {
		t.Fatal("expected read-only rejection")
	}
	value, source, ok := store.Resolve("codex/api_key")
	if !ok || value != "env-secret" || source != SourceEnvironment {
		t.Fatalf("environment value was overwritten: %q %s", value, source)
	}
}

func TestOfficialStoreIsReadOnly(t *testing.T) {
	store := NewMemoryStore(nil)
	_ = store.RegisterReadOnly("codex/access_token", "official", SourceOfficialStore)
	if err := store.Delete("operator", "codex/access_token"); err == nil {
		t.Fatal("expected official store delete to be refused")
	}
}

func TestManagedSetRotateDelete(t *testing.T) {
	var events []AuditEvent
	store := NewMemoryStore(AuditSinkFunc(func(e AuditEvent) { events = append(events, e) }))

	meta, err := store.Set("admin", "kilo/api_key", "v1")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Version != 1 || meta.Source != SourceManaged {
		t.Fatalf("meta = %+v", meta)
	}
	meta, err = store.Set("admin", "kilo/api_key", "v2")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Version != 2 {
		t.Fatalf("rotation did not bump version: %+v", meta)
	}
	if err := store.Delete("admin", "kilo/api_key"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Metadata("kilo/api_key"); ok {
		t.Fatal("secret should be gone after delete")
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 audit events, got %d", len(events))
	}
}

func TestFileStorePersistsManagedSecretsWithoutPersistingReadOnlySources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	store, err := OpenFileStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterReadOnly("codex/api_key", "environment-value", SourceEnvironment); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Set("operator", "kilo/api_key", "managed-value"); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenFileStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, source, ok := reopened.Resolve("kilo/api_key")
	if !ok || value != "managed-value" || source != SourceManaged {
		t.Fatalf("managed secret after restart = %q, %s, %t", value, source, ok)
	}
	if _, _, ok := reopened.Resolve("codex/api_key"); ok {
		t.Fatal("read-only source must not be persisted")
	}
}

// TestMetadataAndAuditNeverContainSecretValue guards the redaction contract.
func TestMetadataAndAuditNeverContainSecretValue(t *testing.T) {
	var captured []AuditEvent
	store := NewMemoryStore(AuditSinkFunc(func(e AuditEvent) { captured = append(captured, e) }))
	const secretValue = "super-secret-token-value"
	if _, err := store.Set("admin", "codex/api_key", secretValue); err != nil {
		t.Fatal(err)
	}

	meta, _ := store.Metadata("codex/api_key")
	metaJSON, _ := json.Marshal(meta)
	if strings.Contains(string(metaJSON), secretValue) {
		t.Fatalf("metadata leaked secret: %s", metaJSON)
	}
	listJSON, _ := json.Marshal(store.List())
	if strings.Contains(string(listJSON), secretValue) {
		t.Fatalf("list leaked secret: %s", listJSON)
	}
	for _, event := range captured {
		eventJSON, _ := json.Marshal(event)
		if strings.Contains(string(eventJSON), secretValue) {
			t.Fatalf("audit leaked secret: %s", eventJSON)
		}
	}
	// But Resolve (internal-only) must still return the value.
	if value, _, ok := store.Resolve("codex/api_key"); !ok || value != secretValue {
		t.Fatalf("internal resolve failed: %q", value)
	}
}

func TestStoreControllerPostureRedactsAndResolves(t *testing.T) {
	store := NewMemoryStore(nil)
	_, _ = store.Set("admin", "kilo/api_key", "managed-value")
	controller := NewStoreController(store, map[string]func() []Candidate{
		"kilo/api_key": func() []Candidate { return nil },
		"codex/api_key": func() []Candidate {
			return []Candidate{{Value: "env-value", Source: SourceEnvironment}}
		},
	})

	// Managed slot resolves to the managed value; posture is redacted.
	got, ok := controller.ResolveCredential("kilo/api_key")
	if !ok || got.Value != "managed-value" || got.Source != SourceManaged {
		t.Fatalf("kilo resolve = %+v ok=%v", got, ok)
	}
	posture := controller.Posture("kilo/api_key")
	postureJSON, _ := json.Marshal(posture)
	if strings.Contains(string(postureJSON), "managed-value") {
		t.Fatalf("posture leaked value: %s", postureJSON)
	}
	if !posture.Configured || posture.Source != SourceManaged {
		t.Fatalf("posture = %+v", posture)
	}

	// Env-backed slot: environment wins and is marked read-only.
	envPosture := controller.Posture("codex/api_key")
	if envPosture.Source != SourceEnvironment || !envPosture.ReadOnly {
		t.Fatalf("env posture = %+v", envPosture)
	}
	// SetManaged for that slot must not report success shadowing env at resolve
	// time (env still wins because it outranks managed).
	_ = controller.SetManaged("op", "codex/api_key", "managed-attempt")
	resolved, _ := controller.ResolveCredential("codex/api_key")
	if resolved.Source != SourceEnvironment {
		t.Fatalf("environment must still win: %+v", resolved)
	}
}
