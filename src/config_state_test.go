package yarouter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedConfigStateOwnsRevisionedWrites(t *testing.T) {
	oldOverride := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldOverride })

	release, err := acquireManagedConfigState("test-daemon")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release() })

	config, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.Port = 8080
	config.Providers.Copilot.Auth.GitHubToken = "credential"
	if err := saveConfig(config); err != nil {
		t.Fatal(err)
	}

	snapshot := currentConfigState().Snapshot()
	if snapshot.Revision < 2 || snapshot.Desired.Port != 8080 {
		t.Fatalf("unexpected managed snapshot: %+v", snapshot)
	}
	if snapshot.Digest == "" || snapshot.EffectiveDigest == "" {
		t.Fatal("revision metadata is incomplete")
	}

	_, err = openConfigState(configPathOverride, "second-daemon")
	if err == nil || !strings.Contains(err.Error(), "another ya-routerd process") {
		t.Fatalf("expected actionable single-writer error, got %v", err)
	}

	payload, err := os.ReadFile(configPathOverride)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Config
	if err := json.Unmarshal(payload, &persisted); err != nil {
		t.Fatalf("config path is no longer V1-compatible JSON: %v", err)
	}
	if persisted.Providers.Copilot.Auth.GitHubToken != "credential" {
		t.Fatal("credential was lost during managed persistence")
	}
}

func TestManagedConfigDryRunAndRollback(t *testing.T) {
	oldOverride := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldOverride })

	manager, err := openConfigState(configPathOverride, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	initial := manager.Snapshot()
	baseline := defaultConfig()
	baseline.Providers.Codex.Auth.APIKey = "original-key"
	if _, err := manager.Apply(initial.Revision, baseline, baseline, "baseline", false); err != nil {
		t.Fatal(err)
	}
	current := manager.Snapshot()
	candidate := configschemaCloneForTest(current.Desired)
	candidate.Port = 9090
	preview, err := manager.Validate(candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Changed || len(preview.RestartRequired) == 0 {
		t.Fatalf("unexpected validation preview: %+v", preview)
	}
	if manager.Snapshot().Revision != current.Revision {
		t.Fatal("validation changed the revision")
	}

	if _, err := manager.Apply(current.Revision, candidate, nil, "change", false); err != nil {
		t.Fatal(err)
	}
	changed := manager.Snapshot()
	rolledBack, err := manager.Rollback(changed.Revision, "rollback")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Desired.Port != baseline.Port || codexAPIKeyForTest(rolledBack.Desired) != "original-key" {
		t.Fatalf("rollback did not restore last-known-good config: %+v", rolledBack.Desired)
	}
}

func codexAPIKeyForTest(config *Config) string {
	if config.Providers.Codex.Auth.APIKey != "" {
		return config.Providers.Codex.Auth.APIKey
	}
	if len(config.Providers.Codex.Accounts) > 0 {
		return config.Providers.Codex.Accounts[0].Auth.APIKey
	}
	return ""
}

func configschemaCloneForTest(config *Config) *Config {
	payload, _ := json.Marshal(config)
	var cloned Config
	_ = json.Unmarshal(payload, &cloned)
	return &cloned
}
