package control

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const IdempotencyKeyHeader = "Idempotency-Key"

// IdempotencyStore serializes duplicate mutation attempts and replays the
// original sanitized HTTP result for an identical key and payload.
type IdempotencyStore struct {
	mu         sync.Mutex
	entries    map[string]*idempotencyEntry
	maxEntries int
	ttl        time.Duration
	now        func() time.Time
}

type idempotencyEntry struct {
	digest    string
	createdAt time.Time
	ready     chan struct{}
	response  storedResponse
}

type storedResponse struct {
	status int
	header http.Header
	body   []byte
}

func NewIdempotencyStore(maxEntries int, ttl time.Duration) *IdempotencyStore {
	if maxEntries < 1 {
		maxEntries = 2048
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &IdempotencyStore{
		entries:    make(map[string]*idempotencyEntry),
		maxEntries: maxEntries,
		ttl:        ttl,
		now:        time.Now,
	}
}

func idempotencyMiddleware(store *IdempotencyStore, next http.Handler) http.Handler {
	if store == nil {
		store = NewIdempotencyStore(0, 0)
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		key := strings.TrimSpace(request.Header.Get(IdempotencyKeyHeader))
		if key == "" {
			writeError(writer, request, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required for this operation.", false, nil)
			return
		}
		if len(key) > 200 {
			writeError(writer, request, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is too long.", false, nil)
			return
		}
		var bodyReader io.Reader = request.Body
		if bodyReader == nil {
			bodyReader = http.NoBody
		}
		body, err := io.ReadAll(io.LimitReader(bodyReader, (4<<20)+1))
		if err != nil {
			writeError(writer, request, http.StatusBadRequest, "invalid_request_body", "The request body could not be read.", false, nil)
			return
		}
		if len(body) > 4<<20 {
			writeError(writer, request, http.StatusRequestEntityTooLarge, "request_body_too_large", "The request body exceeds the control-plane limit.", false, nil)
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		identity, _ := IdentityFromContext(request.Context())
		entryKey := identity.Subject + "\x00" + request.Method + "\x00" + request.URL.Path + "\x00" + key
		digest := requestDigest(request.Method, request.URL.Path, body)
		entry, owner, conflict := store.begin(entryKey, digest)
		if conflict {
			writeError(writer, request, http.StatusConflict, "idempotency_key_conflict", "The idempotency key was already used with a different request.", false, nil)
			return
		}
		if !owner {
			select {
			case <-entry.ready:
				replayResponse(writer, entry.response)
			case <-request.Context().Done():
				writeError(writer, request, http.StatusRequestTimeout, "idempotency_wait_cancelled", "Waiting for the original request was cancelled.", true, nil)
			}
			return
		}
		recorder := newBufferedResponse()
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Printf("control mutation panic request_id=%s", RequestIDFromContext(request.Context()))
					writeError(recorder, request, http.StatusInternalServerError, "internal_control_error", "The control operation failed unexpectedly.", true, nil)
				}
			}()
			next.ServeHTTP(recorder, request)
		}()
		response := recorder.snapshot()
		store.complete(entryKey, entry, response)
		replayResponse(writer, response)
	})
}

func requestDigest(method, path string, body []byte) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%s\x00", method, path)
	_, _ = hash.Write(body)
	return hex.EncodeToString(hash.Sum(nil))
}

func (store *IdempotencyStore) begin(key, digest string) (*idempotencyEntry, bool, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.cleanupLocked()
	if entry, exists := store.entries[key]; exists {
		if entry.digest != digest {
			return entry, false, true
		}
		return entry, false, false
	}
	if len(store.entries) >= store.maxEntries {
		store.evictOldestLocked()
	}
	entry := &idempotencyEntry{digest: digest, createdAt: store.now().UTC(), ready: make(chan struct{})}
	store.entries[key] = entry
	return entry, true, false
}

func (store *IdempotencyStore) complete(key string, entry *idempotencyEntry, response storedResponse) {
	store.mu.Lock()
	defer store.mu.Unlock()
	current, exists := store.entries[key]
	if !exists || current != entry {
		return
	}
	entry.response = response
	close(entry.ready)
}

func (store *IdempotencyStore) cleanupLocked() {
	threshold := store.now().Add(-store.ttl)
	for key, entry := range store.entries {
		if entry.createdAt.Before(threshold) {
			select {
			case <-entry.ready:
				delete(store.entries, key)
			default:
			}
		}
	}
}

func (store *IdempotencyStore) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for key, entry := range store.entries {
		select {
		case <-entry.ready:
			if oldestKey == "" || entry.createdAt.Before(oldest) {
				oldestKey = key
				oldest = entry.createdAt
			}
		default:
		}
	}
	if oldestKey != "" {
		delete(store.entries, oldestKey)
	}
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header)}
}

func (response *bufferedResponse) Header() http.Header { return response.header }

func (response *bufferedResponse) WriteHeader(status int) {
	if response.status == 0 {
		response.status = status
	}
}

func (response *bufferedResponse) Write(payload []byte) (int, error) {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	return response.body.Write(payload)
}

func (response *bufferedResponse) snapshot() storedResponse {
	status := response.status
	if status == 0 {
		status = http.StatusOK
	}
	return storedResponse{status: status, header: response.header.Clone(), body: append([]byte(nil), response.body.Bytes()...)}
}

func replayResponse(writer http.ResponseWriter, response storedResponse) {
	for key, values := range response.header {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	writer.WriteHeader(response.status)
	_, _ = writer.Write(response.body)
}
