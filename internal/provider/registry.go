package provider

import (
	"errors"
	"fmt"
	"sync"
)

var ErrRegistryFrozen = errors.New("provider registry is frozen")

// Registry holds providers in deterministic registration order.
// Construction is side-effect free so tests and future runtime snapshots can
// own isolated registries.
type Registry struct {
	mu        sync.RWMutex
	providers map[ID]Provider
	order     []ID
	frozen    bool
}

// NewRegistry returns an isolated provider registry. Passing providers is a
// convenient way to build a registry that will remain private to a runtime
// snapshot.
func NewRegistry(providers ...Provider) *Registry {
	registry := &Registry{providers: make(map[ID]Provider)}
	for _, registered := range providers {
		_ = registry.Register(registered)
	}
	return registry
}

// Register adds or replaces a provider while retaining registration order.
func (r *Registry) Register(provider Provider) error {
	if provider == nil {
		return fmt.Errorf("provider is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrRegistryFrozen
	}
	if _, exists := r.providers[provider.ID()]; !exists {
		r.order = append(r.order, provider.ID())
	}
	r.providers[provider.ID()] = provider
	return nil
}

// Unregister removes one provider and reports whether it was registered.
// Runtime publication builds a new registry rather than mutating a registry
// already visible to requests; this method remains useful for isolated setup.
func (r *Registry) Unregister(id ID) (Provider, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return nil, false, ErrRegistryFrozen
	}
	registered, exists := r.providers[id]
	if !exists {
		return nil, false, nil
	}
	delete(r.providers, id)
	for index, orderedID := range r.order {
		if orderedID == id {
			r.order = append(r.order[:index], r.order[index+1:]...)
			break
		}
	}
	return registered, true, nil
}

// Freeze permanently prevents mutation. Runtime snapshots freeze their
// private registry before publication.
func (r *Registry) Freeze() {
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

func (r *Registry) Frozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// IDs returns a deterministic snapshot of registered provider IDs.
func (r *Registry) IDs() []ID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]ID(nil), r.order...)
}

// Get returns a provider by ID.
func (r *Registry) Get(id ID) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	registered, ok := r.providers[id]
	if !ok {
		return nil, fmt.Errorf("provider %q not registered", id)
	}
	return registered, nil
}

// All returns a registration-ordered snapshot of the providers.
func (r *Registry) All() []Provider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := make([]Provider, 0, len(r.order))
	for _, id := range r.order {
		providers = append(providers, r.providers[id])
	}
	return providers
}
