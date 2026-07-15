package yarouter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCoalesceRequest_ConcurrentWaitersReceiveSameResult verifies that
// all concurrent callers of CoalesceRequest for the same key receive
// the same non-nil result.
func TestCoalesceRequest_ConcurrentWaitersReceiveSameResult(t *testing.T) {
	cc := NewCoalescingCache()
	key := cc.getRequestKey("GET", "/v1/models", nil)

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
	if !ids["github/gpt-4"] || !ids["codex/o1-preview"] {
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
	cb := &CircuitBreaker{state: CircuitClosed, timeout: 100 * time.Millisecond}

	// Should start closed
	if !cb.canExecute() {
		t.Fatal("circuit breaker should allow execution when closed")
	}

	// Trigger failures to open the circuit
	for i := 0; i < circuitBreakerFailureThreshold; i++ {
		cb.onFailure()
	}
	if cb.canExecute() {
		t.Fatal("circuit breaker should be open after threshold failures")
	}

	// Wait for timeout to transition to half-open
	time.Sleep(150 * time.Millisecond)
	if !cb.canExecute() {
		t.Fatal("circuit breaker should allow execution after timeout (half-open)")
	}

	// Success should close it
	cb.onSuccess()
	if !cb.canExecute() {
		t.Fatal("circuit breaker should be closed after success")
	}
}

func TestCircuitBreaker_HalfOpenAllowsSingleProbe(t *testing.T) {
	cb := &CircuitBreaker{
		failureCount:    circuitBreakerFailureThreshold,
		lastFailureTime: time.Now().Add(-time.Second),
		state:           CircuitOpen,
		timeout:         time.Millisecond,
	}

	const callers = 64
	start := make(chan struct{})
	var allowed atomic.Int32
	var wait sync.WaitGroup
	wait.Add(callers)
	for index := 0; index < callers; index++ {
		go func() {
			defer wait.Done()
			<-start
			if cb.canExecute() {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wait.Wait()

	if got := allowed.Load(); got != 1 {
		t.Fatalf("half-open probes allowed = %d, want 1", got)
	}
	cb.onFailure()
	if cb.canExecute() {
		t.Fatal("failed half-open probe should reopen the circuit")
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

func TestWorkerPool_BackpressureHonorsCancellationAndStop(t *testing.T) {
	pool := NewWorkerPool(1)
	started := make(chan struct{})
	release := make(chan struct{})
	if !pool.Submit(func() {
		close(started)
		<-release
	}) {
		t.Fatal("first job was rejected")
	}
	<-started

	queuedDone := make(chan struct{}, 2)
	for index := 0; index < 2; index++ {
		if !pool.Submit(func() { queuedDone <- struct{}{} }) {
			t.Fatal("queue fill job was rejected")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if pool.SubmitContext(ctx, func() {}) {
		t.Fatal("saturated pool accepted a job after its context expired")
	}

	close(release)
	for index := 0; index < 2; index++ {
		select {
		case <-queuedDone:
		case <-time.After(time.Second):
			t.Fatal("queued job did not finish")
		}
	}
	pool.Stop()
	if pool.SubmitContext(context.Background(), func() {}) {
		t.Fatal("stopped pool accepted a job")
	}
}
