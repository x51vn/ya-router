// proxy.go — HTTP proxy infrastructure (circuit breaker, retry, worker pool,
// request coalescing) and the provider-dispatching request handler.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	copilotAPIBase = "https://api.githubcopilot.com"

	maxChatRetries     = 3
	baseChatRetryDelay = 1 // seconds

	circuitBreakerFailureThreshold = 5
)

// CircuitBreakerState represents the state of the circuit breaker.
type CircuitBreakerState int

const (
	CircuitClosed   CircuitBreakerState = iota
	CircuitOpen                         // too many failures; rejecting requests
	CircuitHalfOpen                     // testing if upstream has recovered
)

// CircuitBreaker guards against cascading failures to an upstream endpoint.
// Each provider holds its own instance.
type CircuitBreaker struct {
	failureCount    int64
	lastFailureTime time.Time
	state           CircuitBreakerState
	timeout         time.Duration
	mutex           sync.RWMutex
}

func (cb *CircuitBreaker) canExecute() bool {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	if cb.state == CircuitClosed {
		return true
	}
	if cb.state == CircuitOpen {
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.mutex.RUnlock()
			cb.mutex.Lock()
			cb.state = CircuitHalfOpen
			cb.mutex.Unlock()
			cb.mutex.RLock()
			return true
		}
		return false
	}
	return true // CircuitHalfOpen
}

func (cb *CircuitBreaker) onSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.failureCount = 0
	cb.state = CircuitClosed
}

func (cb *CircuitBreaker) onFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.failureCount++
	cb.lastFailureTime = time.Now()
	if cb.failureCount >= circuitBreakerFailureThreshold {
		cb.state = CircuitOpen
	}
}

// sharedHTTPClient is initialised once via initializeTimeouts.
var sharedHTTPClient *http.Client

// initializeTimeouts configures sharedHTTPClient from cfg.Timeouts.
func initializeTimeouts(cfg *Config) {
	sharedHTTPClient = &http.Client{
		Timeout: time.Duration(cfg.Timeouts.HTTPClient) * time.Second,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 20,
			IdleConnTimeout:     time.Duration(cfg.Timeouts.IdleConnTimeout) * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   time.Duration(cfg.Timeouts.DialTimeout) * time.Second,
				KeepAlive: time.Duration(cfg.Timeouts.KeepAlive) * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: time.Duration(cfg.Timeouts.TLSHandshake) * time.Second,
		},
	}
}

// bufferPool reuses 32 KB slices for response copying.
var bufferPool = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 32*1024)
		return &b
	},
}

// WorkerPool dispatches jobs across a fixed goroutine pool.
type WorkerPool struct {
	workers  int
	jobQueue chan func()
	quit     chan bool
	wg       sync.WaitGroup
}

func NewWorkerPool(workers int) *WorkerPool {
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	wp := &WorkerPool{
		workers:  workers,
		jobQueue: make(chan func(), workers*2),
		quit:     make(chan bool),
	}
	wp.start()
	return wp
}

func (wp *WorkerPool) start() {
	for i := 0; i < wp.workers; i++ {
		wp.wg.Add(1)
		go func() {
			defer wp.wg.Done()
			for {
				select {
				case job := <-wp.jobQueue:
					job()
				case <-wp.quit:
					return
				}
			}
		}()
	}
}

func (wp *WorkerPool) Submit(job func()) { wp.jobQueue <- job }
func (wp *WorkerPool) Stop() {
	close(wp.quit)
	wp.wg.Wait()
}

var globalWorkerPool = NewWorkerPool(runtime.NumCPU() * 2)

// coalescingEntry holds a pending or completed coalesced request.
type coalescingEntry struct {
	done   chan struct{}
	result interface{}
}

// CoalescingCache collapses identical concurrent requests into one upstream call.
type CoalescingCache struct {
	requests map[string]*coalescingEntry
	mutex    sync.Mutex
}

func NewCoalescingCache() *CoalescingCache {
	return &CoalescingCache{requests: make(map[string]*coalescingEntry)}
}

func (cc *CoalescingCache) getRequestKey(method, url string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte(url))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func (cc *CoalescingCache) CoalesceRequest(key string, fn func() interface{}) interface{} {
	cc.mutex.Lock()
	if entry, exists := cc.requests[key]; exists {
		cc.mutex.Unlock()
		<-entry.done
		return entry.result
	}
	entry := &coalescingEntry{done: make(chan struct{})}
	cc.requests[key] = entry
	cc.mutex.Unlock()

	entry.result = fn()
	close(entry.done)

	cc.mutex.Lock()
	delete(cc.requests, key)
	cc.mutex.Unlock()
	return entry.result
}

// isRetriableError returns true for transient HTTP/network errors.
// 429 is NOT retried here — "quota exceeded" is permanent for the session;
// "slow_down" (Retry-After) should be handled at a higher layer if needed.
func isRetriableError(statusCode int, err error) bool {
	if err != nil {
		return true
	}
	return statusCode >= 500 || statusCode == 408
}

// makeRequestWithRetry executes req with exponential back-off retry.
// body is used to re-create the request body on each attempt.
func makeRequestWithRetry(client *http.Client, req *http.Request, body []byte) (*http.Response, error) {
	var lastResp *http.Response
	var lastErr error
	ctx := req.Context()

	for attempt := 1; attempt <= maxChatRetries; attempt++ {
		retryReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), bytes.NewBuffer(body))
		if err != nil {
			return nil, err
		}
		for key, values := range req.Header {
			for _, value := range values {
				retryReq.Header.Add(key, value)
			}
		}
		log.Printf("Upstream attempt %d/%d → %s %s", attempt, maxChatRetries, retryReq.Method, retryReq.URL.String())
		start := time.Now()

		resp, err := client.Do(retryReq)
		elapsed := time.Since(start)
		if err != nil {
			log.Printf("Upstream attempt %d/%d FAILED after %s: %v", attempt, maxChatRetries, elapsed, err)
			lastErr = err
			if attempt == maxChatRetries {
				return nil, err
			}
			backoff := time.Duration(baseChatRetryDelay*attempt*attempt) * time.Second
			log.Printf("Retrying in %s...", backoff)
			time.Sleep(backoff)
			continue
		}

		lastResp = resp
		log.Printf("Upstream attempt %d/%d → HTTP %d (%s, Content-Type: %s)",
			attempt, maxChatRetries, resp.StatusCode, elapsed, resp.Header.Get("Content-Type"))
		if !isRetriableError(resp.StatusCode, nil) {
			return resp, nil
		}
		log.Printf("Upstream returned retriable status %d, attempt %d/%d", resp.StatusCode, attempt, maxChatRetries)
		if attempt == maxChatRetries {
			return resp, nil
		}
		resp.Body.Close()
		backoff := time.Duration(baseChatRetryDelay*attempt*attempt) * time.Second
		log.Printf("Retrying in %s...", backoff)
		time.Sleep(backoff)
	}
	return lastResp, lastErr
}

// responseWrapper tracks whether headers have been sent to avoid duplicate writes.
type responseWrapper struct {
	http.ResponseWriter
	headersSent bool
}

func (rw *responseWrapper) WriteHeader(statusCode int) {
	if !rw.headersSent {
		rw.headersSent = true
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

func (rw *responseWrapper) Write(data []byte) (int, error) {
	if !rw.headersSent {
		rw.headersSent = true
	}
	return rw.ResponseWriter.Write(data)
}

// Flush implements http.Flusher so that streaming SSE responses work
// correctly through the responseWrapper middleware.
func (rw *responseWrapper) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// streamResponse copies the upstream response to w, flushing for SSE streams.
func streamResponse(w http.ResponseWriter, resp *http.Response) error {
	copyHeaders(w, resp.Header)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.WriteHeader(resp.StatusCode)

	if resp.Header.Get("Content-Type") == "text/event-stream" {
		if flusher, ok := w.(http.Flusher); ok {
			buf := make([]byte, 1024)
			for {
				n, err := resp.Body.Read(buf)
				if n > 0 {
					if _, werr := w.Write(buf[:n]); werr != nil {
						return werr
					}
					flusher.Flush()
				}
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
			}
		}
	}
	bufPtr := bufferPool.Get().(*[]byte)
	defer bufferPool.Put(bufPtr)
	_, err := io.CopyBuffer(w, resp.Body, *bufPtr)
	return err
}

// copyHeaders copies headers from src to w, skipping any named in skip.
func copyHeaders(w http.ResponseWriter, src http.Header, skip ...string) {
	skipSet := make(map[string]bool, len(skip))
	for _, s := range skip {
		skipSet[strings.ToLower(s)] = true
	}
	for key, values := range src {
		if skipSet[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

// capabilityFromPath maps a request path to a Capability.
func capabilityFromPath(path string) (Capability, error) {
	switch {
	case strings.Contains(path, "/chat/completions"):
		return CapabilityChat, nil
	case strings.Contains(path, "/responses"):
		return CapabilityResponses, nil
	case strings.Contains(path, "/embeddings"):
		return CapabilityEmbeddings, nil
	default:
		return "", fmt.Errorf("unsupported path: %s", path)
	}
}

// proxyHandler is the HTTP handler factory for proxied API paths.
func proxyHandler(registry *ProviderRegistry, router *ModelRouter, cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(cfg.Timeouts.ProxyContext)*time.Second)
		defer cancel()

		r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024)
		rw := &responseWrapper{ResponseWriter: w}
		done := make(chan error, 1)

		globalWorkerPool.Submit(func() {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("Worker panic: %v", rec)
					done <- fmt.Errorf("internal server error")
				}
			}()
			done <- processProxyRequest(registry, router, cfg, rw, r, ctx)
		})

		select {
		case err := <-done:
			if err != nil && !rw.headersSent {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		case <-ctx.Done():
			if !rw.headersSent {
				http.Error(w, "Request timeout", http.StatusRequestTimeout)
			}
		}
	}
}

// processProxyRequest resolves the route and delegates to the provider.
func processProxyRequest(
	registry *ProviderRegistry,
	router *ModelRouter,
	cfg *Config,
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
) error {
	reqStart := time.Now()
	cap, err := capabilityFromPath(r.URL.Path)
	if err != nil {
		log.Printf("[REQ] %s %s → unsupported path", r.Method, r.URL.Path)
		return err
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[REQ] %s %s → body read error: %v", r.Method, r.URL.Path, err)
		return fmt.Errorf("reading request body: %w", err)
	}
	defer r.Body.Close()

	requestedModel := extractModelFromBody(body)
	log.Printf("[REQ] %s %s model=%q capability=%s body_size=%d from=%s",
		r.Method, r.URL.Path, requestedModel, cap, len(body), r.RemoteAddr)

	if cap == CapabilityResponses {
		return processResponsesRequest(registry, router, cfg, w, r, ctx, body, requestedModel, reqStart)
	}

	if cap == CapabilityChat {
		_, prefixProvider, hasPrefix := StripModelPrefix(requestedModel)
		if !hasPrefix || prefixProvider == ProviderCopilot {
			if copilot, err := registry.Get(ProviderCopilot); err == nil {
				if freeChatProvider, ok := copilot.(FreeChatProxyProvider); ok {
					log.Printf("[REQ] Chat path ignoring client model=%q and delegating selection to Copilot free-model rotation", requestedModel)
					proxyErr := freeChatProvider.ProxyFreeChatRequest(ctx, w, r, body, requestedModel)
					elapsed := time.Since(reqStart)
					if proxyErr != nil {
						log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s ERROR: %v",
							r.Method, r.URL.Path, requestedModel, copilot.ID(), elapsed, proxyErr)
					} else {
						log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s OK",
							r.Method, r.URL.Path, requestedModel, copilot.ID(), elapsed)
					}
					return proxyErr
				}
			}
		}
	}

	route, err := router.Resolve(ctx, requestedModel, cap)
	if err != nil {
		log.Printf("[REQ] %s %s model=%q → routing FAILED: %v", r.Method, r.URL.Path, requestedModel, err)
		return fmt.Errorf("routing: %w", err)
	}

	if route.ResolvedModel != requestedModel {
		log.Printf("[REQ] model rewritten: %q → %q", requestedModel, route.ResolvedModel)
		body = patchBodyModel(body, route.ResolvedModel)
	}

	log.Printf("[REQ] Routing %s %s model=%q → provider=%s upstream_model=%q",
		r.Method, r.URL.Path, requestedModel, route.Provider.ID(), route.ResolvedModel)

	proxyErr := route.Provider.ProxyRequest(ctx, w, r, body, cap)
	elapsed := time.Since(reqStart)
	if proxyErr != nil {
		log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s ERROR: %v",
			r.Method, r.URL.Path, requestedModel, route.Provider.ID(), elapsed, proxyErr)
	} else {
		log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s OK",
			r.Method, r.URL.Path, requestedModel, route.Provider.ID(), elapsed)
	}
	return proxyErr
}

func processResponsesRequest(
	registry *ProviderRegistry,
	router *ModelRouter,
	_ *Config,
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
	body []byte,
	requestedModel string,
	reqStart time.Time,
) error {
	if err := validateResponsesRequest(body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"message":%q,"type":"invalid_request_error","code":"unsupported_responses_feature"}}`, err.Error())))
		log.Printf("[REQ] %s %s model=%q → invalid responses request: %v", r.Method, r.URL.Path, requestedModel, err)
		return nil
	}

	route, err := router.Resolve(ctx, requestedModel, CapabilityChat)
	if err != nil {
		log.Printf("[REQ] %s %s model=%q → responses routing FAILED: %v", r.Method, r.URL.Path, requestedModel, err)
		return fmt.Errorf("routing: %w", err)
	}

	if route.ResolvedModel != requestedModel {
		log.Printf("[REQ] responses model rewritten: %q → %q", requestedModel, route.ResolvedModel)
		body = patchBodyModel(body, route.ResolvedModel)
	}

	log.Printf("[REQ] Routing %s %s model=%q → provider=%s upstream_model=%q (responses)",
		r.Method, r.URL.Path, requestedModel, route.Provider.ID(), route.ResolvedModel)

	body = sanitizeResponsesBodyForProvider(body, route.Provider.ID())

	if responsesProvider, ok := route.Provider.(ResponsesProxyProvider); ok {
		proxyErr := responsesProvider.ProxyResponsesRequest(ctx, w, r, body, requestedModel)
		elapsed := time.Since(reqStart)
		if proxyErr != nil {
			log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s ERROR: %v",
				r.Method, r.URL.Path, requestedModel, route.Provider.ID(), elapsed, proxyErr)
		} else {
			log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s OK",
				r.Method, r.URL.Path, requestedModel, route.Provider.ID(), elapsed)
		}
		return proxyErr
	}

	chatBody, streaming, err := buildChatCompletionsRequestFromResponses(body)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":{"message":%q,"type":"invalid_request_error","code":"unsupported_responses_feature"}}`, err.Error())))
		return nil
	}
	chatBody = sanitizeChatBodyForProvider(chatBody, route.Provider.ID())

	compatReq := r.Clone(ctx)
	compatReq.URL.Path = "/v1/chat/completions"
	compatReq.Body = io.NopCloser(bytes.NewReader(chatBody))

	rr := httptestResponseRecorder{header: make(http.Header)}
	proxyErr := route.Provider.ProxyRequest(ctx, &rr, compatReq, chatBody, CapabilityChat)
	if proxyErr != nil {
		elapsed := time.Since(reqStart)
		log.Printf("[REQ] COMPLETED %s %s model=%q provider=%s elapsed=%s ERROR: %v",
			r.Method, r.URL.Path, requestedModel, route.Provider.ID(), elapsed, proxyErr)
		return proxyErr
	}

	return writeResponsesCompatibilityResult(w, &rr, streaming)
}

func validateResponsesRequest(body []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	for _, field := range []string{"previous_response_id", "conversation", "include", "metadata"} {
		if _, ok := raw[field]; ok {
			return fmt.Errorf("field %q is not supported on this proxy yet", field)
		}
	}
	return nil
}

type httptestResponseRecorder struct {
	header http.Header
	body   bytes.Buffer
	code   int
}

func (r *httptestResponseRecorder) Header() http.Header { return r.header }
func (r *httptestResponseRecorder) Write(data []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(data)
}
func (r *httptestResponseRecorder) WriteHeader(statusCode int) { r.code = statusCode }

func writeResponsesCompatibilityResult(w http.ResponseWriter, rr *httptestResponseRecorder, streaming bool) error {
	if rr.code == 0 {
		rr.code = http.StatusOK
	}
	if streaming {
		return writeResponsesSSEFromChat(w, rr.code, rr.header, rr.body.Bytes())
	}
	return writeResponsesJSONFromChat(w, rr.code, rr.header, rr.body.Bytes())
}
