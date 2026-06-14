// httputil.go — shared HTTP infrastructure: circuit breaker, retry, worker pool,
// request coalescing, response streaming, and shared HTTP client.
package httputil

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/x51vn/github-copilot-svcs/internal/config"
)

const (
	maxChatRetries     = 3
	baseChatRetryDelay = 1 // seconds

	CircuitBreakerFailureThreshold = 5
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

// NewCircuitBreaker creates a CircuitBreaker with the given timeout.
func NewCircuitBreaker(timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:   CircuitClosed,
		timeout: timeout,
	}
}

func (cb *CircuitBreaker) CanExecute() bool {
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

func (cb *CircuitBreaker) OnSuccess() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.failureCount = 0
	cb.state = CircuitClosed
}

func (cb *CircuitBreaker) OnFailure() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.failureCount++
	cb.lastFailureTime = time.Now()
	if cb.failureCount >= CircuitBreakerFailureThreshold {
		cb.state = CircuitOpen
	}
}

// SharedHTTPClient is initialised once via InitializeTimeouts.
var SharedHTTPClient *http.Client

// InitializeTimeouts configures SharedHTTPClient from cfg.Timeouts.
func InitializeTimeouts(cfg *config.Config) {
	SharedHTTPClient = &http.Client{
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

var GlobalWorkerPool = NewWorkerPool(runtime.NumCPU() * 2)

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

func (cc *CoalescingCache) GetRequestKey(method, url string, body []byte) string {
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

// IsRetriableError returns true for transient HTTP/network errors.
func IsRetriableError(statusCode int, err error) bool {
	if err != nil {
		return true
	}
	return statusCode >= 500 || statusCode == 408
}

// MakeRequestWithRetry executes req with exponential back-off retry.
// body is used to re-create the request body on each attempt.
func MakeRequestWithRetry(client *http.Client, req *http.Request, body []byte) (*http.Response, error) {
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
		if !IsRetriableError(resp.StatusCode, nil) {
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

// ResponseWrapper tracks whether headers have been sent to avoid duplicate writes.
type ResponseWrapper struct {
	http.ResponseWriter
	HeadersSent bool
}

func (rw *ResponseWrapper) WriteHeader(statusCode int) {
	if !rw.HeadersSent {
		rw.HeadersSent = true
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

func (rw *ResponseWrapper) Write(data []byte) (int, error) {
	if !rw.HeadersSent {
		rw.HeadersSent = true
	}
	return rw.ResponseWriter.Write(data)
}

// Flush implements http.Flusher so that streaming SSE responses work
// correctly through the ResponseWrapper middleware.
func (rw *ResponseWrapper) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// StreamResponse copies the upstream response to w, flushing for SSE streams.
func StreamResponse(w http.ResponseWriter, resp *http.Response) error {
	CopyHeaders(w, resp.Header)
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

// CopyHeaders copies headers from src to w, skipping any named in skip.
func CopyHeaders(w http.ResponseWriter, src http.Header, skip ...string) {
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

// NewCircuitBreakerWithState creates a CircuitBreaker with explicit initial state and timeout.
// Intended for test helpers that need to construct a breaker in a specific state.
func NewCircuitBreakerWithState(state CircuitBreakerState, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{state: state, timeout: timeout}
}
