// Package routing resolves requested models to providers without depending on
// provider implementations or HTTP handlers.
package routing

import (
	"context"
	"fmt"
	"log"
	"strings"

	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
)

// Result is the resolved routing decision for a request.
type Result struct {
	Provider      provider.Provider
	ResolvedModel string
}

// Router maps a requested model and capability to a provider.
type Router struct {
	registry *provider.Registry
	routing  configschema.Routing
}

// NewRouter builds an isolated model router over registry.
func NewRouter(registry *provider.Registry, config configschema.Routing) *Router {
	if config.ModelMap == nil {
		config.ModelMap = make(map[string]configschema.ModelMapEntry)
	}
	return &Router{registry: registry, routing: config}
}

// Resolve applies model-map, provider-prefix, catalog, then default routing.
func (r *Router) Resolve(ctx context.Context, requestedModel string, capability provider.Capability) (*Result, error) {
	model := requestedModel
	usedDefaultModel := model == ""
	if model == "" {
		log.Printf("[router] no model in request, using default=%q", r.routing.DefaultModel)
		model = r.routing.DefaultModel
	}

	bareModel, prefixProvider, hasPrefix := StripModelPrefix(model)
	entry, mapped := r.routing.ModelMap[model]
	if !mapped && hasPrefix {
		entry, mapped = r.routing.ModelMap[bareModel]
	}
	if mapped {
		registered, err := r.registry.Get(provider.ID(entry.Provider))
		if err != nil {
			return nil, fmt.Errorf("model %q maps to unknown provider %q", model, entry.Provider)
		}
		if !hasCapability(registered, capability) {
			return nil, fmt.Errorf("model %q maps to provider %q, which does not support capability %q", model, registered.ID(), capability)
		}
		upstream := entry.UpstreamModel
		if upstream == "" {
			upstream = bareModel
		}
		log.Printf("[router] model_map hit: %q → provider=%s upstream=%q", model, registered.ID(), upstream)
		return &Result{Provider: registered, ResolvedModel: upstream}, nil
	}

	if hasPrefix {
		return r.resolveWithPrefix(ctx, model, bareModel, prefixProvider, capability)
	}

	candidates := r.discoverFromCatalogs(ctx, model, capability)
	switch len(candidates) {
	case 0:
		if !usedDefaultModel {
			return nil, fmt.Errorf("model %q is unavailable for capability %q", model, capability)
		}
		return r.defaultProviderRoute(model, capability)
	case 1:
		log.Printf("[router] catalog match: %q → provider=%s", model, candidates[0].Provider.ID())
		return &candidates[0], nil
	default:
		providers := make([]string, len(candidates))
		for i := range candidates {
			providers[i] = string(candidates[i].Provider.ID())
		}
		return nil, fmt.Errorf("model %q is ambiguous: found in %d providers %v; use a provider-prefixed model ID or add routing.model_map", model, len(candidates), providers)
	}
}

func (r *Router) resolveWithPrefix(ctx context.Context, fullModel, bareModel string, providerID provider.ID, capability provider.Capability) (*Result, error) {
	registered, err := r.registry.Get(providerID)
	if err != nil {
		return nil, fmt.Errorf("model %q: provider %q from prefix is not registered", fullModel, providerID)
	}
	if !hasCapability(registered, capability) {
		return nil, fmt.Errorf("model %q: provider %q does not support capability %q", fullModel, providerID, capability)
	}
	models, listErr := registered.ListModels(ctx)
	if listErr == nil && models != nil {
		for _, model := range models.Data {
			if strings.EqualFold(model.ID, bareModel) {
				log.Printf("[router] prefix-routed: %q → provider=%s upstream=%q", fullModel, registered.ID(), bareModel)
				return &Result{Provider: registered, ResolvedModel: bareModel}, nil
			}
		}
	}
	return nil, fmt.Errorf("model %q is unavailable for capability %q from provider %q", fullModel, capability, providerID)
}

func (r *Router) discoverFromCatalogs(ctx context.Context, model string, capability provider.Capability) []Result {
	var found []Result
	for _, registered := range r.registry.All() {
		if !hasCapability(registered, capability) {
			continue
		}
		models, err := registered.ListModels(ctx)
		if err != nil || models == nil {
			continue
		}
		for _, candidate := range models.Data {
			if strings.EqualFold(candidate.ID, model) {
				found = append(found, Result{Provider: registered, ResolvedModel: model})
				break
			}
		}
	}
	return found
}

func (r *Router) defaultProviderRoute(model string, capability provider.Capability) (*Result, error) {
	providerID := provider.ID(r.routing.DefaultProvider)
	if providerID == "" {
		providerID = provider.Copilot
	}
	registered, err := r.registry.Get(providerID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found and default provider %q is unavailable", model, providerID)
	}
	if !hasCapability(registered, capability) {
		return nil, fmt.Errorf("default provider %q does not support capability %q", providerID, capability)
	}
	return &Result{Provider: registered, ResolvedModel: model}, nil
}

func hasCapability(registered provider.Provider, capability provider.Capability) bool {
	for _, supported := range registered.Capabilities() {
		if supported == capability {
			return true
		}
	}
	return false
}
