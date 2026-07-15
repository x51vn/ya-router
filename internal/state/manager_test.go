package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configschema "github.com/duvu/ya-router/internal/config"
)

func testConfig(port int, secret string) *configschema.Config {
	return &configschema.Config{
		Port:          port,
		ConfigVersion: 1,
		Routing:       configschema.Routing{DefaultModel: "gpt-test", DefaultProvider: "copilot"},
		Providers: configschema.Providers{
			Copilot: configschema.CopilotProvider{Enabled: true, Auth: configschema.CopilotAuthState{Mode: "device_code", GitHubToken: secret}},
		},
	}
}

func writeConfig(t *testing.T, path string, config *configschema.Config) {
	t.Helper()
	payload, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func openTestManager(t *testing.T, path string, hook func(FaultStage) error) *Manager {
	t.Helper()
	manager, err := Open(Options{
		Path:      path,
		Initial:   testConfig(7071, "initial-secret"),
		FaultHook: hook,
		Validator: func(config *configschema.Config) error {
			if config.Port < 1 || config.Port > 65535 {
				return errors.New("invalid port")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func TestStaleRevisionFailsDeterministically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfig(t, path, testConfig(7071, "secret"))
	manager := openTestManager(t, path, nil)
	current := manager.Snapshot()

	updated := configschema.Clone(current.Desired)
	updated.Port = 8080
	if _, err := manager.Apply(current.Revision, updated, updated, "test", false); err != nil {
		t.Fatal(err)
	}
	_, err := manager.Apply(current.Revision, testConfig(9090, "secret"), nil, "stale", false)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if conflict.Expected != current.Revision || conflict.Current != current.Revision+1 {
		t.Fatalf("unexpected conflict details: %+v", conflict)
	}
}

func TestFaultBeforeRenameLeavesValidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := testConfig(7071, "credential-must-survive")
	writeConfig(t, path, original)
	manager := openTestManager(t, path, func(stage FaultStage) error {
		if stage == FaultBeforeConfigRename {
			return errors.New("simulated crash")
		}
		return nil
	})
	candidate := testConfig(8080, "new-secret")
	if _, err := manager.Apply(manager.Snapshot().Revision, candidate, candidate, "test", false); err == nil {
		t.Fatal("expected fault-injection error")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got configschema.Config
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("persisted config became partial: %v\n%s", err, payload)
	}
	if got.Port != original.Port || got.Providers.Copilot.Auth.GitHubToken != "credential-must-survive" {
		t.Fatalf("original config was replaced: %+v", got)
	}
}

func TestCrashBetweenRenamesRecoversMonotonicMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfig(t, path, testConfig(7071, "secret"))
	manager := openTestManager(t, path, func(stage FaultStage) error {
		if stage == FaultAfterConfigRename {
			return errors.New("simulated process exit")
		}
		return nil
	})
	oldRevision := manager.Snapshot().Revision
	if _, err := manager.Apply(oldRevision, testConfig(8080, "secret"), nil, "test", false); err == nil {
		t.Fatal("expected simulated crash")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openTestManager(t, path, nil)
	snapshot := reopened.Snapshot()
	if snapshot.Desired.Port != 8080 {
		t.Fatalf("valid renamed config was not recovered: %+v", snapshot.Desired)
	}
	if snapshot.Revision <= oldRevision {
		t.Fatalf("revision did not advance during recovery: old=%d new=%d", oldRevision, snapshot.Revision)
	}
}

func TestSecondDaemonGetsActionableLockError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfig(t, path, testConfig(7071, "secret"))
	first := openTestManager(t, path, nil)
	_, err := Open(Options{Path: path, Initial: testConfig(7071, "secret")})
	if err == nil {
		t.Fatal("expected second manager to fail")
	}
	if !strings.Contains(err.Error(), "another ya-routerd process") {
		t.Fatalf("lock error is not actionable: %v", err)
	}
	_ = first
}

func TestRollbackPreservesCredentialsAndPathCompatibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfig(t, path, testConfig(7071, "legacy-credential"))
	manager := openTestManager(t, path, nil)
	initial := manager.Snapshot()
	if _, err := manager.Apply(initial.Revision, testConfig(8080, "rotated-credential"), nil, "change", false); err != nil {
		t.Fatal(err)
	}
	changed := manager.Snapshot()
	rolledBack, err := manager.Rollback(changed.Revision, "rollback")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Desired.Port != 7071 || rolledBack.Desired.Providers.Copilot.Auth.GitHubToken != "legacy-credential" {
		t.Fatalf("rollback did not preserve original config: %+v", rolledBack.Desired)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw configschema.Config
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("config path no longer contains compatible config JSON: %v", err)
	}
	if raw.Providers.Copilot.Auth.GitHubToken != "legacy-credential" {
		t.Fatal("credential was lost from compatible config path")
	}
}

func TestValidateReportsDesiredEffectiveAndRestartPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	writeConfig(t, path, testConfig(7071, "secret"))
	manager := openTestManager(t, path, nil)
	candidate := testConfig(8080, "secret")
	preview, err := manager.Validate(candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Changed || len(preview.RestartRequired) != 1 || preview.RestartRequired[0] != "port" {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	if manager.Snapshot().Revision != preview.CurrentRevision {
		t.Fatal("validation mutated state")
	}
}
