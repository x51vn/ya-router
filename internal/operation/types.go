// Package operation owns persistent long-running control-plane work.
package operation

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const schemaVersion = 1

// State is the durable operation state machine.
type State string

const (
	StatePending        State = "pending"
	StateRunning        State = "running"
	StateWaitingForUser State = "waiting_for_user"
	StateSucceeded      State = "succeeded"
	StateFailed         State = "failed"
	StateCancelled      State = "cancelled"
	StateExpired        State = "expired"
)

func (state State) Terminal() bool {
	switch state {
	case StateSucceeded, StateFailed, StateCancelled, StateExpired:
		return true
	default:
		return false
	}
}

// RecoveryPolicy controls restart handling for non-terminal operations. The
// first release intentionally supports only fail/expire recovery. Provider
// adapters may create a replacement operation after reconnect; partial work is
// never silently resumed.
type RecoveryPolicy string

const (
	RecoveryFail   RecoveryPolicy = "fail"
	RecoveryExpire RecoveryPolicy = "expire"
)

// Failure is a typed, redacted failure safe for persistence and clients.
type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// Record is the public, secret-free operation resource.
type Record struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	Target         string            `json:"target,omitempty"`
	Owner          string            `json:"owner"`
	State          State             `json:"state"`
	Progress       int               `json:"progress"`
	Cancelable     bool              `json:"cancelable"`
	RecoveryPolicy RecoveryPolicy    `json:"recovery_policy"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	ExpiresAt      time.Time         `json:"expires_at"`
	Sequence       uint64            `json:"sequence"`
	Failure        *Failure          `json:"failure,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	Result         map[string]string `json:"result,omitempty"`
}

// Event is a bounded, persisted operation lifecycle event.
type Event struct {
	Sequence    uint64    `json:"sequence"`
	OperationID string    `json:"operation_id"`
	Type        string    `json:"type"`
	State       State     `json:"state"`
	Timestamp   time.Time `json:"timestamp"`
}

// CreateRequest is the daemon-internal creation contract.
type CreateRequest struct {
	Kind           string
	Target         string
	Owner          string
	Cancelable     bool
	ExpiresAt      time.Time
	RecoveryPolicy RecoveryPolicy
	Metadata       map[string]string
	IdempotencyKey string
	RequestDigest  string
}

// Options configures bounded durable operation storage.
type Options struct {
	Path          string
	MaxOperations int
	MaxEvents     int
	Retention     time.Duration
	DefaultTTL    time.Duration
	Now           func() time.Time
}

type IdempotencyConflictError struct{}

func (*IdempotencyConflictError) Error() string {
	return "operation idempotency key was reused with a different request"
}

type NotFoundError struct{ ID string }

func (err *NotFoundError) Error() string { return fmt.Sprintf("operation %q was not found", err.ID) }

type InvalidTransitionError struct {
	ID   string
	From State
	To   State
}

func (err *InvalidTransitionError) Error() string {
	return fmt.Sprintf("operation %q cannot transition from %s to %s", err.ID, err.From, err.To)
}

type CapacityError struct{}

func (*CapacityError) Error() string { return "operation store capacity is exhausted" }

var safeCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// NewFailure sanitizes caller-controlled failure text. Raw upstream errors are
// never persisted by the operation layer.
func NewFailure(code, _ string, retryable bool) Failure {
	code = strings.TrimSpace(code)
	if !safeCode.MatchString(code) {
		code = "operation_failed"
	}
	return Failure{Code: code, Message: failureMessage(code), Retryable: retryable}
}

func failureMessage(code string) string {
	switch code {
	case "operation_cancelled":
		return "The operation was cancelled."
	case "operation_expired":
		return "The operation expired."
	case "operation_interrupted":
		return "The operation was interrupted."
	case "operation_panic":
		return "The operation failed unexpectedly."
	case "progress_failed":
		return "Operation progress could not be persisted."
	default:
		return "The operation failed."
	}
}

func copyRecord(source Record) Record {
	copied := source
	if source.Failure != nil {
		failure := *source.Failure
		copied.Failure = &failure
	}
	copied.Metadata = copyStringMap(source.Metadata)
	copied.Result = copyStringMap(source.Result)
	return copied
}

func copyStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}
