package yarouter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	secretpkg "github.com/duvu/ya-router/internal/secret"
)

func newTestKiloProvider(t *testing.T, handler http.HandlerFunc) (*KiloProvider, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv(kiloAPIKeyEnv, "")
	t.Setenv(kiloOrganizationIDEnv, "")
	t.Setenv(kiloGatewayBaseURLEnv, "")
	cfg := defaultConfig()
	cfg.Providers.Kilo.Enabled = true
	cfg.Providers.Kilo.AllowAnonymous = true
	cfg.Providers.Kilo.BaseURL = server.URL
	provider := NewKiloProvider(cfg)
	provider.client = server.Client()
	return provider, server
}

func TestKiloAnonymousModelDiscoveryOnlyExposesFreeModels(t *testing.T) {
	provider, _ := newTestKiloProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("anonymous model discovery Authorization = %q, want empty", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []map[string]interface{}{
				{"id": "anthropic/paid", "object": "model", "pricing": map[string]string{"prompt": "0.000003", "completion": "0.000015"}},
				{"id": "example/promotional", "object": "model", "isFree": true},
				{"id": "example/free:free", "object": "model"},
			},
		})
	})

	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	ids := make(map[string]bool)
	for _, model := range models.Data {
		ids[model.ID] = true
	}
	for _, want := range []string{"example/promotional", "example/free:free", kiloAutoFreeModel} {
		if !ids[want] {
			t.Errorf("free model %q missing from %v", want, ids)
		}
	}
	if ids["anthropic/paid"] {
		t.Errorf("paid model exposed to anonymous provider: %v", ids)
	}
}

func TestKiloAuthenticatedProxyUsesProviderCredentialAndPreservesStatus(t *testing.T) {
	provider, _ := newTestKiloProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer kilo-secret" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-KiloCode-OrganizationId"); got != "org-1" {
			t.Errorf("organization header = %q", got)
		}
		if got := r.Header.Get("X-KiloCode-Mode"); got != "plan" {
			t.Errorf("mode header = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"anthropic/paid"`) {
			t.Errorf("upstream body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	})
	provider.cfg.Providers.Kilo.APIKey = "kilo-secret"
	provider.cfg.Providers.Kilo.OrganizationID = "org-1"

	clientRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	clientRequest.Header.Set("Authorization", "Bearer inbound-router-key")
	clientRequest.Header.Set("X-KiloCode-Mode", "plan")
	clientRequest.Header.Set("X-KiloCode-OrganizationId", "caller-controlled-org")
	response := httptest.NewRecorder()
	err := provider.ProxyRequest(context.Background(), response, clientRequest,
		[]byte(`{"model":"anthropic/paid","messages":[{"role":"user","content":"hello"}]}`), CapabilityChat)
	if err != nil {
		t.Fatalf("ProxyRequest() error = %v", err)
	}
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	if !strings.Contains(response.Body.String(), "rate limited") {
		t.Errorf("body = %q", response.Body.String())
	}
}

func TestKiloUsesManagedCredentialResolver(t *testing.T) {
	cfg := defaultConfig()
	cfg.Providers.Kilo.APIKey = "legacy-key"
	store := secretpkg.NewMemoryStore(nil)
	if _, err := store.Set("operator", "kilo/api_key", "managed-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Set("operator", "kilo/organization_id", "managed-organization"); err != nil {
		t.Fatal(err)
	}
	provider := NewKiloProviderWithAuth(cfg, secretpkg.NewStoreController(store, nil))
	key, source := provider.apiKey()
	if key != "managed-key" || source != "managed" {
		t.Fatalf("credential = %q from %q", key, source)
	}
	if organizationID := provider.organizationID(); organizationID != "managed-organization" {
		t.Fatalf("organization id = %q", organizationID)
	}
}

func TestKiloAnonymousProxyRejectsPaidModel(t *testing.T) {
	provider, _ := newTestKiloProvider(t, func(http.ResponseWriter, *http.Request) {
		t.Error("upstream must not be called for a paid anonymous model")
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	err := provider.ProxyRequest(context.Background(), httptest.NewRecorder(), request,
		[]byte(`{"model":"anthropic/paid","messages":[]}`), CapabilityChat)
	if providerErrorStatus(err) != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (err=%v)", providerErrorStatus(err), http.StatusUnauthorized, err)
	}
}

func TestKiloResponsesPassthroughPreservesSSE(t *testing.T) {
	provider, _ := newTestKiloProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path = %q, want /responses", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\"}\n\n"))
	})

	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	response := httptest.NewRecorder()
	err := provider.ProxyRequest(context.Background(), response, request,
		[]byte(`{"model":"kilo-auto/free","input":"hello","stream":true}`), CapabilityResponses)
	if err != nil {
		t.Fatalf("ProxyRequest() error = %v", err)
	}
	if got := response.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q", got)
	}
	if !strings.Contains(response.Body.String(), "response.completed") {
		t.Errorf("body = %q", response.Body.String())
	}
}

func TestKiloRejectsInsecureRemoteBaseURL(t *testing.T) {
	t.Setenv(kiloGatewayBaseURLEnv, "")
	cfg := defaultConfig()
	cfg.Providers.Kilo.Enabled = true
	cfg.Providers.Kilo.AllowAnonymous = true
	cfg.Providers.Kilo.BaseURL = "http://example.com/api/gateway"
	provider := NewKiloProvider(cfg)
	if err := provider.EnsureAuthenticated(context.Background()); err == nil {
		t.Fatal("EnsureAuthenticated() error = nil, want invalid base URL error")
	}
}

func TestKiloAllowedModelsFilter(t *testing.T) {
	provider, _ := newTestKiloProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"one/free:free"},{"id":"two/free:free"}]}`))
	})
	provider.cfg.Providers.Kilo.AllowedModels = []string{"kilo/two/free:free"}
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models.Data) != 1 || models.Data[0].ID != "two/free:free" {
		t.Fatalf("models = %#v", models.Data)
	}
}

func TestKiloAnonymousAllowlistDoesNotSynthesizePaidModel(t *testing.T) {
	provider, _ := newTestKiloProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"one/free:free"},{"id":"vendor/paid","pricing":{"prompt":"0.1","completion":"0.2"}}]}`))
	})
	provider.cfg.Providers.Kilo.AllowedModels = []string{"kilo/vendor/paid"}
	models, err := provider.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models.Data) != 0 {
		t.Fatalf("models = %#v, want no anonymous paid models", models.Data)
	}
}

func TestKiloFreeModelDiscoveryRevokesStaleEntries(t *testing.T) {
	calls := 0
	provider, _ := newTestKiloProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			_, _ = w.Write([]byte(`{"data":[{"id":"vendor/changing","isFree":true}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"vendor/changing","pricing":{"prompt":"0.1","completion":"0.2"}}]}`))
	})

	if _, err := provider.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !provider.allowsAnonymousModel("vendor/changing") {
		t.Fatal("discovered free model was not admitted")
	}
	provider.InvalidateModelCache()
	if _, err := provider.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.allowsAnonymousModel("vendor/changing") {
		t.Fatal("model remained anonymously accessible after the catalog marked it paid")
	}
}
