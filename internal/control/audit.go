package control

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// AuditEvent intentionally excludes request headers, bodies, query strings,
// credentials, device codes, and upstream account identifiers.
type AuditEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Actor     string    `json:"actor"`
	Role      Role      `json:"role,omitempty"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Result    string    `json:"result"`
	Status    int       `json:"status"`
	RequestID string    `json:"request_id"`
}

type AuditSink interface {
	Record(AuditEvent)
}

type AuditSinkFunc func(AuditEvent)

func (function AuditSinkFunc) Record(event AuditEvent) { function(event) }

type MultiAuditSink []AuditSink

func (sinks MultiAuditSink) Record(event AuditEvent) {
	for _, sink := range sinks {
		if sink != nil {
			sink.Record(event)
		}
	}
}

// MemoryAuditSink is a bounded, concurrency-safe audit foundation. Durable
// retention is added with production packaging; this sink is also useful for
// security tests and future read-only audit resources.
type MemoryAuditSink struct {
	mu     sync.RWMutex
	limit  int
	events []AuditEvent
}

func NewMemoryAuditSink(limit int) *MemoryAuditSink {
	if limit < 1 {
		limit = 1024
	}
	return &MemoryAuditSink{limit: limit}
}

func (sink *MemoryAuditSink) Record(event AuditEvent) {
	if sink == nil {
		return
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.events) == sink.limit {
		copy(sink.events, sink.events[1:])
		sink.events = sink.events[:sink.limit-1]
	}
	sink.events = append(sink.events, event)
}

func (sink *MemoryAuditSink) Snapshot() []AuditEvent {
	if sink == nil {
		return nil
	}
	sink.mu.RLock()
	defer sink.mu.RUnlock()
	return append([]AuditEvent(nil), sink.events...)
}

// LogAuditSink emits one structured, redacted JSON object per control request.
type LogAuditSink struct{}

func (LogAuditSink) Record(event AuditEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		log.Printf("control audit encoding failed: %v", err)
		return
	}
	log.Printf("control_audit=%s", payload)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	if recorder.status != 0 {
		return
	}
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func (recorder *statusRecorder) Write(payload []byte) (int, error) {
	if recorder.status == 0 {
		recorder.WriteHeader(http.StatusOK)
	}
	return recorder.ResponseWriter.Write(payload)
}

func (recorder *statusRecorder) Flush() {
	if recorder.status == 0 {
		recorder.WriteHeader(http.StatusOK)
	}
	if flusher, ok := recorder.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func auditMiddleware(sink AuditSink, now func() time.Time, next http.Handler) http.Handler {
	if now == nil {
		now = time.Now
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		recorder := &statusRecorder{ResponseWriter: writer}
		next.ServeHTTP(recorder, request)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		identity, _ := IdentityFromContext(request.Context())
		result := "success"
		if status >= 400 {
			result = "denied"
		}
		if status >= 500 {
			result = "failed"
		}
		if sink != nil {
			sink.Record(AuditEvent{
				Timestamp: now().UTC(),
				Actor:     identity.Subject,
				Role:      identity.Role,
				Action:    request.Method,
				Target:    request.URL.Path,
				Result:    result,
				Status:    status,
				RequestID: RequestIDFromContext(request.Context()),
			})
		}
	})
}
