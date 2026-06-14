package main

import (
	"context"
	"net/http"
)

// mockProvider implements the Provider interface for testing.
type mockProvider struct {
	id     ProviderID
	name   string
	caps   []Capability
	health ProviderHealth
	models *ModelList

	// proxyFunc is called by ProxyRequest if set.
	proxyFunc func(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, cap Capability) error
	// freeChatProxyFunc is called by ProxyFreeChatRequest if set.
	freeChatProxyFunc func(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, requestedModel string) error
	// responsesProxyFunc is called by ProxyResponsesRequest if set.
	responsesProxyFunc func(ctx context.Context, w http.ResponseWriter, r *http.Request, body []byte, requestedModel string) error
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

func (m *mockProvider) ProxyFreeChatRequest(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	requestedModel string,
) error {
	if m.freeChatProxyFunc != nil {
		return m.freeChatProxyFunc(ctx, w, r, body, requestedModel)
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

func (m *mockProvider) ProxyResponsesRequest(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	body []byte,
	requestedModel string,
) error {
	if m.responsesProxyFunc != nil {
		return m.responsesProxyFunc(ctx, w, r, body, requestedModel)
	}
	w.WriteHeader(http.StatusOK)
	return nil
}
