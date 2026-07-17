package yarouter

import (
	"context"
	"net/http"
	"time"
)

// mockProvider implements the Provider interface for testing.
type mockProvider struct {
	id     ProviderID
	name   string
	caps   []Capability
	health ProviderHealth
	models *ModelList

	// lastKnown, when set, exposes a network-free last-known-good catalog for
	// umbrella availability evaluation.
	lastKnown      []string
	lastKnownStale bool
	allowed        []string

	proxyFunc func(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, cap Capability) error
}

func (m *mockProvider) ID() ProviderID             { return m.id }
func (m *mockProvider) Name() string               { return m.name }
func (m *mockProvider) Capabilities() []Capability { return m.caps }

func (m *mockProvider) EnsureAuthenticated(_ context.Context) error {
	return nil
}

func (m *mockProvider) ListModels(_ context.Context) (*ModelList, error) {
	return m.models, nil
}

func (m *mockProvider) ProxyRequest(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	cap Capability,
) error {
	if m.proxyFunc != nil {
		return m.proxyFunc(ctx, w, r, body, cap)
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

func (m *mockProvider) Health(_ context.Context) ProviderHealth {
	return m.health
}

func (m *mockProvider) LastKnownModels() ([]string, time.Time, bool, bool) {
	if m.lastKnown == nil {
		return nil, time.Time{}, false, false
	}
	return m.lastKnown, time.Unix(1000, 0), m.lastKnownStale, true
}

func (m *mockProvider) AllowedModelIDs() []string { return m.allowed }
