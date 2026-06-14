// Package main provides the GitHub Copilot proxy service.
// provider.go defines the Provider interface and shared types.
package provider

import (
	"context"
	"net/http"
	"time"

	"github.com/x51vn/github-copilot-svcs/internal/types"
)

// ProviderID is the machine-readable identifier for a backend provider.
type ProviderID string

const (
	ProviderCopilot ProviderID = "copilot"
	ProviderCodex   ProviderID = "codex"
)

// Capability describes a request type that a provider may support.
type Capability string

const (
	CapabilityChat       Capability = "chat"
	CapabilityEmbeddings Capability = "embeddings"
	CapabilityResponses  Capability = "responses"
)

// ProviderHealth summarises a provider's current operational state.
type ProviderHealth struct {
	Authenticated bool      `json:"authenticated"`
	CanRefresh    bool      `json:"can_refresh"`
	LastError     string    `json:"last_error,omitempty"`
	LastRefreshAt time.Time `json:"last_refresh_at,omitempty"`
}

// Provider is the abstraction every backend implementation must satisfy.
// Auth, model discovery, routing, header policy, and retry policy live
// inside each provider implementation; proxy.go must not know about
// provider-specific details.
type Provider interface {
	// ID returns the machine-readable identifier for this provider.
	ID() ProviderID

	// Name returns the human-readable display name.
	Name() string

	// Capabilities lists the request types this provider supports.
	Capabilities() []Capability

	// EnsureAuthenticated ensures the provider has a valid credential,
	// refreshing or re-authenticating as needed.
	EnsureAuthenticated(ctx context.Context) error

	// ListModels returns the filtered list of models for this provider.
	// Implementations must apply provider-specific allowed_models filtering.
	ListModels(ctx context.Context) (*types.ModelList, error)

	// ProxyRequest executes a proxied request for the given capability
	// and writes the full response to w.  body is the request payload
	// with the model field already resolved by the router.
	ProxyRequest(
		ctx context.Context,
		w http.ResponseWriter,
		r *http.Request,
		body []byte,
		capability Capability,
	) error

	// Health returns the provider's current health snapshot.
	Health(ctx context.Context) ProviderHealth
}

// FreeChatProxyProvider is an optional interface for providers that want to
// own chat-model selection themselves instead of relying on router.Resolve.
type FreeChatProxyProvider interface {
	ProxyFreeChatRequest(
		ctx context.Context,
		w http.ResponseWriter,
		r *http.Request,
		body []byte,
		requestedModel string,
	) error
}

// ResponsesProxyProvider is an optional interface for providers that can
// handle /v1/responses natively instead of going through chat-compat mode.
type ResponsesProxyProvider interface {
	ProxyResponsesRequest(
		ctx context.Context,
		w http.ResponseWriter,
		r *http.Request,
		body []byte,
		requestedModel string,
	) error
}
