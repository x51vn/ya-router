package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadOfficialCodexModelsReadsCache(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	oldHome := os.Getenv("CODEX_HOME")
	tempDir := t.TempDir()
	if err := os.Setenv("CODEX_HOME", tempDir); err != nil {
		t.Fatalf("set CODEX_HOME: %v", err)
	}
	defer os.Setenv("CODEX_HOME", oldHome)

	path := filepath.Join(tempDir, "models_cache.json")
	data := `{
	  "models": [
	    {"slug": "gpt-5.5", "supported_in_api": true, "visibility": "public"},
	    {"slug": "gpt-5.4-mini", "supported_in_api": true, "visibility": "public"},
	    {"slug": "hidden-model", "supported_in_api": true, "visibility": "hide"}
	  ]
	}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write models_cache.json: %v", err)
	}

	models, err := loadOfficialCodexModels()
	if err != nil {
		t.Fatalf("loadOfficialCodexModels error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("loadOfficialCodexModels() returned %d models, want 2", len(models))
	}
	if models[0].ID != "gpt-5.5" || models[1].ID != "gpt-5.4-mini" {
		t.Fatalf("loadOfficialCodexModels() = %+v, want gpt-5.5 and gpt-5.4-mini", models)
	}
}

func TestCodexKnownModelListIncludesLatestModels(t *testing.T) {
	p := &CodexProvider{Cfg: defaultConfig()}

	ml := p.KnownModelList()
	ids := make(map[string]bool, len(ml.Data))
	for _, m := range ml.Data {
		ids[m.ID] = true
	}

	for _, want := range []string{"gpt-5.5", "gpt-5.4-mini"} {
		if !ids[want] {
			t.Fatalf("knownModelList() missing %q, got IDs: %v", want, ids)
		}
	}
}

func TestCodexProviderAuthBrokenReturns503(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("CODEX_HOME", emptyDir)

	p := &CodexProvider{
		Cfg:        defaultConfig(),
		AuthBroken: true,
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err := p.ProxyRequest(context.Background(), w, req, []byte(`{}`), CapabilityChat)
	if err != nil {
		t.Fatalf("ProxyRequest returned unexpected error: %v", err)
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Error.Code != "provider_auth_broken" {
		t.Errorf("error.code = %q, want %q", body.Error.Code, "provider_auth_broken")
	}
}

func TestCodexProviderAuthBrokenRecovery(t *testing.T) {
	dir := t.TempDir()
	old := os.Getenv("CODEX_HOME")
	os.Setenv("CODEX_HOME", dir)
	defer os.Setenv("CODEX_HOME", old)

	accountID := "test-account-id"
	freshToken := "fresh.access.token.after.reauth"
	authData := officialCodexAuthJSON{
		Tokens: &officialTokenData{
			AccessToken:  freshToken,
			RefreshToken: "fresh-refresh",
			ExpiresAt:    time.Now().Unix() + 3600,
			AccountID:    &accountID,
		},
	}
	b, _ := json.Marshal(authData)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), b, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}

	cfg := defaultConfig()
	cfg.Providers.Codex.Auth.Mode = "device_code"
	p := &CodexProvider{
		Cfg:        cfg,
		AuthBroken: true,
		Cb:         newCircuitBreakerWithState(CircuitClosed, 30),
		Cache:      NewModelCache(DefaultModelCacheTTL),
	}
	p.ProxyExecutor = func(_ context.Context, _ *http.Request, _ []byte, _ Capability) (*http.Response, string, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusOK)
		rec.Body.WriteString(`{"id":"chatcmpl-test","choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
		return rec.Result(), "test-provider", nil
	}

	body := []byte(`{"model":"gpt-oss-120b","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", io.NopCloser(strings.NewReader(string(body))))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := p.ProxyRequest(context.Background(), w, req, body, CapabilityChat)
	if w.Code == http.StatusServiceUnavailable {
		t.Errorf("got 503, want non-503 after recovery; body: %s", w.Body.String())
	}
	if err != nil && strings.Contains(err.Error(), "provider_auth_broken") {
		t.Errorf("got auth_broken error after recovery: %v", err)
	}

	p.Mu.Lock()
	stillBroken := p.AuthBroken
	p.Mu.Unlock()
	if stillBroken {
		t.Errorf("authBroken should be false after recovery, still true")
	}
}

func TestProxyRequestRetryOn401_RefreshSucceeds(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"refreshed-token","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	oldIssuer := codexAuthIssuer
	oldDelay := baseRetryDelay
	codexAuthIssuer = tokenSrv.URL
	baseRetryDelay = 0
	defer func() {
		codexAuthIssuer = oldIssuer
		baseRetryDelay = oldDelay
	}()

	emptyDir := t.TempDir()
	t.Setenv("CODEX_HOME", emptyDir)

	callCount := 0
	cfg := defaultConfig()
	cfg.Providers.Codex.Auth.Mode = "device_code"
	cfg.Providers.Codex.Auth.AccessToken = "initial-token"
	cfg.Providers.Codex.Auth.RefreshToken = "test-refresh"
	cfg.Providers.Codex.Auth.ExpiresAt = time.Now().Unix() + 3600
	cfg.Providers.Codex.Auth.AccountID = "acc-123"

	p := &CodexProvider{
		Cfg:   cfg,
		Cb:    newCircuitBreakerWithState(CircuitClosed, 30),
		Cache: NewModelCache(DefaultModelCacheTTL),
	}
	p.RefreshExecutor = codexRefreshToken
	p.ProxyExecutor = func(_ context.Context, _ *http.Request, _ []byte, _ Capability) (*http.Response, string, error) {
		callCount++
		rec := httptest.NewRecorder()
		if callCount == 1 {
			rec.WriteHeader(http.StatusUnauthorized)
			rec.Body.WriteString(`{"error":{"message":"Could not parse your authentication token","code":"unauthorized_unknown"}}`)
		} else {
			rec.Header().Set("Content-Type", "text/event-stream")
			rec.WriteHeader(http.StatusOK)
			rec.Body.WriteString("event: response.completed\ndata: {\"id\":\"resp-ok\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":5,\"output_tokens\":3}}\n\n")
		}
		return rec.Result(), "codex", nil
	}

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err := p.ProxyRequest(context.Background(), w, req, body, CapabilityChat)
	if err != nil {
		t.Fatalf("ProxyRequest error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 upstream calls (1 fail + 1 retry), got %d", callCount)
	}
	if w.Code == http.StatusUnauthorized {
		t.Errorf("expected non-401 response after successful retry, got %d", w.Code)
	}
}

func TestProxyRequestRetryOn401_RefreshFails(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"refresh token reused","type":"invalid_request_error","code":"refresh_token_reused"}}`))
	}))
	defer tokenSrv.Close()

	oldIssuer := codexAuthIssuer
	oldDelay := baseRetryDelay
	codexAuthIssuer = tokenSrv.URL
	baseRetryDelay = 0
	defer func() {
		codexAuthIssuer = oldIssuer
		baseRetryDelay = oldDelay
	}()

	emptyDir := t.TempDir()
	t.Setenv("CODEX_HOME", emptyDir)

	cfg := defaultConfig()
	cfg.Providers.Codex.Auth.Mode = "device_code"
	cfg.Providers.Codex.Auth.AccessToken = "initial-token"
	cfg.Providers.Codex.Auth.RefreshToken = "test-refresh"
	cfg.Providers.Codex.Auth.ExpiresAt = time.Now().Unix() + 3600
	cfg.Providers.Codex.Auth.AccountID = "acc-123"

	p := &CodexProvider{
		Cfg:   cfg,
		Cb:    newCircuitBreakerWithState(CircuitClosed, 30),
		Cache: NewModelCache(DefaultModelCacheTTL),
	}
	p.RefreshExecutor = codexRefreshToken
	p.ProxyExecutor = func(_ context.Context, _ *http.Request, _ []byte, _ Capability) (*http.Response, string, error) {
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusUnauthorized)
		rec.Body.WriteString(`{"error":{"message":"unauthorized","code":"unauthorized_unknown"}}`)
		return rec.Result(), "codex", nil
	}

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err := p.ProxyRequest(context.Background(), w, req, body, CapabilityChat)
	if err != nil {
		t.Fatalf("ProxyRequest error: %v", err)
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when refresh fails with unrecoverable, got %d; body: %s", w.Code, w.Body.String())
	}
	var respBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &respBody); err == nil {
		if respBody.Error.Code != "provider_auth_broken" {
			t.Errorf("error.code = %q, want %q", respBody.Error.Code, "provider_auth_broken")
		}
	}
}

func TestProxyRequestRetryOn401_RetryAlso401(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"refreshed-token","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	oldIssuer := codexAuthIssuer
	oldDelay := baseRetryDelay
	codexAuthIssuer = tokenSrv.URL
	baseRetryDelay = 0
	defer func() {
		codexAuthIssuer = oldIssuer
		baseRetryDelay = oldDelay
	}()

	emptyDir := t.TempDir()
	t.Setenv("CODEX_HOME", emptyDir)

	callCount := 0
	cfg := defaultConfig()
	cfg.Providers.Codex.Auth.Mode = "device_code"
	cfg.Providers.Codex.Auth.AccessToken = "initial-token"
	cfg.Providers.Codex.Auth.RefreshToken = "test-refresh"
	cfg.Providers.Codex.Auth.ExpiresAt = time.Now().Unix() + 3600
	cfg.Providers.Codex.Auth.AccountID = "acc-123"

	p := &CodexProvider{
		Cfg:   cfg,
		Cb:    newCircuitBreakerWithState(CircuitClosed, 30),
		Cache: NewModelCache(DefaultModelCacheTTL),
	}
	p.RefreshExecutor = codexRefreshToken
	p.ProxyExecutor = func(_ context.Context, _ *http.Request, _ []byte, _ Capability) (*http.Response, string, error) {
		callCount++
		rec := httptest.NewRecorder()
		rec.WriteHeader(http.StatusUnauthorized)
		rec.Body.WriteString(`{"error":{"message":"unauthorized","code":"unauthorized_unknown"}}`)
		return rec.Result(), "codex", nil
	}

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	err := p.ProxyRequest(context.Background(), w, req, body, CapabilityChat)
	if err != nil && !strings.Contains(err.Error(), "upstream HTTP 401") {
		t.Fatalf("ProxyRequest unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 upstream calls (initial + 1 retry), got %d", callCount)
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 passthrough after retry also fails, got %d", w.Code)
	}

	p.Mu.Lock()
	broken := p.AuthBroken
	p.Mu.Unlock()
	if broken {
		t.Errorf("authBroken should NOT be set when retry 401 — only on unrecoverable refresh")
	}
}
