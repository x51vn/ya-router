package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestModelExtractionAndPatchIntegration tests extractModelFromBody
// followed by patchBodyModel as a round-trip.
func TestModelExtractionAndPatchIntegration(t *testing.T) {
	original := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`

	model := extractModelFromBody([]byte(original))
	if model != "gpt-4" {
		t.Fatalf("extractModelFromBody = %q, want gpt-4", model)
	}

	patched := patchBodyModel([]byte(original), "gpt-5-mini")

	newModel := extractModelFromBody(patched)
	if newModel != "gpt-5-mini" {
		t.Errorf("after patch: model = %q, want gpt-5-mini", newModel)
	}

	// Verify messages are preserved
	if !strings.Contains(string(patched), `"content":"hi"`) {
		t.Errorf("message content lost after patch")
	}
}

// TestModelsEndpointConsistency tests the /v1/models endpoint returns
// models from providers with provider prefixes applied.
func TestModelsEndpointConsistency(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "gpt-4", Object: "model", OwnedBy: "openai"},
			{ID: "gpt-4.1", Object: "model", OwnedBy: "openai"},
			{ID: "gpt-5-mini", Object: "model", OwnedBy: "openai"},
		}},
	})

	cfg := defaultConfig()
	cfg.Providers.Copilot.AllowedModels = []string{
		"gpt-4", "gpt-4.1", "gpt-5-mini",
	}

	handler := modelsHandler(registry, cfg)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var ml ModelList
	if err := json.NewDecoder(rec.Body).Decode(&ml); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(ml.Data) == 0 {
		t.Fatal("expected at least one model, got 0")
	}

	ids := map[string]bool{}
	for _, m := range ml.Data {
		ids[m.ID] = true
	}

	// Models from Copilot are prefixed gc-.
	if !ids["gc-gpt-5-mini"] {
		t.Errorf("gc-gpt-5-mini not in response; got %v", ids)
	}
}

// TestModelsEndpointAggregatesMultipleProviders verifies the models
// endpoint merges models from copilot and codex providers.
func TestModelsEndpointAggregatesMultipleProviders(t *testing.T) {
	registry := NewProviderRegistry()

	copilotModels := []Model{
		{ID: "gpt-4", Object: "model", OwnedBy: "openai"},
	}
	codexModels := []Model{
		{ID: "o3-mini", Object: "model", OwnedBy: "openai"},
	}

	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: copilotModels},
	})
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: codexModels},
	})

	cfg := defaultConfig()
	handler := modelsHandler(registry, cfg)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var ml ModelList
	json.NewDecoder(rec.Body).Decode(&ml)

	if len(ml.Data) != 2 {
		t.Fatalf("expected 2 models (aggregated), got %d", len(ml.Data))
	}

	ids := map[string]bool{}
	for _, m := range ml.Data {
		ids[m.ID] = true
	}

	// Models are now prefixed: Copilot→gc-, Codex→oc-.
	if !ids["gc-gpt-4"] || !ids["oc-o3-mini"] {
		t.Errorf("expected both gc-gpt-4 and oc-o3-mini (prefixed), got %v", ids)
	}
}

// TestModelsEndpointEmptyWhenNoAuth verifies that the models endpoint
// returns an empty list when no providers are authenticated.
func TestModelsEndpointEmptyWhenNoAuth(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: false},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "gpt-4", Object: "model"},
		}},
	})

	cfg := defaultConfig()
	cfg.Routing.ShowUnavailableModels = false

	handler := modelsHandler(registry, cfg)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var ml ModelList
	json.NewDecoder(rec.Body).Decode(&ml)

	if len(ml.Data) != 0 {
		t.Errorf("expected empty models list, got %d models", len(ml.Data))
	}
}

// TestProviderRegistryIntegration tests the ProviderRegistry lifecycle.
func TestProviderRegistryIntegration(t *testing.T) {
	registry := NewProviderRegistry()

	if got := len(registry.All()); got != 0 {
		t.Fatalf("new registry has %d providers, want 0", got)
	}

	p1 := &mockProvider{id: ProviderCopilot, name: "Copilot"}
	p2 := &mockProvider{id: ProviderCodex, name: "Codex"}

	registry.Register(p1)
	registry.Register(p2)

	if got := len(registry.All()); got != 2 {
		t.Fatalf("registry has %d providers, want 2", got)
	}

	got, err := registry.Get(ProviderCopilot)
	if err != nil || got.ID() != ProviderCopilot {
		t.Errorf("Get(copilot) failed: %v", err)
	}

	_, err = registry.Get("nonexistent")
	if err == nil {
		t.Errorf("Get(nonexistent) should return error")
	}
}

// TestModelRouterResolve tests the model router resolution logic.
func TestModelRouterResolve(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityChat, CapabilityEmbeddings},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "gpt-4", Object: "model"},
			{ID: "gpt-5-mini", Object: "model"},
		}},
	})
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "o3-mini", Object: "model"},
		}},
	})

	cfg := defaultConfig()
	cfg.Routing.DefaultProvider = string(ProviderCopilot)
	cfg.Routing.ModelMap = map[string]ModelMapEntry{
		"o3-mini": {Provider: "codex"},
	}

	router := NewModelRouter(registry, cfg.Routing)

	tests := []struct {
		name    string
		model   string
		cap     Capability
		wantPID ProviderID
		wantErr bool
	}{
		{
			name:    "explicit model_map hit",
			model:   "o3-mini",
			cap:     CapabilityChat,
			wantPID: ProviderCodex,
		},
		{
			name:    "default provider fallback",
			model:   "gpt-4",
			cap:     CapabilityChat,
			wantPID: ProviderCopilot,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := router.Resolve(context.Background(), tt.model, tt.cap)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Resolve(%q, %q) error = %v, wantErr %v",
					tt.model, tt.cap, err, tt.wantErr)
			}
			if err == nil && result.Provider.ID() != tt.wantPID {
				t.Errorf("Resolve(%q, %q) = %q, want %q",
					tt.model, tt.cap, result.Provider.ID(), tt.wantPID)
			}
		})
	}
}

// TestConfigDefaultAuth verifies default auth config consistency.
func TestConfigDefaultAuth(t *testing.T) {
	cfg := defaultConfig()

	if cfg.Providers.Copilot.Auth.Mode != "device_code" {
		t.Errorf("Copilot auth mode = %q, want device_code",
			cfg.Providers.Copilot.Auth.Mode)
	}
	if cfg.Providers.Codex.Auth.Mode != "device_code" {
		t.Errorf("Codex auth mode = %q, want device_code",
			cfg.Providers.Codex.Auth.Mode)
	}
}

// TestIsChatGPTModeBackwardCompat verifies backward compatibility
// with the old chatgpt_device_auth and device_code mode names.
func TestIsChatGPTModeBackwardCompat(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{"device_code", true},
		{"chatgpt_device_auth", true},
		{"chatgpt", true},
		{"api_key", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			if got := isChatGPTMode(tt.mode); got != tt.want {
				t.Errorf("isChatGPTMode(%q) = %v, want %v",
					tt.mode, got, tt.want)
			}
		})
	}
}

func TestResolveCodexAPIKeyPrefersEnvOverConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")
	key, source, err := resolveCodexAPIKey(&CodexAuthState{APIKey: "config-key"})
	if err != nil {
		t.Fatalf("resolveCodexAPIKey error = %v", err)
	}
	if key != "env-key" {
		t.Fatalf("key = %q, want env-key", key)
	}
	if source != codexCredentialSourceEnv {
		t.Fatalf("source = %q, want %q", source, codexCredentialSourceEnv)
	}
}

func TestResolveCodexChatGPTAuthPrefersOfficialStore(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	oldHome := os.Getenv("CODEX_HOME")
	tempDir := t.TempDir()
	if err := os.Setenv("CODEX_HOME", tempDir); err != nil {
		t.Fatalf("set CODEX_HOME: %v", err)
	}
	defer os.Setenv("CODEX_HOME", oldHome)

	path := filepath.Join(tempDir, "auth.json")
	data := `{"tokens":{"access_token":"official-token","refresh_token":"official-refresh","account_id":"acct-123","expires_at":789}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	resolved, err := resolveCodexChatGPTAuth(&CodexAuthState{AccessToken: "config-token", RefreshToken: "config-refresh", AccountID: "config-acct", ExpiresAt: 123})
	if err != nil {
		t.Fatalf("resolveCodexChatGPTAuth error = %v", err)
	}
	if resolved == nil {
		t.Fatal("expected resolved auth")
	}
	if resolved.AccessToken != "official-token" || resolved.RefreshToken != "official-refresh" {
		t.Fatalf("resolved = %+v, want official tokens", resolved)
	}
	if resolved.AccountID != "acct-123" {
		t.Fatalf("accountID = %q, want acct-123", resolved.AccountID)
	}
	if resolved.Source != codexCredentialSourceOfficialStore {
		t.Fatalf("source = %q, want %q", resolved.Source, codexCredentialSourceOfficialStore)
	}
	if resolved.ExpiresAt != 789 {
		t.Fatalf("expiresAt = %d, want 789", resolved.ExpiresAt)
	}
}

func TestResolveCodexChatGPTAuthFallsBackToConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	oldHome := os.Getenv("CODEX_HOME")
	tempDir := t.TempDir()
	if err := os.Setenv("CODEX_HOME", tempDir); err != nil {
		t.Fatalf("set CODEX_HOME: %v", err)
	}
	defer os.Setenv("CODEX_HOME", oldHome)

	resolved, err := resolveCodexChatGPTAuth(&CodexAuthState{AccessToken: "config-token", RefreshToken: "config-refresh", AccountID: "config-acct", ExpiresAt: 456})
	if err != nil {
		t.Fatalf("resolveCodexChatGPTAuth error = %v", err)
	}
	if resolved == nil {
		t.Fatal("expected resolved auth")
	}
	if resolved.AccessToken != "config-token" || resolved.RefreshToken != "config-refresh" {
		t.Fatalf("resolved = %+v, want config tokens", resolved)
	}
	if resolved.Source != codexCredentialSourceProxyConfig {
		t.Fatalf("source = %q, want %q", resolved.Source, codexCredentialSourceProxyConfig)
	}
}

func TestModelRouterResolve_AmbiguousModelRequiresRule(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "shared-model", Object: "model"}}},
	})
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "shared-model", Object: "model"}}},
	})

	router := NewModelRouter(registry, defaultConfig().Routing)
	_, err := router.Resolve(context.Background(), "shared-model", CapabilityChat)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}

func TestModelRouterResolve_ExplicitModelNeedsMatchingCapability(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityEmbeddings},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "embed-ok", Object: "model"}}},
	})
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "chat-only", Object: "model"}}},
	})

	router := NewModelRouter(registry, defaultConfig().Routing)
	_, err := router.Resolve(context.Background(), "chat-only", CapabilityEmbeddings)
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected unavailable error, got %v", err)
	}
}

func TestModelsEndpointKeepsModelMapVisibleWhenProviderUnavailable(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: false},
		models: &ModelList{Object: "list", Data: []Model{}},
	})

	cfg := defaultConfig()
	cfg.Routing.ModelMap = map[string]ModelMapEntry{
		"gpt-5.4": {Provider: string(ProviderCodex)},
	}
	handler := modelsHandler(registry, cfg)

	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var ml ModelList
	if err := json.NewDecoder(rec.Body).Decode(&ml); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(ml.Data) != 1 || ml.Data[0].ID != "gpt-5.4" {
		t.Fatalf("expected model_map fallback entry, got %+v", ml.Data)
	}
}

// TestHealthHandler verifies the /healthz endpoint.
func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()

	healthHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp)

	if resp["status"] != "ok" {
		t.Errorf("health status = %q, want ok", resp["status"])
	}
}

func TestNormalizeCopilotAccounts_PromotesLegacyAuth(t *testing.T) {
	cfg := &CopilotProviderConfig{
		Auth: CopilotAuthState{GitHubToken: "tok123", Mode: "device_code"},
	}
	normalizeCopilotAccounts(cfg)
	if len(cfg.Accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(cfg.Accounts))
	}
	if cfg.Accounts[0].Label != "primary" {
		t.Errorf("label = %q, want primary", cfg.Accounts[0].Label)
	}
	if cfg.Accounts[0].Auth.GitHubToken != "tok123" {
		t.Errorf("GitHub token not promoted to account")
	}
	if cfg.Auth.GitHubToken != "" {
		t.Errorf("legacy Auth should be cleared after promotion")
	}
}

func TestNormalizeCopilotAccounts_SkipsWhenAccountsPresent(t *testing.T) {
	cfg := &CopilotProviderConfig{
		Auth: CopilotAuthState{GitHubToken: "legacy"},
		Accounts: []CopilotAccount{
			{Label: "work", Auth: CopilotAuthState{GitHubToken: "work-tok"}},
		},
	}
	normalizeCopilotAccounts(cfg)
	if len(cfg.Accounts) != 1 {
		t.Errorf("expected accounts unchanged, got %d", len(cfg.Accounts))
	}
	if cfg.Accounts[0].Label != "work" {
		t.Errorf("account label changed unexpectedly")
	}
}

func TestAdvanceAccount_AdvancesToNextHealthyAccount(t *testing.T) {
	cfg := buildTestConfigWithAccounts(300, 0, 0)
	p := &CopilotProvider{Cfg: cfg}

	advanced := p.AdvanceAccount()
	if !advanced {
		t.Fatal("expected advance to succeed with 2 accounts")
	}
	if p.AccountCursor != 1 {
		t.Errorf("cursor = %d, want 1", p.AccountCursor)
	}
	if cfg.Providers.Copilot.Accounts[0].LastLimitedAt == 0 {
		t.Error("account 0 LastLimitedAt should be set after advance")
	}
}

func TestAdvanceAccount_ReturnsFalseOnSingleAccount(t *testing.T) {
	cfg := buildTestConfigWithAccounts(300, 0)
	p := &CopilotProvider{Cfg: cfg}
	if p.AdvanceAccount() {
		t.Error("expected false for single-account pool")
	}
}

func TestAdvanceAccount_SkipsCooldownAccounts(t *testing.T) {
	nowish := time.Now().Unix()
	cfg := buildTestConfigWithAccounts(300, 0, nowish)
	accounts := cfg.Providers.Copilot.Accounts
	accounts[1].LastLimitedAt = nowish
	cfg.Providers.Copilot.Accounts = accounts

	p := &CopilotProvider{Cfg: cfg}
	advanced := p.AdvanceAccount()
	if advanced {
		t.Error("both accounts in cooldown; expected no advance")
	}
}

func TestIsAccountLimitSignal_429(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       http.NoBody,
	}
	ok, _ := isAccountLimitSignal(resp)
	if !ok {
		t.Error("429 should be an account limit signal")
	}
}

func TestIsAccountLimitSignal_403WithRateLimit(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.WriteString(`{"error":"rate limit exceeded"}`)
	resp := rec.Result()
	resp.StatusCode = http.StatusForbidden

	ok, preview := isAccountLimitSignal(resp)
	if !ok {
		t.Error("403 with 'rate limit' body should be account limit signal")
	}
	if !strings.Contains(preview, "rate limit") {
		t.Errorf("expected preview to contain 'rate limit', got %q", preview)
	}
}

func TestIsAccountLimitSignal_403WithoutRateLimit(t *testing.T) {
	rec := httptest.NewRecorder()
	rec.WriteString(`{"error":"forbidden"}`)
	resp := rec.Result()
	resp.StatusCode = http.StatusForbidden

	ok, _ := isAccountLimitSignal(resp)
	if ok {
		t.Error("403 without rate-limit body should NOT be an account limit signal")
	}
}

func TestFirstHealthyAccount_SkipsCooldown(t *testing.T) {
	nowish := time.Now().Unix()
	cfg := buildTestConfigWithAccounts(300, nowish, 0)
	p := &CopilotProvider{Cfg: cfg}
	idx := p.FirstHealthyAccount()
	if idx != 1 {
		t.Errorf("expected account 1 (healthy), got %d", idx)
	}
}

func buildTestConfigWithAccounts(cooldownSecs int, lastLimitedAts ...int64) *Config {
	cfg := defaultConfig()
	cfg.Providers.Copilot.AccountCooldownSeconds = cooldownSecs
	accounts := make([]CopilotAccount, len(lastLimitedAts))
	for i, ts := range lastLimitedAts {
		accounts[i] = CopilotAccount{
			Label:         fmt.Sprintf("acct-%d", i),
			Auth:          CopilotAuthState{GitHubToken: fmt.Sprintf("tok-%d", i), Mode: "device_code"},
			LastLimitedAt: ts,
		}
	}
	cfg.Providers.Copilot.Accounts = accounts
	return cfg
}

func TestProxyFreeChatRequest_AccountFailover(t *testing.T) {
	cfg := buildTestConfigWithAccounts(300, 0, 0)

	p := NewCopilotProvider(cfg)
	p.FreeModelResolver = func(_ context.Context) ([]Model, error) {
		return []Model{{ID: "gpt-test"}}, nil
	}

	callCount := 0
	p.ProxyExecutor = func(_ context.Context, _ *http.Request, _ []byte, _ Capability) (*http.Response, string, error) {
		acc := p.ActiveAccount()
		callCount++
		if acc != nil && acc.Label == "acct-0" {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusTooManyRequests)
			rec.WriteString(`{"error":"rate limited"}`)
			return rec.Result(), "", nil
		}
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		rec.WriteString(`{"choices":[]}`)
		return rec.Result(), "", nil
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	err := p.ProxyFreeChatRequest(context.Background(), w, req, []byte(`{}`), "gpt-test")
	if err != nil {
		t.Fatalf("expected no error after failover, got: %v", err)
	}
	if p.AccountCursor != 1 {
		t.Errorf("expected cursor=1 after failover, got %d", p.AccountCursor)
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 proxy calls (one per account), got %d", callCount)
	}
}

func TestProxyFreeChatRequest_AllAccountsExhausted(t *testing.T) {
	nowish := time.Now().Unix()
	cfg := buildTestConfigWithAccounts(300, 0, nowish)

	p := NewCopilotProvider(cfg)
	p.FreeModelResolver = func(_ context.Context) ([]Model, error) {
		return []Model{{ID: "gpt-test"}}, nil
	}

	p.ProxyExecutor = func(_ context.Context, _ *http.Request, _ []byte, _ Capability) (*http.Response, string, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusTooManyRequests)
		rec.WriteString(`{"error":"rate limited"}`)
		return rec.Result(), "", nil
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	err := p.ProxyFreeChatRequest(context.Background(), w, req, []byte(`{}`), "gpt-test")
	if err != nil {
		t.Errorf("expected no error (should forward last 429), got: %v", err)
	}
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected forwarded 429, got %d", w.Code)
	}
}

func buildTestCodexConfigWithAccounts(cooldownSecs int, modes []string, lastLimitedAts ...int64) *Config {
	cfg := defaultConfig()
	cfg.Providers.Codex.Enabled = true
	cfg.Providers.Codex.AccountCooldownSeconds = cooldownSecs
	accounts := make([]CodexAccount, len(lastLimitedAts))
	for i, ts := range lastLimitedAts {
		mode := "chatgpt"
		if i < len(modes) {
			mode = modes[i]
		}
		accounts[i] = CodexAccount{
			Label:         fmt.Sprintf("codex-acct-%d", i),
			Auth:          CodexAuthState{Mode: mode, AccessToken: fmt.Sprintf("tok-%d", i)},
			LastLimitedAt: ts,
		}
	}
	cfg.Providers.Codex.Accounts = accounts
	return cfg
}

func TestNormalizeCodexAccounts_PromotesLegacyAuth(t *testing.T) {
	cfg := defaultConfig()
	cfg.Providers.Codex.Auth = CodexAuthState{Mode: "chatgpt", AccessToken: "legacy-tok"}
	normalizeCodexAccounts(&cfg.Providers.Codex)
	if len(cfg.Providers.Codex.Accounts) != 1 {
		t.Fatalf("expected 1 account after promotion, got %d", len(cfg.Providers.Codex.Accounts))
	}
	got := cfg.Providers.Codex.Accounts[0]
	if got.Label != "primary" {
		t.Errorf("expected label 'primary', got %q", got.Label)
	}
	if got.Auth.AccessToken != "legacy-tok" {
		t.Errorf("expected promoted token, got %q", got.Auth.AccessToken)
	}
	if cfg.Providers.Codex.Auth.Mode != "" {
		t.Errorf("expected top-level Auth zeroed after promotion, got mode=%q", cfg.Providers.Codex.Auth.Mode)
	}
}

func TestNormalizeCodexAccounts_SkipsWhenAccountsPresent(t *testing.T) {
	cfg := buildTestCodexConfigWithAccounts(300, []string{"chatgpt"}, 0)
	before := len(cfg.Providers.Codex.Accounts)
	cfg.Providers.Codex.Auth = CodexAuthState{Mode: "chatgpt", AccessToken: "should-not-promote"}
	normalizeCodexAccounts(&cfg.Providers.Codex)
	if len(cfg.Providers.Codex.Accounts) != before {
		t.Errorf("expected %d accounts (no promotion), got %d", before, len(cfg.Providers.Codex.Accounts))
	}
}

func TestAdvanceCodexAccount_AdvancesToNextHealthyAccount(t *testing.T) {
	cfg := buildTestCodexConfigWithAccounts(300, []string{"chatgpt", "chatgpt"}, 0, 0)
	p := NewCodexProvider(cfg)
	p.AccountCursor = 0
	advanced := p.AdvanceCodexAccount()
	if !advanced {
		t.Error("expected advanceCodexAccount to return true")
	}
	if p.AccountCursor != 1 {
		t.Errorf("expected cursor=1, got %d", p.AccountCursor)
	}
	if cfg.Providers.Codex.Accounts[0].LastLimitedAt == 0 {
		t.Error("expected account 0 to have LastLimitedAt set")
	}
}

func TestAdvanceCodexAccount_ReturnsFalseOnSingleAccount(t *testing.T) {
	cfg := buildTestCodexConfigWithAccounts(300, []string{"chatgpt"}, 0)
	p := NewCodexProvider(cfg)
	if p.AdvanceCodexAccount() {
		t.Error("expected false for single-account pool")
	}
}

func TestIsCodexAccountInCooldown(t *testing.T) {
	nowish := time.Now().Unix()
	cfg := defaultConfig()
	cfg.Providers.Codex.Enabled = true
	cfg.Providers.Codex.AccountCooldownSeconds = 300
	p := NewCodexProvider(cfg)

	recent := &CodexAccount{LastLimitedAt: nowish}
	if !p.IsCodexAccountInCooldown(recent) {
		t.Error("recently limited account should be in cooldown")
	}

	old := &CodexAccount{LastLimitedAt: nowish - 400}
	if p.IsCodexAccountInCooldown(old) {
		t.Error("old limited account should NOT be in cooldown")
	}

	zero := &CodexAccount{}
	if p.IsCodexAccountInCooldown(zero) {
		t.Error("never-limited account should NOT be in cooldown")
	}
}

func TestFirstHealthyCodexAccount_SkipsCooldown(t *testing.T) {
	nowish := time.Now().Unix()
	cfg := buildTestCodexConfigWithAccounts(300, []string{"chatgpt", "chatgpt"}, nowish, 0)
	p := NewCodexProvider(cfg)
	idx := p.FirstHealthyCodexAccount()
	if idx != 1 {
		t.Errorf("expected account 1 (healthy), got %d", idx)
	}
}

func TestProxyCodexRequest_AccountFailover(t *testing.T) {
	cfg := buildTestCodexConfigWithAccounts(300, []string{"chatgpt", "chatgpt"}, 0, 0)
	p := NewCodexProvider(cfg)
	p.AccountCursor = 0

	callCount := 0
	p.ProxyExecutor = func(_ context.Context, _ *http.Request, _ []byte, _ Capability) (*http.Response, string, error) {
		acc := p.ActiveAccount()
		callCount++
		if acc != nil && acc.Label == "codex-acct-0" {
			rec := httptest.NewRecorder()
			rec.WriteHeader(http.StatusTooManyRequests)
			rec.WriteString(`{"error":"rate limited"}`)
			return rec.Result(), "", nil
		}
		sseBody := "event: response.completed\ndata: {\"response\":{\"id\":\"r1\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hi\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		rec.WriteString(sseBody)
		return rec.Result(), "", nil
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	err := p.ProxyRequest(context.Background(), w, req, []byte(`{"model":"oc-gpt-5","messages":[]}`), CapabilityChat)
	if err != nil {
		t.Fatalf("expected no error after failover, got: %v", err)
	}
	if p.AccountCursor != 1 {
		t.Errorf("expected cursor=1 after failover, got %d", p.AccountCursor)
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 proxy calls (one per account), got %d", callCount)
	}
}

func TestProxyCodexRequest_AllAccountsExhausted(t *testing.T) {
	nowish := time.Now().Unix()
	cfg := buildTestCodexConfigWithAccounts(300, []string{"chatgpt", "chatgpt"}, 0, nowish)
	p := NewCodexProvider(cfg)
	p.AccountCursor = 0

	p.ProxyExecutor = func(_ context.Context, _ *http.Request, _ []byte, _ Capability) (*http.Response, string, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusTooManyRequests)
		rec.WriteString(`{"error":"rate limited"}`)
		return rec.Result(), "", nil
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	err := p.ProxyRequest(context.Background(), w, req, []byte(`{"model":"oc-gpt-5","messages":[]}`), CapabilityChat)
	if err != nil {
		t.Errorf("expected no error (should forward last 429), got: %v", err)
	}
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected forwarded 429, got %d", w.Code)
	}
}
