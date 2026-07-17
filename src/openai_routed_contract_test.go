package yarouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestThienduChatForwardsToolsAndPreservesModelIdentity(t *testing.T) {
	var forwarded string
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true}, lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, writer http.ResponseWriter, _ *http.Request, body []byte, capability Capability) error {
			forwarded = string(body)
			if capability != CapabilityChat {
				t.Fatalf("capability = %s", capability)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"model":"gpt-5-mini","choices":[{"finish_reason":"tool_calls"}]}`))
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini"}},
	}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"thiendu","messages":[{"role":"user","content":"weather"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}]}`)))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(forwarded, `"model":"gpt-5-mini"`) || !strings.Contains(forwarded, `"tools"`) {
		t.Fatalf("forwarded request = %s", forwarded)
	}
	if !strings.Contains(recorder.Body.String(), `"model":"thiendu"`) || !strings.Contains(recorder.Body.String(), `"tool_calls"`) {
		t.Fatalf("response = %s", recorder.Body.String())
	}
}

func TestThienduResponsesSelectsResponsesCapableTarget(t *testing.T) {
	var selected Capability
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true}, lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(context.Context, http.ResponseWriter, *http.Request, []byte, Capability) error {
			t.Fatal("chat-only candidate was dispatched for Responses")
			return nil
		},
	})
	registry.Register(&mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat, CapabilityResponses},
		health: ProviderHealth{Authenticated: true}, lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, writer http.ResponseWriter, _ *http.Request, body []byte, capability Capability) error {
			selected = capability
			if !strings.Contains(string(body), `"model":"gpt-5.4-mini"`) {
				t.Fatalf("upstream body = %s", body)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"object":"response","model":"gpt-5.4-mini","output":[]}`))
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"thiendu","input":"hello"}`)))

	if recorder.Code != http.StatusOK || selected != CapabilityResponses {
		t.Fatalf("status=%d capability=%s body=%s", recorder.Code, selected, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"model":"thiendu"`) {
		t.Fatalf("response model identity = %s", recorder.Body.String())
	}
}

func TestThienduResponsesStreamingPreservesModelIdentity(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityResponses},
		health: ProviderHealth{Authenticated: true}, lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(_ context.Context, writer http.ResponseWriter, _ *http.Request, _ []byte, capability Capability) error {
			if capability != CapabilityResponses {
				t.Fatalf("capability = %s", capability)
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write([]byte("event: response.created\ndata: {\"response\":{\"model\":\"gpt-5.4-mini\"}}\n\ndata: [DONE]\n\n"))
			writer.(http.Flusher).Flush()
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"codex/gpt-5.4-mini"}},
	}
	handler := proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"thiendu","input":"hello","stream":true}`)))

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"model":"thiendu"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestThienduCancellationReachesOneSelectedProvider(t *testing.T) {
	started := make(chan struct{})
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id: ProviderCopilot, name: "Copilot", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true}, lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(ctx context.Context, _ http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	})
	var codexCalls int
	registry.Register(&mockProvider{
		id: ProviderCodex, name: "Codex", caps: []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true}, lastKnown: []string{"gpt-5.4-mini"},
		proxyFunc: func(context.Context, http.ResponseWriter, *http.Request, []byte, Capability) error {
			codexCalls++
			return nil
		},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
	}
	requestContext, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"thiendu","messages":[]}`)).WithContext(requestContext)
	done := make(chan struct{})
	go func() {
		proxyHandler(registry, NewModelRouter(registry, cfg.Routing), cfg).ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("selected provider did not receive the request")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not finish the selected request")
	}
	if codexCalls != 0 {
		t.Fatalf("cancelled request dispatched codex %d times", codexCalls)
	}
}
