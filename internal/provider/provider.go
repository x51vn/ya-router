// Package provider defines the narrow data-plane provider contract and its
// concurrency-safe registry. Provider-specific auth and transport stay in
// their implementations.
package provider

import (
	"context"
	"net/http"
	"time"

	"github.com/duvu/ya-router/internal/api"
)

// ID is the machine-readable identifier for a backend provider.
type ID string

const (
	Copilot ID = "copilot"
	Codex   ID = "codex"
	Kilo    ID = "kilo"
)

// Capability describes a request type that a provider may support.
type Capability string

const (
	CapabilityChat       Capability = "chat"
	CapabilityResponses  Capability = "responses"
	CapabilityEmbeddings Capability = "embeddings"
)

// Health summarises a provider's current operational state.
type Health struct {
	Authenticated bool      `json:"authenticated"`
	CanRefresh    bool      `json:"can_refresh"`
	LastError     string    `json:"last_error,omitempty"`
	LastRefreshAt time.Time `json:"last_refresh_at,omitempty"`
}

// Provider is the abstraction every backend implementation must satisfy.
type Provider interface {
	ID() ID
	Name() string
	Capabilities() []Capability
	EnsureAuthenticated(ctx context.Context) error
	ListModels(ctx context.Context) (*api.ModelList, error)
	ProxyRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, capability Capability) error
	Health(ctx context.Context) Health
}
