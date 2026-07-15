package yarouter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type hardeningProvider struct {
	id         ProviderID
	caps       []Capability
	models     []Model
	proxyCalls int
}

func (p *hardeningProvider) ID() ProviderID                            { return p.id }
func (p *hardeningProvider) Name() string                              { return string(p.id) }
func (p *hardeningProvider) Capabilities() []Capability                { return p.caps }
func (p *hardeningProvider) EnsureAuthenticated(context.Context) error { return nil }
func (p *hardeningProvider) ListModels(context.Context) (*ModelList, error) {
	return &ModelList{Object: "list", Data: p.models}, nil
}
func (p *hardeningProvider) ProxyRequest(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
	p.proxyCalls++
	w.WriteHeader(http.StatusOK)
	return nil
}
func (p *hardeningProvider) Health(context.Context) ProviderHealth {
	return ProviderHealth{Authenticated: true}
}

func TestProcessProxyRequestHonorsProviderPrefix(t *testing.T) {
	registry := NewProviderRegistry()
	copilot := &hardeningProvider{
		id: ProviderCopilot, caps: []Capability{CapabilityChat},
		models: []Model{{ID: "gpt-test", Object: "model"}},
	}
	codex := &hardeningProvider{
		id: ProviderCodex, caps: []Capability{CapabilityChat},
		models: []Model{{ID: "gpt-test", Object: "model"}},
	}
	registry.Register(copilot)
	registry.Register(codex)
	router := NewModelRouter(registry, defaultConfig().Routing)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"codex/gpt-test","messages":[]}`))
	response := httptest.NewRecorder()
	if err := processProxyRequest(registry, router, defaultConfig(), response, request, context.Background()); err != nil {
		t.Fatalf("processProxyRequest: %v", err)
	}
	if codex.proxyCalls != 1 || copilot.proxyCalls != 0 {
		t.Fatalf("codex proxy calls=%d; copilot proxy calls=%d", codex.proxyCalls, copilot.proxyCalls)
	}
}

func TestConfiguredListenAddressDefaultsToLoopback(t *testing.T) {
	t.Setenv(listenAddressEnv, "")
	t.Setenv(inboundAPIKeyEnv, "")
	address, err := configuredListenAddress(7071)
	if err != nil || address != "127.0.0.1:7071" {
		t.Fatalf("address=%q err=%v", address, err)
	}
}

func TestConfiguredListenAddressRequiresCredentialForPublicBind(t *testing.T) {
	t.Setenv(listenAddressEnv, "0.0.0.0")
	t.Setenv(inboundAPIKeyEnv, "")
	if _, err := configuredListenAddress(7071); err == nil {
		t.Fatal("expected public bind without API key to fail")
	}
}

func TestSecureHandlerRejectsInvalidCredential(t *testing.T) {
	t.Setenv(inboundAPIKeyEnv, "secret")
	handler := secureHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestStructuredOutputIsTranslatedToResponsesTextFormat(t *testing.T) {
	request := []byte(`{
		"model":"gpt-test",
		"messages":[{"role":"user","content":"hi"}],
		"response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}}
	}`)
	body, _, err := buildChatGPTCodexRequest(request)
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	textValue, ok := decoded["text"]
	if !ok {
		t.Fatalf("structured output was dropped: %s", body)
	}
	if !strings.Contains(string(textValue), `"json_schema"`) || !strings.Contains(string(textValue), `"answer"`) {
		t.Fatalf("unexpected text.format: %s", textValue)
	}
}

func TestCodexCapabilitiesDependOnAuthMode(t *testing.T) {
	cfg := defaultConfig()
	cfg.Providers.Codex.Auth = CodexAuthState{Mode: "chatgpt", AccessToken: "token"}
	provider := NewCodexProvider(cfg)
	for _, capability := range provider.Capabilities() {
		if capability == CapabilityEmbeddings {
			t.Fatal("ChatGPT mode must not advertise embeddings")
		}
	}
	cfg.Providers.Codex.Auth.Mode = "api_key"
	cfg.Providers.Codex.Auth.APIKey = "test"
	found := false
	for _, capability := range provider.Capabilities() {
		if capability == CapabilityEmbeddings {
			found = true
		}
	}
	if !found {
		t.Fatal("API-key mode should advertise embeddings")
	}
}

func TestChatGPTModeRejectsEmbeddingsWithoutOutboundCall(t *testing.T) {
	cfg := defaultConfig()
	cfg.Providers.Codex.Enabled = true
	cfg.Providers.Codex.Auth = CodexAuthState{
		Mode: "chatgpt", AccessToken: "token", AccountID: "account",
		ExpiresAt: time.Now().Unix() + 3600,
	}
	provider := NewCodexProvider(cfg)
	calls := 0
	provider.proxyExecutor = func(context.Context, *http.Request, []byte, Capability) (*http.Response, string, error) {
		calls++
		return nil, "", nil
	}
	err := provider.ProxyRequest(
		context.Background(), httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/v1/embeddings", nil),
		[]byte(`{"model":"text-embedding-3-large","input":"x"}`),
		CapabilityEmbeddings,
	)
	if err == nil || calls != 0 {
		t.Fatalf("err=%v outbound calls=%d", err, calls)
	}
}

func TestCodexAccountCredentialsAreIsolatedFromOfficialStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"official-token","refresh_token":"official-refresh","account_id":"official-account"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.Providers.Codex.Accounts = []CodexAccount{
		{Label: "a", Auth: CodexAuthState{Mode: "chatgpt", AccessToken: "token-a", AccountID: "account-a", ExpiresAt: time.Now().Unix() + 3600}},
		{Label: "b", Auth: CodexAuthState{Mode: "chatgpt", AccessToken: "token-b", AccountID: "account-b", ExpiresAt: time.Now().Unix() + 3600}},
	}
	provider := NewCodexProvider(cfg)
	if err := provider.EnsureAuthenticated(context.Background()); err != nil {
		t.Fatal(err)
	}
	token, accountID, _ := provider.authCredentials()
	if token != "token-a" || accountID != "account-a" {
		t.Fatalf("first account was overridden: token=%q account=%q", token, accountID)
	}
	provider.mu.Lock()
	if !provider.advanceCodexAccount() {
		provider.mu.Unlock()
		t.Fatal("expected account advance")
	}
	provider.mu.Unlock()
	token, accountID, _ = provider.authCredentials()
	if token != "token-b" || accountID != "account-b" {
		t.Fatalf("second account was overridden: token=%q account=%q", token, accountID)
	}
}

func TestOfficialCodexStoreIsNeverMutated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "auth.json")
	original := []byte(`{"auth_mode":"chatgpt","agent_identity":{"opaque":"preserve"},"tokens":{"access_token":"old"}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := persistToOfficialStore(&CodexAuthState{AccessToken: "new"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("official auth store changed: %s", after)
	}
}

func TestRedactAuthErrorNeverReturnsUpstreamSecrets(t *testing.T) {
	secret := "sensitive-refresh-token"
	reason := redactAuthError([]byte(`{"error":{"code":"invalid_grant","message":"` + secret + `"}}`))
	if reason != "invalid_grant" {
		t.Fatalf("reason = %q, want invalid_grant", reason)
	}
	if strings.Contains(reason, secret) {
		t.Fatalf("redacted reason leaked secret: %q", reason)
	}
}

func TestExtractJWTExpiryAndAccountMetadata(t *testing.T) {
	payload, _ := json.Marshal(map[string]interface{}{
		"exp":                         1234567890,
		"https://api.openai.com/auth": map[string]string{"chatgpt_account_id": "acct-1"},
	})
	token := "x." + base64.RawURLEncoding.EncodeToString(payload) + ".y"
	if got := extractJWTExpiry(token); got != 1234567890 {
		t.Fatalf("expiry=%d", got)
	}
	if got := extractAccountIDFromJWT(token); got != "acct-1" {
		t.Fatalf("account=%q", got)
	}
}
