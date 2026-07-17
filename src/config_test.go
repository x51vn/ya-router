package yarouter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	configschema "github.com/duvu/ya-router/internal/config"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	if cfg.Port != 7071 {
		t.Errorf("Port = %d, want 7071", cfg.Port)
	}
	if cfg.ConfigVersion != 1 {
		t.Errorf("ConfigVersion = %d, want 1", cfg.ConfigVersion)
	}
	if cfg.Routing.DefaultModel != "gpt-5-mini" {
		t.Errorf("DefaultModel = %q, want gpt-5-mini", cfg.Routing.DefaultModel)
	}
	if cfg.Routing.DefaultProvider != "copilot" {
		t.Errorf("DefaultProvider = %q, want copilot", cfg.Routing.DefaultProvider)
	}
	if !cfg.Providers.Copilot.Enabled {
		t.Error("Copilot should be enabled by default")
	}
	if cfg.Providers.Copilot.Auth.Mode != "device_code" {
		t.Errorf("Copilot auth mode = %q, want device_code", cfg.Providers.Copilot.Auth.Mode)
	}
	if cfg.Providers.Codex.Enabled {
		t.Error("Codex should be disabled by default")
	}
	if cfg.Providers.Codex.Auth.Mode != "device_code" {
		t.Errorf("Codex auth mode = %q, want device_code", cfg.Providers.Codex.Auth.Mode)
	}
	if cfg.Timeouts.HTTPClient != 300 {
		t.Errorf("HTTPClient timeout = %d, want 300", cfg.Timeouts.HTTPClient)
	}
}

func Test_defaultConfig_uses_bounded_file_logging_defaults(t *testing.T) {
	// Given
	cfg := defaultConfig()

	// When
	logging := cfg.Logging

	// Then
	if logging.FilePath != defaultLogFilePath {
		t.Fatalf("log file path = %q, want %q", logging.FilePath, defaultLogFilePath)
	}
	if logging.MaxFileSizeMiB != defaultLogFileSizeMiB {
		t.Fatalf("max log file size = %d, want %d", logging.MaxFileSizeMiB, defaultLogFileSizeMiB)
	}
	if logging.RetainedFiles != defaultRetainedLogFiles {
		t.Fatalf("retained log files = %d, want %d", logging.RetainedFiles, defaultRetainedLogFiles)
	}
}

func Test_applyConfigDefaults_restores_omitted_logging_settings(t *testing.T) {
	// Given
	cfg := &Config{}

	// When
	applyConfigDefaults(cfg)

	// Then
	if cfg.Logging.FilePath != defaultLogFilePath ||
		cfg.Logging.MaxFileSizeMiB != defaultLogFileSizeMiB ||
		cfg.Logging.RetainedFiles != defaultRetainedLogFiles {
		t.Fatalf("logging defaults = %+v", cfg.Logging)
	}
}

func TestGetConfigPath_UsesConfigPathEnv(t *testing.T) {
	old := configPathOverride
	configPathOverride = ""
	defer func() { configPathOverride = old }()

	targetDir := filepath.Join(t.TempDir(), "override")
	target := filepath.Join(targetDir, "config.json")
	t.Setenv("YA_ROUTER_CONFIG_PATH", target)
	t.Setenv("YA_ROUTER_CONFIG_DIR", "/dev/null/should-not-be-used")

	got, err := getConfigPath()
	if err != nil {
		t.Fatalf("getConfigPath: %v", err)
	}
	if got != target {
		t.Fatalf("config path = %q, want %q", got, target)
	}
	info, err := os.Stat(targetDir)
	if err != nil {
		t.Fatalf("config dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("config dir is not a directory: %s", targetDir)
	}
}

func TestGetConfigPath_UsesConfigDirEnv(t *testing.T) {
	old := configPathOverride
	configPathOverride = ""
	defer func() { configPathOverride = old }()

	targetDir := filepath.Join(t.TempDir(), "config-dir")
	t.Setenv("YA_ROUTER_CONFIG_DIR", targetDir)
	t.Setenv("YA_ROUTER_CONFIG_PATH", "")

	got, err := getConfigPath()
	if err != nil {
		t.Fatalf("getConfigPath: %v", err)
	}
	want := filepath.Join(targetDir, configFileName)
	if got != want {
		t.Fatalf("config path = %q, want %q", got, want)
	}
	info, err := os.Stat(targetDir)
	if err != nil {
		t.Fatalf("config dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("config dir is not a directory: %s", targetDir)
	}
}

func TestApplyConfigDefaults_FillsZeros(t *testing.T) {
	cfg := &Config{}
	applyConfigDefaults(cfg)

	if cfg.Port != 7071 {
		t.Errorf("Port = %d, want 7071", cfg.Port)
	}
	if cfg.Routing.DefaultModel != "gpt-5-mini" {
		t.Errorf("DefaultModel = %q, want gpt-5-mini", cfg.Routing.DefaultModel)
	}
	if cfg.Providers.Codex.Auth.Mode != "device_code" {
		t.Errorf("Codex auth mode = %q, want device_code", cfg.Providers.Codex.Auth.Mode)
	}
	if cfg.Timeouts.CircuitBreaker != 30 {
		t.Errorf("CircuitBreaker = %d, want 30", cfg.Timeouts.CircuitBreaker)
	}
}

func TestApplyConfigDefaults_PreservesExisting(t *testing.T) {
	cfg := &Config{
		Port: 9090,
		Routing: RoutingConfig{
			DefaultModel:    "gpt-4",
			DefaultProvider: "codex",
		},
	}
	cfg.Providers.Codex.Auth.Mode = "device_code"
	cfg.Timeouts.HTTPClient = 600

	applyConfigDefaults(cfg)

	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want 9090", cfg.Port)
	}
	if cfg.Routing.DefaultModel != "gpt-4" {
		t.Errorf("DefaultModel = %q, want gpt-4", cfg.Routing.DefaultModel)
	}
	if cfg.Routing.DefaultProvider != "codex" {
		t.Errorf("DefaultProvider = %q, want codex", cfg.Routing.DefaultProvider)
	}
	if cfg.Providers.Codex.Auth.Mode != "device_code" {
		t.Errorf("Codex auth mode = %q, want device_code", cfg.Providers.Codex.Auth.Mode)
	}
	if cfg.Timeouts.HTTPClient != 600 {
		t.Errorf("HTTPClient = %d, want 600", cfg.Timeouts.HTTPClient)
	}
}

func TestMergeConfigs(t *testing.T) {
	existing := &Config{
		Port:          8080,
		ConfigVersion: 1,
		Routing: RoutingConfig{
			DefaultModel:    "gpt-4",
			DefaultProvider: "copilot",
		},
		Providers: ProvidersConfig{
			Copilot: CopilotProviderConfig{
				Enabled: true,
				Auth: CopilotAuthState{
					Mode:         "device_code",
					GitHubToken:  "existing_github_token",
					CopilotToken: "existing_copilot_token",
					ExpiresAt:    1234567890,
					RefreshIn:    1500,
				},
				AllowedModels: []string{"gpt-4"},
			},
		},
	}
	existing.Timeouts.HTTPClient = 200

	defaults := &Config{
		Port:          7071,
		ConfigVersion: 1,
		Routing: RoutingConfig{
			DefaultModel:    "gpt-5-mini",
			DefaultProvider: "copilot",
		},
		Providers: ProvidersConfig{
			Copilot: CopilotProviderConfig{
				Enabled:       true,
				AllowedModels: []string{"gpt-4", "gpt-4.1", "gpt-5-mini"},
			},
		},
	}
	setDefaultTimeouts(defaults)

	tests := []struct {
		name     string
		mode     ConfigMigrationMode
		validate func(t *testing.T, result *Config)
	}{
		{
			name: "merge preserves existing non-zero values",
			mode: ConfigMigrationMerge,
			validate: func(t *testing.T, result *Config) {
				if result.Port != 8080 {
					t.Errorf("Port = %d, want 8080", result.Port)
				}
				if result.Routing.DefaultModel != "gpt-4" {
					t.Errorf("DefaultModel = %q, want gpt-4", result.Routing.DefaultModel)
				}
				if result.Providers.Copilot.Auth.GitHubToken != "existing_github_token" {
					t.Errorf("GitHubToken not preserved")
				}
				if result.Providers.Copilot.Auth.CopilotToken != "existing_copilot_token" {
					t.Errorf("CopilotToken not preserved")
				}
				if result.Timeouts.HTTPClient != 200 {
					t.Errorf("HTTPClient = %d, want 200", result.Timeouts.HTTPClient)
				}
				// Zero timeouts should be filled from defaults
				if result.Timeouts.ServerWrite != 300 {
					t.Errorf("ServerWrite = %d, want 300", result.Timeouts.ServerWrite)
				}
			},
		},
		{
			name: "override replaces everything",
			mode: ConfigMigrationOverride,
			validate: func(t *testing.T, result *Config) {
				if result.Port != 7071 {
					t.Errorf("Port = %d, want 7071", result.Port)
				}
				if result.Routing.DefaultModel != "gpt-5-mini" {
					t.Errorf("DefaultModel = %q, want gpt-5-mini", result.Routing.DefaultModel)
				}
			},
		},
		{
			name: "none returns existing unchanged",
			mode: ConfigMigrationNone,
			validate: func(t *testing.T, result *Config) {
				if result.Port != 8080 {
					t.Errorf("Port = %d, want 8080", result.Port)
				}
				if result.Routing.DefaultModel != "gpt-4" {
					t.Errorf("DefaultModel = %q, want gpt-4", result.Routing.DefaultModel)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeConfigs(existing, defaults, tt.mode)
			tt.validate(t, result)
		})
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	tempDir := t.TempDir()
	testConfigPath := filepath.Join(tempDir, "config.json")
	old := configPathOverride
	configPathOverride = testConfigPath
	defer func() { configPathOverride = old }()

	cfg := defaultConfig()
	cfg.Port = 9999
	cfg.Providers.Copilot.Auth.GitHubToken = "test-token"
	cfg.Providers.Copilot.Auth.CopilotToken = "copilot-tok"
	cfg.Providers.Codex.Enabled = true
	cfg.Providers.Codex.Auth.Mode = "device_code"

	if err := saveConfig(cfg); err != nil {
		t.Fatalf("saveConfig: %v", err)
	}
	info, err := os.Stat(testConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %o, want 600", got)
	}
	if leftovers, err := filepath.Glob(testConfigPath + ".tmp-*"); err != nil || len(leftovers) != 0 {
		t.Fatalf("temporary config files after save = %v (err=%v)", leftovers, err)
	}

	loaded, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if loaded.Port != 9999 {
		t.Errorf("Port = %d, want 9999", loaded.Port)
	}
	if len(loaded.Providers.Copilot.Accounts) == 0 || loaded.Providers.Copilot.Accounts[0].Auth.GitHubToken != "test-token" {
		t.Errorf("GitHubToken = %q, want test-token (in Accounts[0])", loaded.Providers.Copilot.Auth.GitHubToken)
	}
	if loaded.Providers.Codex.Auth.Mode != "device_code" {
		t.Errorf("Codex mode = %q, want device_code", loaded.Providers.Codex.Auth.Mode)
	}
}

func TestLoadConfig_V0Migration(t *testing.T) {
	tempDir := t.TempDir()
	testConfigPath := filepath.Join(tempDir, "config.json")
	old := configPathOverride
	configPathOverride = testConfigPath
	defer func() { configPathOverride = old }()

	// Write a V0 config
	v0 := map[string]interface{}{
		"port":           8080,
		"github_token":   "gh-tok",
		"copilot_token":  "cp-tok",
		"expires_at":     1234567890,
		"refresh_in":     1500,
		"allowed_models": []string{"gpt-4"},
		"default_model":  "gpt-4",
	}
	data, _ := json.Marshal(v0)
	if err := os.WriteFile(testConfigPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.ConfigVersion != 1 {
		t.Errorf("ConfigVersion = %d, want 1", cfg.ConfigVersion)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if len(cfg.Providers.Copilot.Accounts) == 0 || cfg.Providers.Copilot.Accounts[0].Auth.GitHubToken != "gh-tok" {
		t.Errorf("GitHubToken = %q, want gh-tok (in Accounts[0])", cfg.Providers.Copilot.Auth.GitHubToken)
	}
	if len(cfg.Providers.Copilot.Accounts) == 0 || cfg.Providers.Copilot.Accounts[0].Auth.CopilotToken != "cp-tok" {
		t.Errorf("CopilotToken = %q, want cp-tok (in Accounts[0])", cfg.Providers.Copilot.Auth.CopilotToken)
	}
	if cfg.Routing.DefaultModel != "gpt-4" {
		t.Errorf("DefaultModel = %q, want gpt-4", cfg.Routing.DefaultModel)
	}
	if !cfg.Providers.Copilot.Enabled {
		t.Error("Copilot should be enabled after V0 migration")
	}
}

func TestLoadConfig_MissingFile(t *testing.T) {
	tempDir := t.TempDir()
	testConfigPath := filepath.Join(tempDir, "nonexistent.json")
	old := configPathOverride
	configPathOverride = testConfigPath
	defer func() { configPathOverride = old }()

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Port != 7071 {
		t.Errorf("Port = %d, want 7071 (default)", cfg.Port)
	}
}

func TestLoadConfig_DoesNotMaskUnreadableState(t *testing.T) {
	old := configPathOverride
	configPathOverride = t.TempDir() // opening a directory is not a missing file
	defer func() { configPathOverride = old }()

	if _, err := loadConfig(); err == nil {
		t.Fatal("loadConfig returned defaults for unreadable state")
	}
}

func TestRuntimeCredentialPersistenceMergesLatestConfig(t *testing.T) {
	old := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	defer func() { configPathOverride = old }()

	latest := defaultConfig()
	latest.Providers.Copilot.Accounts = []CopilotAccount{{
		ID: "acct-copilot", Label: "primary", Auth: CopilotAuthState{GitHubToken: "github", CopilotToken: "old-copilot"},
	}}
	latest.Providers.Codex.Accounts = []CodexAccount{{
		ID: "acct-codex", Label: "primary", Auth: CodexAuthState{Mode: "chatgpt", AccessToken: "latest-codex"},
	}}
	latest.Routing.DefaultModel = "operator-model"
	if err := saveConfig(latest); err != nil {
		t.Fatal(err)
	}

	providerSnapshot := configschema.Clone(latest)
	providerSnapshot.Providers.Copilot.Accounts[0].Auth.CopilotToken = "refreshed-copilot"
	providerSnapshot.Providers.Codex.Accounts[0].Auth.AccessToken = "stale-codex"
	providerSnapshot.Routing.DefaultModel = "stale-model"
	if err := persistCopilotRuntimeAccount(providerSnapshot, 0); err != nil {
		t.Fatal(err)
	}

	loaded, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Providers.Copilot.Accounts[0].Auth.CopilotToken; got != "refreshed-copilot" {
		t.Fatalf("Copilot token = %q", got)
	}
	if got := loaded.Providers.Codex.Accounts[0].Auth.AccessToken; got != "latest-codex" {
		t.Fatalf("Codex token was overwritten: %q", got)
	}
	if got := loaded.Routing.DefaultModel; got != "operator-model" {
		t.Fatalf("routing config was overwritten: %q", got)
	}
}

func TestAccountIDsRemainStableAcrossNormalization(t *testing.T) {
	copilot := CopilotProviderConfig{Accounts: []CopilotAccount{{Label: "primary"}}}
	normalizeCopilotAccounts(&copilot)
	first := copilot.Accounts[0].ID
	normalizeCopilotAccounts(&copilot)
	if first == "" || copilot.Accounts[0].ID != first {
		t.Fatalf("Copilot account ID is not stable: %q -> %q", first, copilot.Accounts[0].ID)
	}

	codex := CodexProviderConfig{Accounts: []CodexAccount{{Label: "primary"}}}
	normalizeCodexAccounts(&codex)
	if codex.Accounts[0].ID == "" || codex.Accounts[0].ID == first {
		t.Fatalf("Codex account ID is invalid: %q", codex.Accounts[0].ID)
	}
}

func TestLoadDefaultConfigFromExample(t *testing.T) {
	tempDir := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}

	example := map[string]interface{}{
		"port":           7071,
		"config_version": 1,
		"routing": map[string]interface{}{
			"default_model":    "gpt-5-mini",
			"default_provider": "copilot",
		},
		"providers": map[string]interface{}{
			"copilot": map[string]interface{}{
				"enabled":        true,
				"allowed_models": []string{"gpt-4", "gpt-5-mini"},
			},
		},
	}

	data, _ := json.Marshal(example)
	if err := os.WriteFile("config.example.json", data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadDefaultConfigFromExample()
	if err != nil {
		t.Fatalf("loadDefaultConfigFromExample: %v", err)
	}

	if cfg.Port != 7071 {
		t.Errorf("Port = %d, want 7071", cfg.Port)
	}
	if cfg.Routing.DefaultModel != "gpt-5-mini" {
		t.Errorf("DefaultModel = %q, want gpt-5-mini", cfg.Routing.DefaultModel)
	}
}

func TestMigrateConfigIntegration(t *testing.T) {
	tempDir := t.TempDir()
	testConfigPath := filepath.Join(tempDir, "config.json")
	old := configPathOverride
	configPathOverride = testConfigPath
	defer func() { configPathOverride = old }()

	// Save a V1 config with custom settings
	cfg := defaultConfig()
	cfg.Port = 8080
	cfg.Providers.Copilot.Auth.GitHubToken = "test_github_token"
	cfg.Providers.Copilot.Auth.CopilotToken = "test_copilot_token"
	cfg.Routing.DefaultModel = "gpt-4"
	cfg.Providers.Copilot.AllowedModels = []string{"gpt-4"}
	if err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	// Create config.example.json
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}

	example := map[string]interface{}{
		"port":           7071,
		"config_version": 1,
		"routing": map[string]interface{}{
			"default_model": "gpt-5-mini",
		},
		"providers": map[string]interface{}{
			"copilot": map[string]interface{}{
				"enabled":        true,
				"allowed_models": []string{"gpt-4", "gpt-5-mini"},
			},
		},
	}
	data, _ := json.Marshal(example)
	if err := os.WriteFile("config.example.json", data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := migrateConfig(ConfigMigrationMerge); err != nil {
		t.Fatalf("migrateConfig: %v", err)
	}

	migrated, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}

	if migrated.Port != 8080 {
		t.Errorf("Port = %d, want 8080 (preserved)", migrated.Port)
	}
	if migrated.Routing.DefaultModel != "gpt-4" {
		t.Errorf("DefaultModel = %q, want gpt-4 (preserved)", migrated.Routing.DefaultModel)
	}
	if len(migrated.Providers.Copilot.Accounts) == 0 || migrated.Providers.Copilot.Accounts[0].Auth.GitHubToken != "test_github_token" {
		t.Errorf("GitHubToken not preserved in Accounts[0]")
	}
}
