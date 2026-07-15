// proxy.go — HTTP proxy infrastructure (circuit breaker, retry, worker pool,
// request coalescing) and the provider-dispatching request handler.
package yarouter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	requestproxy "github.com/duvu/ya-router/internal/proxy"
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
	probeInFlight   bool
	mutex           sync.Mutex
}

func (cb *CircuitBreaker) canExecute() bool {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.state = CircuitHalfOpen
			cb.probeInFlight = true
			return true
		}
		return false
	case CircuitHalfOpen:
		if cb.probeInFlight {
			return false
		}
		cb.probeInFlight = true
		return true
	default:
		return false
	}
}

func (cb *CircuitBreaker) onSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.failureCount = 0
	cb.state = CircuitClosed
	cb.probeInFlight = false
}

func (cb *CircuitBreaker) onFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	if cb.state == CircuitHalfOpen {
		cb.failureCount = circuitBreakerFailureThreshold
	} else {
		cb.failureCount++
	}
	cb.lastFailureTime = time.Now()
	cb.probeInFlight = false
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
	workers        int
	jobQueue       chan func()
	spaceAvailable chan struct{}
	quit           chan struct{}
	wg             sync.WaitGroup
	stopOnce       sync.Once
	stateMu        sync.Mutex
	stopped        bool
}

func NewWorkerPool(workers int) *WorkerPool {
	if workers <= 0 {
		workers = runtime.NumCPU() * 2
	}
	wp := &WorkerPool{
		workers:        workers,
		jobQueue:       make(chan func(), workers*2),
		spaceAvailable: make(chan struct{}, 1),
		quit:           make(chan struct{}),
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
					select {
					case wp.spaceAvailable <- struct{}{}:
					default:
					}
					if job != nil {
						job()
					}
				case <-wp.quit:
					return
				}
			}
		}()
	}
}

func (wp *WorkerPool) Submit(job func()) bool {
	return wp.SubmitContext(context.Background(), job)
}

// SubmitContext applies bounded backpressure and stops waiting when the caller
// is cancelled. No job is accepted after Stop begins.
func (wp *WorkerPool) SubmitContext(ctx context.Context, job func()) bool {
	if wp == nil || job == nil {
		return false
	}
	for {
		wp.stateMu.Lock()
		if wp.stopped {
			wp.stateMu.Unlock()
			return false
		}
		select {
		case wp.jobQueue <- job:
			wp.stateMu.Unlock()
			return true
		default:
			wp.stateMu.Unlock()
		}
		select {
		case <-ctx.Done():
			return false
		case <-wp.quit:
			return false
		case <-wp.spaceAvailable:
		}
	}
}

func (wp *WorkerPool) Stop() {
	if wp == nil {
		return
	}
	wp.stopOnce.Do(func() {
		wp.stateMu.Lock()
		wp.stopped = true
		close(wp.quit)
		wp.stateMu.Unlock()
	})
	wp.wg.Wait()
}

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
// Account quota errors are handled by provider-specific account failover.
func isRetriableError(statusCode int, err error) bool {
	if err != nil {
		return true
	}
	return statusCode >= 500 || statusCode == http.StatusRequestTimeout
}

// makeRequestWithRetry executes a request with bounded retries. Unsafe methods
// are retried only when the caller supplies an Idempotency-Key, preventing a
// duplicate model generation after an uncertain delivery.
func makeRequestWithRetry(client *http.Client, req *http.Request, body []byte) (*http.Response, error) {
	var lastResp *http.Response
	var lastErr error
	ctx := req.Context()
	safeMethod := req.Method == http.MethodGet || req.Method == http.MethodHead
	retryAllowed := safeMethod || req.Header.Get("Idempotency-Key") != ""

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
		started := time.Now()
		resp, err := client.Do(retryReq)
		elapsed := time.Since(started)
		if err != nil {
			lastErr = err
			log.Printf("Upstream attempt %d/%d failed after %s: %v", attempt, maxChatRetries, elapsed, err)
			if !retryAllowed || attempt == maxChatRetries {
				return nil, err
			}
			backoff := time.Duration(baseChatRetryDelay*attempt*attempt) * time.Second
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			continue
		}

		lastResp = resp
		log.Printf("Upstream attempt %d/%d → HTTP %d (%s, Content-Type: %s)",
			attempt, maxChatRetries, resp.StatusCode, elapsed, resp.Header.Get("Content-Type"))
		if !isRetriableError(resp.StatusCode, nil) || !retryAllowed || attempt == maxChatRetries {
			return resp, nil
		}
		resp.Body.Close()
		backoff := time.Duration(baseChatRetryDelay*attempt*attempt) * time.Second
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}
	return lastResp, lastErr
}

// responseWrapper tracks committed status and response size.
type responseWrapper struct {
	http.ResponseWriter
	headersSent  bool
	statusCode   int
	bytesWritten int64
}

func (rw *responseWrapper) WriteHeader(statusCode int) {
	if !rw.headersSent {
		rw.headersSent = true
		rw.statusCode = statusCode
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

func (rw *responseWrapper) Write(data []byte) (int, error) {
	if !rw.headersSent {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(data)
	rw.bytesWritten += int64(n)
	return n, err
}

func (rw *responseWrapper) StatusCode() int {
	if rw.statusCode == 0 && rw.headersSent {
		return http.StatusOK
	}
	return rw.statusCode
}

// Flush implements http.Flusher so SSE responses work through middleware.
func (rw *responseWrapper) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// streamResponse copies the upstream response to w, flushing SSE streams.
func streamResponse(w http.ResponseWriter, resp *http.Response) error {
	copyHeaders(w, resp.Header)
	w.WriteHeader(resp.StatusCode)

	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		if flusher, ok := w.(http.Flusher); ok {
			buf := make([]byte, 32*1024)
			for {
				n, err := resp.Body.Read(buf)
				if n > 0 {
					if _, writeErr := w.Write(buf[:n]); writeErr != nil {
						return writeErr
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
	return requestproxy.CapabilityFromPath(path)
}

// proxyHandler is the HTTP handler factory for proxied API paths.
func proxyHandler(registry *ProviderRegistry, router *ModelRouter, cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(cfg.Timeouts.ProxyContext)*time.Second)
		defer cancel()

		r.Body = http.MaxBytesReader(w, r.Body, 5*1024*1024)
		rw := &responseWrapper{ResponseWriter: w}
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("proxy panic: %v", recovered)
				if !rw.headersSent {
					writeOpenAIError(rw, http.StatusInternalServerError,
						newProviderError("", ProviderErrorTransport, http.StatusInternalServerError, false, "internal server error"))
				}
			}
		}()
		// net/http already executes handlers concurrently. Keeping provider work on
		// this goroutine ensures ResponseWriter is never used after ServeHTTP
		// returns and makes the request context the sole lifetime boundary.
		err := processProxyRequest(registry, router, cfg, rw, r, ctx)
		if err == nil && ctx.Err() != nil && !rw.headersSent {
			err = ctx.Err()
		}
		if err != nil && !rw.headersSent {
			status := providerErrorStatus(err)
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
			}
			writeOpenAIError(rw, status, err)
		}
	}
}

// processProxyRequest resolves a route and delegates to the selected provider.
func processProxyRequest(
	registry *ProviderRegistry,
	router *ModelRouter,
	cfg *Config,
	w http.ResponseWriter,
	r *http.Request,
	ctx context.Context,
) error {
	_ = cfg
	reqStart := time.Now()
	capability, err := capabilityFromPath(r.URL.Path)
	if err != nil {
		return newProviderError("", ProviderErrorInvalidRequest, http.StatusNotFound, false, "%v", err)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return newProviderError("", ProviderErrorInvalidRequest, http.StatusBadRequest, false, "reading request body: %v", err)
	}
	defer r.Body.Close()

	requestedModel := extractModelFromBody(body)
	log.Printf("[REQ] %s %s model=%q capability=%s body_size=%d from=%s",
		r.Method, r.URL.Path, requestedModel, capability, len(body), r.RemoteAddr)

	route, err := router.Resolve(ctx, requestedModel, capability)
	if err != nil {
		log.Printf("[REQ] %s %s model=%q → routing failed: %v", r.Method, r.URL.Path, requestedModel, err)
		return newProviderError("", ProviderErrorInvalidRequest, http.StatusBadRequest, false, "routing: %v", err)
	}

	if route.ResolvedModel != requestedModel {
		log.Printf("[REQ] model rewritten: %q → %q", requestedModel, route.ResolvedModel)
		body = patchBodyModel(body, route.ResolvedModel)
	}

	log.Printf("[REQ] routing %s %s model=%q → provider=%s upstream_model=%q",
		r.Method, r.URL.Path, requestedModel, route.Provider.ID(), route.ResolvedModel)

	proxyErr := route.Provider.ProxyRequest(ctx, w, r, body, capability)

	elapsed := time.Since(reqStart)
	status := responseStatus(w)
	if proxyErr != nil {
		log.Printf("[REQ] completed %s %s model=%q provider=%s status=%d elapsed=%s error=%v",
			r.Method, r.URL.Path, requestedModel, route.Provider.ID(), status, elapsed, proxyErr)
	} else {
		log.Printf("[REQ] completed %s %s model=%q provider=%s status=%d elapsed=%s",
			r.Method, r.URL.Path, requestedModel, route.Provider.ID(), status, elapsed)
	}
	return proxyErr
}
