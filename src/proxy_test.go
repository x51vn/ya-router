package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProcessProxyRequest_ChatUsesCopilotFreePathBeforeRouter(t *testing.T) {
	registry := NewProviderRegistry()
	var gotRequested string
	var gotBodyModel string
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		freeChatProxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, requestedModel string) error {
			gotRequested = requestedModel
			gotBodyModel = extractModelFromBody(body)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return nil
		},
	})

	cfg := defaultConfig()
	cfg.Routing.DefaultProvider = string(ProviderCodex)
	router := NewModelRouter(registry, cfg.Routing)

	req := httptest.NewRequest("POST", "/v1/chat/completions", io.NopCloser(strings.NewReader(`{"model":"claude-sonnet-4.6","messages":[{"role":"user","content":"hi"}]}`)))
	rr := httptest.NewRecorder()

	err := processProxyRequest(registry, router, cfg, rr, req, context.Background())
	if err != nil {
		t.Fatalf("processProxyRequest: %v", err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if gotRequested != "claude-sonnet-4.6" {
		t.Fatalf("requestedModel = %q, want claude-sonnet-4.6", gotRequested)
	}
	if gotBodyModel != "claude-sonnet-4.6" {
		t.Fatalf("body model = %q, want claude-sonnet-4.6", gotBodyModel)
	}
}

func TestProcessProxyRequest_ChatSkipsFreeRotationForPrefixedModel(t *testing.T) {
	registry := NewProviderRegistry()

	var freeCalled bool
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		freeChatProxyFunc: func(context.Context, http.ResponseWriter, *http.Request, []byte, string) error {
			freeCalled = true
			return nil
		},
	})

	var codexCalled bool
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Mock Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4", Object: "model"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, _ Capability) error {
			codexCalled = true
			if got := extractModelFromBody(body); got != "gpt-5.4" {
				t.Fatalf("proxy body model = %q, want gpt-5.4", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return nil
		},
	})

	cfg := defaultConfig()
	cfg.Routing.DefaultProvider = string(ProviderCodex)
	router := NewModelRouter(registry, cfg.Routing)

	req := httptest.NewRequest("POST", "/v1/chat/completions", io.NopCloser(strings.NewReader(`{"model":"oc-gpt-5.4","messages":[{"role":"user","content":"hi"}]}`)))
	rr := httptest.NewRecorder()

	if err := processProxyRequest(registry, router, cfg, rr, req, context.Background()); err != nil {
		t.Fatalf("processProxyRequest: %v", err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if freeCalled {
		t.Fatal("free rotation should not run for explicit provider-prefixed model")
	}
	if !codexCalled {
		t.Fatal("expected normal Codex routing path to run")
	}
}

func TestProcessProxyRequest_ResponsesSkipsUnrelatedShortcutForPrefixedModel(t *testing.T) {
	registry := NewProviderRegistry()

	var copilotResponsesCalled bool
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat, CapabilityResponses},
		health: ProviderHealth{Authenticated: true},
		responsesProxyFunc: func(context.Context, http.ResponseWriter, *http.Request, []byte, string) error {
			copilotResponsesCalled = true
			return nil
		},
	})

	var codexResponsesCalled bool
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Mock Codex",
		caps:   []Capability{CapabilityChat, CapabilityResponses},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4", Object: "model"}}},
		responsesProxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, _ string) error {
			codexResponsesCalled = true
			if got := extractModelFromBody(body); got != "gpt-5.4" {
				t.Fatalf("responses body model = %q, want gpt-5.4", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","model":"gpt-5.4","output":[]}`))
			return nil
		},
	})

	cfg := defaultConfig()
	cfg.Routing.DefaultProvider = string(ProviderCodex)
	router := NewModelRouter(registry, cfg.Routing)

	req := httptest.NewRequest("POST", "/v1/responses", io.NopCloser(strings.NewReader(`{"model":"oc-gpt-5.4","input":"hi"}`)))
	rr := httptest.NewRecorder()

	if err := processProxyRequest(registry, router, cfg, rr, req, context.Background()); err != nil {
		t.Fatalf("processProxyRequest: %v", err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if copilotResponsesCalled {
		t.Fatal("responses request should not be hijacked by unrelated provider shortcut")
	}
	if !codexResponsesCalled {
		t.Fatal("expected explicit prefixed responses request to route to Codex")
	}
}

func TestProcessProxyRequest_ResponsesRejectsUnsupportedFields(t *testing.T) {
	registry := NewProviderRegistry()
	router := NewModelRouter(registry, defaultConfig().Routing)
	req := httptest.NewRequest("POST", "/v1/responses", io.NopCloser(strings.NewReader(`{"model":"gpt-5.4","input":"hi","previous_response_id":"resp_old"}`)))
	rr := httptest.NewRecorder()

	if err := processProxyRequest(registry, router, defaultConfig(), rr, req, context.Background()); err != nil {
		t.Fatalf("processProxyRequest: %v", err)
	}
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "previous_response_id") {
		t.Fatalf("response body = %q, want unsupported field message", rr.Body.String())
	}
}

func TestProcessProxyRequest_ResponsesAllowsReasoningForCodex(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Mock Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4", Object: "model"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, _ Capability) error {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			var gotReasoning map[string]interface{}
			if err := json.Unmarshal(raw["reasoning"], &gotReasoning); err != nil {
				t.Fatalf("unmarshal reasoning: %v", err)
			}
			if gotReasoning["effort"] != "medium" {
				t.Fatalf("reasoning.effort = %#v, want medium", gotReasoning["effort"])
			}
			if _, ok := gotReasoning["summary"]; ok {
				t.Fatal("reasoning.summary should be stripped in compat chat body")
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":1700000000,"model":"gpt-5.4","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
			return nil
		},
	})

	cfg := defaultConfig()
	cfg.Routing.DefaultProvider = string(ProviderCodex)
	cfg.Routing.ModelMap = map[string]ModelMapEntry{
		"oc-gpt-5.4": {Provider: string(ProviderCodex), UpstreamModel: "gpt-5.4"},
	}
	router := NewModelRouter(registry, cfg.Routing)
	req := httptest.NewRequest("POST", "/v1/responses", io.NopCloser(strings.NewReader(`{"model":"oc-gpt-5.4","input":"hi","reasoning":{"effort":"medium","summary":"auto"}}`)))
	rr := httptest.NewRecorder()

	if err := processProxyRequest(registry, router, cfg, rr, req, context.Background()); err != nil {
		t.Fatalf("processProxyRequest: %v", err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestProcessProxyRequest_ResponsesNativeCodexForcesStoreFalse(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Mock Codex",
		caps:   []Capability{CapabilityChat, CapabilityResponses},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4", Object: "model"}}},
		responsesProxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, _ string) error {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			var store bool
			if err := json.Unmarshal(raw["store"], &store); err != nil {
				t.Fatalf("unmarshal store: %v", err)
			}
			if store {
				t.Fatal("store = true, want false")
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","model":"gpt-5.4","output":[]}`))
			return nil
		},
	})

	cfg := defaultConfig()
	cfg.Routing.DefaultProvider = string(ProviderCodex)
	cfg.Routing.ModelMap = map[string]ModelMapEntry{
		"oc-gpt-5.4": {Provider: string(ProviderCodex), UpstreamModel: "gpt-5.4"},
	}
	router := NewModelRouter(registry, cfg.Routing)
	req := httptest.NewRequest("POST", "/v1/responses", io.NopCloser(strings.NewReader(`{"model":"oc-gpt-5.4","input":"hi","store":true}`)))
	rr := httptest.NewRecorder()

	if err := processProxyRequest(registry, router, cfg, rr, req, context.Background()); err != nil {
		t.Fatalf("processProxyRequest: %v", err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestProcessProxyRequest_ResponsesNativeCodexStripsReasoningSummaryOnly(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Mock Codex",
		caps:   []Capability{CapabilityChat, CapabilityResponses},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4", Object: "model"}}},
		responsesProxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, _ string) error {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			if _, ok := raw["reasoningSummary"]; ok {
				t.Fatal("reasoningSummary should be stripped for native Codex responses requests")
			}
			if _, ok := raw["reasoning"]; !ok {
				t.Fatal("reasoning should be preserved for native Codex responses requests")
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","model":"gpt-5.4","output":[]}`))
			return nil
		},
	})

	cfg := defaultConfig()
	cfg.Routing.DefaultProvider = string(ProviderCodex)
	cfg.Routing.ModelMap = map[string]ModelMapEntry{
		"oc-gpt-5.4": {Provider: string(ProviderCodex), UpstreamModel: "gpt-5.4"},
	}
	router := NewModelRouter(registry, cfg.Routing)
	req := httptest.NewRequest("POST", "/v1/responses", io.NopCloser(strings.NewReader(`{"model":"oc-gpt-5.4","input":"hi","reasoning":{"effort":"medium"},"reasoningSummary":"auto"}`)))
	rr := httptest.NewRecorder()

	if err := processProxyRequest(registry, router, cfg, rr, req, context.Background()); err != nil {
		t.Fatalf("processProxyRequest: %v", err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestProcessProxyRequest_ResponsesStripsReasoningForNonCodexFallback(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "claude-sonnet-4-6", Object: "model"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, body []byte, _ Capability) error {
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatalf("unmarshal body: %v", err)
			}
			if _, ok := raw["reasoning"]; ok {
				t.Fatal("reasoning should be stripped for non-Codex providers")
			}
			if _, ok := raw["reasoningSummary"]; ok {
				t.Fatal("reasoningSummary should be stripped for non-Codex providers")
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":1700000000,"model":"claude-sonnet-4-6","choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
			return nil
		},
	})

	cfg := defaultConfig()
	cfg.Routing.DefaultProvider = string(ProviderCopilot)
	router := NewModelRouter(registry, cfg.Routing)
	req := httptest.NewRequest("POST", "/v1/responses", io.NopCloser(strings.NewReader(`{"model":"claude-sonnet-4-6","input":"hi","reasoning":{"effort":"low"}}`)))
	rr := httptest.NewRecorder()

	if err := processProxyRequest(registry, router, cfg, rr, req, context.Background()); err != nil {
		t.Fatalf("processProxyRequest: %v", err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
}

func TestCopilotProvider_ProxyFreeChatRequest_RotatesAndIgnoresClientModel(t *testing.T) {
	provider := NewCopilotProvider(defaultConfig())
	provider.FreeModelResolver = func(context.Context) ([]Model, error) {
		return []Model{
			{ID: "gpt-4.1", Name: "GPT-4.1"},
			{ID: "gpt-4o", Name: "GPT-4o"},
		}, nil
	}

	var attempted []string
	provider.ProxyExecutor = func(_ context.Context, _ *http.Request, body []byte, _ Capability) (*http.Response, string, error) {
		attempted = append(attempted, extractModelFromBody(body))
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}
		resp.Header.Set("Content-Type", "application/json")
		return resp, "", nil
	}

	makeRequest := func() *http.Request {
		return httptest.NewRequest("POST", "/v1/chat/completions", io.NopCloser(strings.NewReader(`{"model":"claude-sonnet-4.6","messages":[{"role":"user","content":"hi"}]}`)))
	}

	rr1 := httptest.NewRecorder()
	if err := provider.ProxyFreeChatRequest(context.Background(), rr1, makeRequest(), []byte(`{"model":"claude-sonnet-4.6","messages":[{"role":"user","content":"hi"}]}`), "claude-sonnet-4.6"); err != nil {
		t.Fatalf("first ProxyFreeChatRequest: %v", err)
	}
	rr2 := httptest.NewRecorder()
	if err := provider.ProxyFreeChatRequest(context.Background(), rr2, makeRequest(), []byte(`{"model":"claude-sonnet-4.6","messages":[{"role":"user","content":"hi"}]}`), "claude-sonnet-4.6"); err != nil {
		t.Fatalf("second ProxyFreeChatRequest: %v", err)
	}

	want := []string{"gpt-4.1", "gpt-4o"}
	if !reflect.DeepEqual(attempted, want) {
		t.Fatalf("attempted models = %v, want %v", attempted, want)
	}
}

func TestCopilotProvider_ProxyFreeChatRequest_ShiftsOnFailure(t *testing.T) {
	provider := NewCopilotProvider(defaultConfig())
	provider.FreeModelResolver = func(context.Context) ([]Model, error) {
		return []Model{
			{ID: "gpt-4.1", Name: "GPT-4.1"},
			{ID: "gpt-4o", Name: "GPT-4o"},
			{ID: "gpt-5-mini", Name: "GPT-5 mini"},
		}, nil
	}

	var attempted []string
	provider.ProxyExecutor = func(_ context.Context, _ *http.Request, body []byte, _ Capability) (*http.Response, string, error) {
		modelID := extractModelFromBody(body)
		attempted = append(attempted, modelID)
		resp := &http.Response{
			Header: make(http.Header),
		}
		resp.Header.Set("Content-Type", "application/json")
		if modelID == "gpt-4.1" {
			resp.StatusCode = http.StatusServiceUnavailable
			resp.Body = io.NopCloser(strings.NewReader(`{"error":"temporary failure"}`))
			return resp, "", nil
		}
		resp.StatusCode = http.StatusOK
		resp.Body = io.NopCloser(strings.NewReader(`{"model":"` + modelID + `"}`))
		return resp, "", nil
	}

	rr := httptest.NewRecorder()
	err := provider.ProxyFreeChatRequest(
		context.Background(),
		rr,
		httptest.NewRequest("POST", "/v1/chat/completions", io.NopCloser(strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`))),
		[]byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}]}`),
		"gpt-5.4",
	)
	if err != nil {
		t.Fatalf("ProxyFreeChatRequest: %v", err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	want := []string{"gpt-4.1", "gpt-4o"}
	if !reflect.DeepEqual(attempted, want) {
		t.Fatalf("attempted models = %v, want %v", attempted, want)
	}
}

// TestCoalesceRequest_ConcurrentWaitersReceiveSameResult verifies that
// all concurrent callers of CoalesceRequest for the same key receive
// the same non-nil result.
func TestCoalesceRequest_ConcurrentWaitersReceiveSameResult(t *testing.T) {
	cc := NewCoalescingCache()
	key := cc.GetRequestKey("GET", "/v1/models", nil)

	expected := &ModelList{
		Object: "list",
		Data:   []Model{{ID: "test-model", Object: "model"}},
	}

	var callCount int32
	fn := func() interface{} {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond)
		return expected
	}

	const numWaiters = 20
	results := make([]interface{}, numWaiters)
	var wg sync.WaitGroup
	wg.Add(numWaiters)

	for i := 0; i < numWaiters; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = cc.CoalesceRequest(key, fn)
		}(i)
	}

	wg.Wait()

	if c := atomic.LoadInt32(&callCount); c != 1 {
		t.Errorf("fn called %d times, want 1", c)
	}

	for i, r := range results {
		ml, ok := r.(*ModelList)
		if !ok || ml == nil {
			t.Fatalf("waiter %d received nil or wrong type: %v", i, r)
		}
		if ml != expected {
			t.Errorf("waiter %d received different pointer", i)
		}
	}
}

// TestMakeRequestWithRetry_PreservesContext verifies that the context
// from the original request is propagated to each retry attempt.
func TestMakeRequestWithRetry_PreservesContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok": true}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := makeRequestWithRetry(client, req, []byte(`{"test": true}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

// TestMakeRequestWithRetry_CancelledContextStopsRetries verifies that
// a cancelled context stops the retry loop.
func TestMakeRequestWithRetry_CancelledContextStopsRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/test", nil)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	_, err = makeRequestWithRetry(client, req, []byte(`{}`))

	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

// TestMakeRequestWithRetry_LastRetryBodyNotClosed verifies that on the
// last retry attempt the response body is still readable.
func TestMakeRequestWithRetry_LastRetryBodyNotClosed(t *testing.T) {
	errorBody := `{"error": "rate limited", "retry_after": 60}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(errorBody))
	}))
	defer server.Close()

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/test", nil)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := makeRequestWithRetry(client, req, []byte(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()

	if readErr != nil {
		t.Fatalf("failed to read response body: %v", readErr)
	}

	if string(body) != errorBody {
		t.Errorf("body = %q, want %q", string(body), errorBody)
	}

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusTooManyRequests)
	}
}

// TestFilterAllowedModels verifies the filter logic.
func TestFilterAllowedModels(t *testing.T) {
	full := &ModelList{
		Object: "list",
		Data: []Model{
			{ID: "gpt-4", Object: "model"},
			{ID: "gpt-4.1", Object: "model"},
			{ID: "gpt-5-mini", Object: "model"},
			{ID: "claude-3.5-sonnet", Object: "model"},
		},
	}

	tests := []struct {
		name    string
		allowed []string
		wantIDs []string
	}{
		{
			name:    "empty allowed list returns all",
			allowed: []string{},
			wantIDs: []string{"gpt-4", "gpt-4.1", "gpt-5-mini", "claude-3.5-sonnet"},
		},
		{
			name:    "filter to subset",
			allowed: []string{"gpt-4", "gpt-5-mini"},
			wantIDs: []string{"gpt-4", "gpt-5-mini"},
		},
		{
			name:    "no match returns synthetic",
			allowed: []string{"nonexistent-model"},
			wantIDs: []string{"nonexistent-model"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := filterAllowedModels(full, tt.allowed)

			if len(result.Data) != len(tt.wantIDs) {
				t.Fatalf("got %d models, want %d", len(result.Data), len(tt.wantIDs))
			}

			ids := make(map[string]bool)
			for _, m := range result.Data {
				ids[m.ID] = true
			}
			for _, wantID := range tt.wantIDs {
				if !ids[wantID] {
					t.Errorf("missing expected model %q", wantID)
				}
			}
		})
	}
}

// TestModelsHandler_WithMockProvider tests the models endpoint with a mock provider.
func TestModelsHandler_WithMockProvider(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{
			Object: "list",
			Data: []Model{
				{ID: "gpt-4", Object: "model", OwnedBy: "openai"},
				{ID: "gpt-5-mini", Object: "model", OwnedBy: "openai"},
			},
		},
	})

	cfg := defaultConfig()
	handler := modelsHandler(registry, cfg)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var ml ModelList
	if err := json.NewDecoder(rr.Body).Decode(&ml); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(ml.Data) != 2 {
		t.Fatalf("got %d models, want 2", len(ml.Data))
	}
}

// TestModelsHandler_MultipleProviders tests aggregation across providers.
func TestModelsHandler_MultipleProviders(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "gpt-4", Object: "model", OwnedBy: "openai"},
		}},
	})
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Mock Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "o1-preview", Object: "model", OwnedBy: "openai"},
		}},
	})

	cfg := defaultConfig()
	handler := modelsHandler(registry, cfg)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	handler.ServeHTTP(rr, req)

	var ml ModelList
	if err := json.NewDecoder(rr.Body).Decode(&ml); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(ml.Data) != 2 {
		t.Fatalf("got %d models, want 2", len(ml.Data))
	}

	ids := map[string]bool{}
	for _, m := range ml.Data {
		ids[m.ID] = true
	}
	if !ids["gc-gpt-4"] || !ids["oc-o1-preview"] {
		t.Errorf("missing expected models, got IDs: %v", ids)
	}
}

// TestModelsHandler_UnauthenticatedProviderSkipped tests that unauthenticated
// providers are skipped unless ShowUnavailableModels is set.
func TestModelsHandler_UnauthenticatedProviderSkipped(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: false},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "gpt-4", Object: "model"},
		}},
	})

	cfg := defaultConfig()
	cfg.Routing.ShowUnavailableModels = false

	handler := modelsHandler(registry, cfg)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	handler.ServeHTTP(rr, req)

	var ml ModelList
	json.NewDecoder(rr.Body).Decode(&ml)

	if len(ml.Data) != 0 {
		t.Errorf("expected 0 models (provider unauthenticated), got %d", len(ml.Data))
	}
}

// TestModelsHandler_ShowUnavailableModels tests that unauthenticated providers
// are included when ShowUnavailableModels is true.
func TestModelsHandler_ShowUnavailableModels(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: false},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "gpt-4", Object: "model"},
		}},
	})

	cfg := defaultConfig()
	cfg.Routing.ShowUnavailableModels = true

	handler := modelsHandler(registry, cfg)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/models", nil)
	handler.ServeHTTP(rr, req)

	var ml ModelList
	json.NewDecoder(rr.Body).Decode(&ml)

	if len(ml.Data) != 1 {
		t.Errorf("expected 1 model (ShowUnavailableModels=true), got %d", len(ml.Data))
	}
}

// TestModelsHandler_ConcurrentRequests hits the models endpoint concurrently.
func TestModelsHandler_ConcurrentRequests(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Mock Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{
			{ID: "gpt-4", Object: "model"},
			{ID: "gpt-5-mini", Object: "model"},
		}},
	})

	cfg := defaultConfig()
	handler := modelsHandler(registry, cfg)

	const concurrency = 50
	var wg sync.WaitGroup
	wg.Add(concurrency)

	errors := make(chan string, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()

			rr := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/v1/models", nil)
			handler.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				errors <- fmt.Sprintf("waiter %d: status %d", idx, rr.Code)
				return
			}

			var ml ModelList
			if err := json.NewDecoder(rr.Body).Decode(&ml); err != nil {
				errors <- fmt.Sprintf("waiter %d: decode error: %v", idx, err)
				return
			}

			if len(ml.Data) == 0 {
				errors <- fmt.Sprintf("waiter %d: empty model list", idx)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	for e := range errors {
		t.Error(e)
	}
}

// TestCircuitBreaker_OpenClosedTransition tests the circuit breaker states.
func TestCircuitBreaker_OpenClosedTransition(t *testing.T) {
	cb := newCircuitBreakerWithState(CircuitClosed, 100*time.Millisecond)

	// Should start closed
	if !cb.CanExecute() {
		t.Fatal("circuit breaker should allow execution when closed")
	}

	// Trigger failures to open the circuit
	for i := 0; i < CircuitBreakerFailureThreshold; i++ {
		cb.OnFailure()
	}
	if cb.CanExecute() {
		t.Fatal("circuit breaker should be open after threshold failures")
	}

	// Wait for timeout to transition to half-open
	time.Sleep(150 * time.Millisecond)
	if !cb.CanExecute() {
		t.Fatal("circuit breaker should allow execution after timeout (half-open)")
	}

	// Success should close it
	cb.OnSuccess()
	if !cb.CanExecute() {
		t.Fatal("circuit breaker should be closed after success")
	}
}

// TestCapabilityFromPath tests path to capability mapping.
func TestCapabilityFromPath(t *testing.T) {
	tests := []struct {
		path    string
		want    Capability
		wantErr bool
	}{
		{"/v1/chat/completions", CapabilityChat, false},
		{"/v1/chat/completions/", CapabilityChat, false},
		{"/v1/responses", CapabilityResponses, false},
		{"/v1/embeddings", CapabilityEmbeddings, false},
		{"/v1/unknown", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := capabilityFromPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("capabilityFromPath(%q) error = %v, wantErr = %v", tt.path, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("capabilityFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// TestWorkerPool tests that the worker pool processes jobs.
func TestWorkerPool_ProcessesJobs(t *testing.T) {
	wp := NewWorkerPool(4)
	defer wp.Stop()

	var counter int32
	var wg sync.WaitGroup
	wg.Add(10)

	for i := 0; i < 10; i++ {
		wp.Submit(func() {
			defer wg.Done()
			atomic.AddInt32(&counter, 1)
		})
	}

	wg.Wait()
	if c := atomic.LoadInt32(&counter); c != 10 {
		t.Errorf("processed %d jobs, want 10", c)
	}
}
