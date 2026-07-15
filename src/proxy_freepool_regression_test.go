package yarouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProcessProxyRequest_GivenUnknownUnqualifiedChatModel_WhenRouting_ThenReturnsRoutingError(t *testing.T) {
	registry := NewProviderRegistry()
	var normalCalls int32
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-4", Object: "model"}}},
		proxyFunc: func(_ context.Context, _ http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			atomic.AddInt32(&normalCalls, 1)
			return nil
		},
	})

	cfg := defaultConfig()
	router := NewModelRouter(registry, cfg.Routing)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"unknown-model","messages":[{"role":"user","content":"hi"}]}`))
	rr := httptest.NewRecorder()

	err := processProxyRequest(registry, router, cfg, rr, req, context.Background())
	if got := atomic.LoadInt32(&normalCalls); got != 0 {
		t.Fatalf("normal proxy path was used %d times, want 0", got)
	}
	if err == nil {
		t.Fatal("expected routing error, got nil")
	}
}

func TestProcessProxyRequest_GivenExplicitGithubChatModel_WhenProxying_ThenUsesNormalProxyRequest(t *testing.T) {
	registry := NewProviderRegistry()
	var normalCalls int32
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-4", Object: "model"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			atomic.AddInt32(&normalCalls, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return nil
		},
	})

	cfg := defaultConfig()
	router := NewModelRouter(registry, cfg.Routing)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"github/gpt-4","messages":[{"role":"user","content":"hi"}]}`))
	rr := httptest.NewRecorder()

	err := processProxyRequest(registry, router, cfg, rr, req, context.Background())
	if err != nil {
		t.Fatalf("processProxyRequest: %v", err)
	}
	if got := atomic.LoadInt32(&normalCalls); got != 1 {
		t.Fatalf("normal proxy path was used %d times, want 1", got)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestProcessProxyRequest_GivenCodexProviderPrefixedChatModel_WhenProxying_ThenRoutesToCodexAndRewritesBodyModel(t *testing.T) {
	registry := NewProviderRegistry()
	var codexCalls int32
	var copilotCalls int32
	var forwardedBody string

	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-4", Object: "model"}}},
		proxyFunc: func(_ context.Context, _ http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			atomic.AddInt32(&copilotCalls, 1)
			return nil
		},
	})
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Mock Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4-mini", Object: "model"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, _ Capability) error {
			atomic.AddInt32(&codexCalls, 1)
			forwardedBody = string(body)
			w.WriteHeader(http.StatusOK)
			return nil
		},
	})

	cfg := defaultConfig()
	router := NewModelRouter(registry, cfg.Routing)
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"codex/gpt-5.4-mini","messages":[{"role":"user","content":"hi"}]}`))
	rr := httptest.NewRecorder()

	err := processProxyRequest(registry, router, cfg, rr, req, context.Background())
	if err != nil {
		t.Fatalf("processProxyRequest: %v", err)
	}
	if got := atomic.LoadInt32(&codexCalls); got != 1 {
		t.Fatalf("codex proxy path was used %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&copilotCalls); got != 0 {
		t.Fatalf("copilot proxy path was used %d times, want 0", got)
	}
	if got := extractModelFromBody([]byte(forwardedBody)); got != "gpt-5.4-mini" {
		t.Fatalf("forwarded body model = %q, want gpt-5.4-mini", got)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}
