package provider

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/duvu/ya-router/internal/api"
)

type testProvider struct{ id ID }

func (p *testProvider) ID() ID                                    { return p.id }
func (p *testProvider) Name() string                              { return string(p.id) }
func (p *testProvider) Capabilities() []Capability                { return []Capability{CapabilityChat} }
func (p *testProvider) EnsureAuthenticated(context.Context) error { return nil }
func (p *testProvider) ListModels(context.Context) (*api.ModelList, error) {
	return &api.ModelList{}, nil
}
func (p *testProvider) Health(context.Context) Health { return Health{Authenticated: true} }
func (p *testProvider) ProxyRequest(context.Context, http.ResponseWriter, *http.Request, []byte, Capability) error {
	return nil
}

func TestRegistryConstructionIsIsolated(t *testing.T) {
	first := NewRegistry()
	second := NewRegistry()
	first.Register(&testProvider{id: Copilot})
	if len(first.All()) != 1 {
		t.Fatal("first registry lost its provider")
	}
	if len(second.All()) != 0 {
		t.Fatal("registry construction leaked state")
	}
}

func TestRegistryConcurrentAccess(t *testing.T) {
	registry := NewRegistry()
	providers := []*testProvider{{id: Copilot}, {id: Codex}, {id: Kilo}}
	var wait sync.WaitGroup
	for i := 0; i < 64; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			registered := providers[index%len(providers)]
			registry.Register(registered)
			_, _ = registry.Get(registered.ID())
			_ = registry.All()
		}(i)
	}
	wait.Wait()
	if len(registry.All()) != len(providers) {
		t.Fatalf("providers=%d", len(registry.All()))
	}
}

func TestFrozenRegistryRejectsMutation(t *testing.T) {
	registry := NewRegistry(&testProvider{id: Copilot})
	registry.Freeze()
	if !registry.Frozen() {
		t.Fatal("registry did not report frozen state")
	}
	if err := registry.Register(&testProvider{id: Codex}); err != ErrRegistryFrozen {
		t.Fatalf("frozen Register error = %v", err)
	}
	if _, _, err := registry.Unregister(Copilot); err != ErrRegistryFrozen {
		t.Fatalf("frozen Unregister error = %v", err)
	}
}
