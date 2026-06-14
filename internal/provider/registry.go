// registry.go — ProviderRegistry holds and dispatches to all configured providers.
package provider

import (
	"fmt"
	"sync"
)

// ProviderRegistry holds all configured providers in registration order.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[ProviderID]Provider
	order     []ProviderID // preserves insertion order for deterministic iteration
}

// NewProviderRegistry returns an empty ProviderRegistry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[ProviderID]Provider),
	}
}

// Register adds or replaces a provider in the registry.
func (r *ProviderRegistry) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[p.ID()]; !exists {
		r.order = append(r.order, p.ID())
	}
	r.providers[p.ID()] = p
}

// Get returns the provider with the given id, or an error if not registered.
func (r *ProviderRegistry) Get(id ProviderID) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[id]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", id)
	}
	return p, nil
}

// All returns all registered providers in registration order.
func (r *ProviderRegistry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Provider, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.providers[id])
	}
	return out
}
