package yarouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestModelsEndpointExposesUmbrellaModel verifies each configured virtual model
// appears exactly once in /v1/models with owned_by=ya-router.
func TestModelsEndpointExposesUmbrellaModel(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5-mini", Object: "model"}}},
	})

	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"router/auto": {Strategy: "priority", Targets: []string{"github/gpt-5-mini"}},
	}

	handler := modelsHandler(registry, cfg)
	req := httptest.NewRequest("GET", "/v1/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var ml ModelList
	if err := json.NewDecoder(rec.Body).Decode(&ml); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range ml.Data {
		if m.ID == "router/auto" {
			count++
			if m.OwnedBy != "ya-router" {
				t.Fatalf("owned_by = %q, want ya-router", m.OwnedBy)
			}
		}
	}
	if count != 1 {
		t.Fatalf("router/auto appeared %d times, want 1", count)
	}
}

func TestDefaultConfigExposesThreeProviderThienduRoute(t *testing.T) {
	cfg := defaultConfig()
	thiendu, ok := cfg.Routing.VirtualModels["thiendu"]
	if !ok {
		t.Fatal("default configuration does not define thiendu")
	}
	if thiendu.Strategy != "priority" {
		t.Fatalf("thiendu strategy = %q", thiendu.Strategy)
	}
	want := []string{"github/gpt-5-mini", "codex/gpt-5.4-mini", "kilo/kilo-auto/free"}
	if strings.Join(thiendu.Targets, ",") != strings.Join(want, ",") {
		t.Fatalf("thiendu targets = %v, want %v", thiendu.Targets, want)
	}
}

func TestModelsEndpointExposesDefaultThiendu(t *testing.T) {
	registry := NewProviderRegistry()
	cfg := defaultConfig()
	handler := modelsHandler(registry, cfg)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var models ModelList
	if err := json.NewDecoder(rec.Body).Decode(&models); err != nil {
		t.Fatal(err)
	}
	for _, model := range models.Data {
		if model.ID == "thiendu" && model.OwnedBy == "ya-router" {
			return
		}
	}
	t.Fatalf("thiendu was absent from models: %+v", models.Data)
}

// TestProxyUmbrellaRewritesBodyAndDispatchesOnce drives a full HTTP request
// through the proxy handler and proves the outbound body model is rewritten to
// the selected bare target and only the selected provider is called.
func TestProxyUmbrellaRewritesBodyAndDispatchesOnce(t *testing.T) {
	var (
		mu           sync.Mutex
		copilotBody  string
		copilotCalls int
		codexCalls   int
	)
	copilot := &mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, _ Capability) error {
			mu.Lock()
			copilotCalls++
			copilotBody = string(body)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return nil
		},
	}
	codex := &mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			mu.Lock()
			codexCalls++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return nil
		},
	}
	registry := NewProviderRegistry()
	registry.Register(copilot)
	registry.Register(codex)

	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"router/auto": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}
	router := NewModelRouter(registry, cfg.Routing)
	handler := proxyHandler(registry, router, cfg)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"router/auto","messages":[]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	if copilotCalls != 1 {
		t.Fatalf("copilot calls = %d, want 1", copilotCalls)
	}
	if codexCalls != 0 {
		t.Fatalf("codex calls = %d, want 0", codexCalls)
	}
	if !strings.Contains(copilotBody, `"gpt-5-mini"`) || strings.Contains(copilotBody, "router/auto") {
		t.Fatalf("outbound body not rewritten to bare target: %s", copilotBody)
	}
}

func TestProxyThienduPreservesExternalModelIdentity(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","model":"gpt-5-mini","choices":[]}`))
			return nil
		},
	})
	cfg := defaultConfig()
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"thiendu","messages":[]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Model != "thiendu" {
		t.Fatalf("response model = %q, want thiendu", response.Model)
	}
}

func TestProxyThienduStreamingPreservesExternalModelIdentity(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"model\":\"gpt-5-mini\",\"choices\":[]}\n\ndata: [DONE]\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return nil
		},
	})
	cfg := defaultConfig()
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"thiendu","stream":true,"messages":[]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, `"model":"thiendu"`) || strings.Contains(body, `"model":"gpt-5-mini"`) {
		t.Fatalf("streamed model identity = %s", body)
	}
}

func TestProxyUmbrellaCooldownOnlyAffectsLaterRequest(t *testing.T) {
	var copilotCalls, codexCalls int
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			copilotCalls++
			w.WriteHeader(http.StatusTooManyRequests)
			return nil
		},
	})
	registry.Register(&mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			codexCalls++
			w.WriteHeader(http.StatusOK)
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"thiendu","messages":[]}`)))
	if first.Code != http.StatusTooManyRequests || copilotCalls != 1 || codexCalls != 0 {
		t.Fatalf("first request status=%d copilot=%d codex=%d", first.Code, copilotCalls, codexCalls)
	}
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"thiendu","messages":[]}`)))
	if second.Code != http.StatusOK || copilotCalls != 1 || codexCalls != 1 {
		t.Fatalf("second request status=%d copilot=%d codex=%d", second.Code, copilotCalls, codexCalls)
	}
}

// TestProxyUmbrellaNoActiveTargetReturns503 proves a configured umbrella with
// no active target returns a typed model_unavailable error and calls no
// provider.
func TestProxyUmbrellaNoActiveTargetReturns503(t *testing.T) {
	var calls int
	copilot := &mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: false}, // not ready
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			calls++
			w.WriteHeader(http.StatusOK)
			return nil
		},
	}
	registry := NewProviderRegistry()
	registry.Register(copilot)

	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"router/auto": {Strategy: "priority", Targets: []string{"github/gpt-5-mini"}},
	}
	router := NewModelRouter(registry, cfg.Routing)
	handler := proxyHandler(registry, router, cfg)

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"router/auto","messages":[]}`))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Type != string(ProviderErrorModelUnavailable) {
		t.Fatalf("error type = %q, want model_unavailable", payload.Error.Type)
	}
	if calls != 0 {
		t.Fatalf("provider calls = %d, want 0", calls)
	}
}

// TestModelCacheLastKnownGood proves the cache serves last-known-good data
// (marking staleness) without a fetch.
func TestModelCacheLastKnownGood(t *testing.T) {
	cache := NewModelCache(10 * time.Millisecond)

	if _, _, _, ok := cache.LastKnownGood(); ok {
		t.Fatal("expected no last-known-good before any Set")
	}

	cache.Set(&ModelList{Object: "list", Data: []Model{{ID: "gpt-5-mini"}}})
	list, _, stale, ok := cache.LastKnownGood()
	if !ok || list == nil || len(list.Data) != 1 {
		t.Fatalf("last-known-good = %+v ok=%v", list, ok)
	}
	if stale {
		t.Fatal("expected fresh entry")
	}

	time.Sleep(15 * time.Millisecond)
	_, _, stale, ok = cache.LastKnownGood()
	if !ok {
		t.Fatal("last-known-good must persist past TTL")
	}
	if !stale {
		t.Fatal("expected stale after TTL")
	}

	// LastKnownGood returns a copy: mutating it must not affect the cache.
	list.Data[0].ID = "mutated"
	fresh, _, _, _ := cache.LastKnownGood()
	if fresh.Data[0].ID != "gpt-5-mini" {
		t.Fatal("LastKnownGood leaked a shared slice")
	}
}
