package routing

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/duvu/ya-router/internal/api"
	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
)

// umbrellaProvider is a controllable mock exposing the optional availability
// interfaces (LastKnownModels, AllowedModelIDs) and counting proxy calls.
type umbrellaProvider struct {
	id            provider.ID
	capabilities  []provider.Capability
	authenticated bool
	catalog       []string
	catalogStale  bool
	allowed       []string
	hasCatalog    bool

	mu         sync.Mutex
	proxyCalls int
	proxyErr   error
}

func (p *umbrellaProvider) ID() provider.ID { return p.id }
func (p *umbrellaProvider) Name() string    { return string(p.id) }
func (p *umbrellaProvider) Capabilities() []provider.Capability {
	if p.capabilities == nil {
		return []provider.Capability{provider.CapabilityChat}
	}
	return p.capabilities
}
func (p *umbrellaProvider) EnsureAuthenticated(context.Context) error { return nil }
func (p *umbrellaProvider) Health(context.Context) provider.Health {
	return provider.Health{Authenticated: p.authenticated}
}
func (p *umbrellaProvider) ListModels(context.Context) (*api.ModelList, error) {
	data := make([]api.Model, 0, len(p.catalog))
	for _, id := range p.catalog {
		data = append(data, api.Model{ID: id})
	}
	return &api.ModelList{Object: "list", Data: data}, nil
}
func (p *umbrellaProvider) ProxyRequest(context.Context, http.ResponseWriter, *http.Request, []byte, provider.Capability) error {
	p.mu.Lock()
	p.proxyCalls++
	p.mu.Unlock()
	return p.proxyErr
}

func (p *umbrellaProvider) LastKnownModels() ([]string, time.Time, bool, bool) {
	if !p.hasCatalog {
		return nil, time.Time{}, false, false
	}
	return p.catalog, time.Unix(1000, 0), p.catalogStale, true
}
func (p *umbrellaProvider) AllowedModelIDs() []string { return p.allowed }

func (p *umbrellaProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.proxyCalls
}

func umbrellaRouting(targets ...string) configschema.Routing {
	return configschema.Routing{
		VirtualModels: map[string]configschema.VirtualModel{
			"router/auto": {Strategy: configschema.VirtualModelStrategyPriority, Targets: targets},
		},
	}
}

func TestResolveUmbrellaSelectsFirstActive(t *testing.T) {
	copilot := &umbrellaProvider{id: provider.Copilot, authenticated: true, hasCatalog: true, catalog: []string{"gpt-5-mini"}}
	codex := &umbrellaProvider{id: provider.Codex, authenticated: true, hasCatalog: true, catalog: []string{"gpt-5.4-mini"}}
	registry := provider.NewRegistry(copilot, codex)
	router := NewRouter(registry, umbrellaRouting("github/gpt-5-mini", "codex/gpt-5.4-mini"))

	result, err := router.Resolve(context.Background(), "router/auto", provider.CapabilityChat)
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider.ID() != provider.Copilot {
		t.Fatalf("provider = %s, want copilot", result.Provider.ID())
	}
	if result.ResolvedModel != "gpt-5-mini" {
		t.Fatalf("resolved model = %q, want gpt-5-mini (bare)", result.ResolvedModel)
	}
	if result.Selection == nil || result.Selection.SelectedTarget != "github/gpt-5-mini" {
		t.Fatalf("selection = %+v", result.Selection)
	}
}

func TestResolveUmbrellaFallsThroughToSecondTarget(t *testing.T) {
	// Copilot is unauthenticated → not ready; Codex should be selected.
	copilot := &umbrellaProvider{id: provider.Copilot, authenticated: false, hasCatalog: true, catalog: []string{"gpt-5-mini"}}
	codex := &umbrellaProvider{id: provider.Codex, authenticated: true, hasCatalog: true, catalog: []string{"gpt-5.4-mini"}}
	registry := provider.NewRegistry(copilot, codex)
	router := NewRouter(registry, umbrellaRouting("github/gpt-5-mini", "codex/gpt-5.4-mini"))

	result, err := router.Resolve(context.Background(), "router/auto", provider.CapabilityChat)
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider.ID() != provider.Codex || result.ResolvedModel != "gpt-5.4-mini" {
		t.Fatalf("result=%+v", result)
	}
	if result.Selection.SelectedIndex != 1 {
		t.Fatalf("selected index = %d, want 1", result.Selection.SelectedIndex)
	}
}

func TestResolveUmbrellaNoActiveTarget(t *testing.T) {
	copilot := &umbrellaProvider{id: provider.Copilot, authenticated: false, hasCatalog: true, catalog: []string{"gpt-5-mini"}}
	registry := provider.NewRegistry(copilot)
	router := NewRouter(registry, umbrellaRouting("github/gpt-5-mini", "codex/gpt-5.4-mini"))

	_, err := router.Resolve(context.Background(), "router/auto", provider.CapabilityChat)
	var noTarget *NoActiveTargetError
	if !errors.As(err, &noTarget) {
		t.Fatalf("expected NoActiveTargetError, got %v", err)
	}
	if len(noTarget.Skipped) != 2 {
		t.Fatalf("skipped %d, want 2", len(noTarget.Skipped))
	}
}

// TestResolveUmbrellaSingleDispatchOnFailure proves that when the selected
// target's upstream fails, no other target's provider receives the request.
func TestResolveUmbrellaSingleDispatchOnFailure(t *testing.T) {
	copilot := &umbrellaProvider{id: provider.Copilot, authenticated: true, hasCatalog: true, catalog: []string{"gpt-5-mini"}, proxyErr: errors.New("429 rate limited")}
	codex := &umbrellaProvider{id: provider.Codex, authenticated: true, hasCatalog: true, catalog: []string{"gpt-5.4-mini"}}
	registry := provider.NewRegistry(copilot, codex)
	router := NewRouter(registry, umbrellaRouting("github/gpt-5-mini", "codex/gpt-5.4-mini"))

	result, err := router.Resolve(context.Background(), "router/auto", provider.CapabilityChat)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the proxy dispatching to the selected provider exactly once.
	dispatchErr := result.Provider.ProxyRequest(context.Background(), nil, nil, nil, provider.CapabilityChat)
	if dispatchErr == nil {
		t.Fatal("expected upstream error from selected target")
	}
	if copilot.calls() != 1 {
		t.Fatalf("copilot proxy calls = %d, want 1", copilot.calls())
	}
	if codex.calls() != 0 {
		t.Fatalf("codex proxy calls = %d, want 0 (no failover)", codex.calls())
	}
}

// TestResolveUmbrellaReselectionAfterStateChange proves a later request can
// select a newly active target once availability changes.
func TestResolveUmbrellaReselectionAfterStateChange(t *testing.T) {
	copilot := &umbrellaProvider{id: provider.Copilot, authenticated: false, hasCatalog: true, catalog: []string{"gpt-5-mini"}}
	codex := &umbrellaProvider{id: provider.Codex, authenticated: true, hasCatalog: true, catalog: []string{"gpt-5.4-mini"}}
	registry := provider.NewRegistry(copilot, codex)
	router := NewRouter(registry, umbrellaRouting("github/gpt-5-mini", "codex/gpt-5.4-mini"))

	first, err := router.Resolve(context.Background(), "router/auto", provider.CapabilityChat)
	if err != nil {
		t.Fatal(err)
	}
	if first.Provider.ID() != provider.Codex {
		t.Fatalf("first selection provider = %s, want codex", first.Provider.ID())
	}

	// Copilot becomes healthy; the next request must prefer it (priority order).
	copilot.authenticated = true
	second, err := router.Resolve(context.Background(), "router/auto", provider.CapabilityChat)
	if err != nil {
		t.Fatal(err)
	}
	if second.Provider.ID() != provider.Copilot {
		t.Fatalf("second selection provider = %s, want copilot", second.Provider.ID())
	}
}

// TestResolveExplicitRoutesUnaffected proves umbrella config does not change
// model_map, prefix, or bare-discovery routing.
func TestResolveExplicitRoutesUnaffected(t *testing.T) {
	copilot := &umbrellaProvider{id: provider.Copilot, authenticated: true, hasCatalog: true, catalog: []string{"gpt-5-mini"}}
	codex := &umbrellaProvider{id: provider.Codex, authenticated: true, hasCatalog: true, catalog: []string{"gpt-5.4-mini"}}
	registry := provider.NewRegistry(copilot, codex)
	routing := umbrellaRouting("github/gpt-5-mini", "codex/gpt-5.4-mini")
	routing.ModelMap = map[string]configschema.ModelMapEntry{
		"research": {Provider: string(provider.Codex), UpstreamModel: "gpt-5.4-mini"},
	}
	router := NewRouter(registry, routing)

	// Explicit prefix stays pinned to its provider.
	prefixed, err := router.Resolve(context.Background(), "codex/gpt-5.4-mini", provider.CapabilityChat)
	if err != nil || prefixed.Provider.ID() != provider.Codex || prefixed.Selection != nil {
		t.Fatalf("prefixed route wrong: %+v err=%v", prefixed, err)
	}

	// model_map wins and is not an umbrella decision.
	mapped, err := router.Resolve(context.Background(), "research", provider.CapabilityChat)
	if err != nil || mapped.Provider.ID() != provider.Codex || mapped.Selection != nil {
		t.Fatalf("model_map route wrong: %+v err=%v", mapped, err)
	}
}
