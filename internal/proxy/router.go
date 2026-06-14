package proxy

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/x51vn/github-copilot-svcs/internal/config"
	"github.com/x51vn/github-copilot-svcs/internal/provider"
)

type RouteResult struct {
	Provider      provider.Provider
	ResolvedModel string
}

type ModelRouter struct {
	registry *provider.ProviderRegistry
	routing  config.RoutingConfig
}

func NewModelRouter(registry *provider.ProviderRegistry, routing config.RoutingConfig) *ModelRouter {
	if routing.ModelMap == nil {
		routing.ModelMap = make(map[string]config.ModelMapEntry)
	}
	return &ModelRouter{registry: registry, routing: routing}
}

func (r *ModelRouter) Resolve(ctx context.Context, requestedModel string, cap provider.Capability) (*RouteResult, error) {
	model := requestedModel
	usedDefaultModel := model == ""
	if model == "" {
		log.Printf("[router] no model in request, using default=%q", r.routing.DefaultModel)
		model = r.routing.DefaultModel
	}

	bareModel, prefixProvider, hasPrefx := provider.StripModelPrefix(model)

	mapKey := model
	entry, ok := r.routing.ModelMap[mapKey]
	if !ok && hasPrefx {
		mapKey = bareModel
		entry, ok = r.routing.ModelMap[mapKey]
	}
	if ok {
		p, err := r.registry.Get(provider.ProviderID(entry.Provider))
		if err != nil {
			log.Printf("[router] model_map hit for %q → provider %q NOT FOUND", model, entry.Provider)
			return nil, fmt.Errorf("model %q maps to unknown provider %q", model, entry.Provider)
		}
		upstream := entry.UpstreamModel
		if upstream == "" {
			upstream = bareModel
		}
		log.Printf("[router] model_map hit: %q → provider=%s upstream=%q", model, p.ID(), upstream)
		return &RouteResult{Provider: p, ResolvedModel: upstream}, nil
	}

	if hasPrefx {
		return r.resolveWithPrefix(ctx, model, bareModel, prefixProvider, cap)
	}

	candidates := r.discoverFromCatalogs(ctx, model, cap)

	switch len(candidates) {
	case 0:
		if !usedDefaultModel {
			log.Printf("[router] model %q unavailable for capability=%s", model, cap)
			return nil, fmt.Errorf("model %q is unavailable for capability %q", model, cap)
		}
		log.Printf("[router] model %q not in any catalog, falling back to default provider", model)
		return r.defaultProviderRoute(model, cap)
	case 1:
		log.Printf("[router] catalog match: %q → provider=%s", model, candidates[0].Provider.ID())
		return &candidates[0], nil
	default:
		providerNames := make([]string, len(candidates))
		for i, c := range candidates {
			providerNames[i] = string(c.Provider.ID())
		}
		log.Printf("[router] model %q AMBIGUOUS: found in providers %v — use a prefixed model ID (e.g. gc-%s or oc-%s) or add a routing.model_map entry", model, providerNames, model, model)
		return nil, fmt.Errorf(
			"model %q is ambiguous: found in %d providers %v; use a provider-prefixed model ID (e.g. gc-%s, oc-%s) or add a routing.model_map entry",
			model, len(candidates), providerNames, model, model,
		)
	}
}

func (r *ModelRouter) resolveWithPrefix(ctx context.Context, fullModel, bareModel string, provID provider.ProviderID, cap provider.Capability) (*RouteResult, error) {
	p, err := r.registry.Get(provID)
	if err != nil {
		return nil, fmt.Errorf("model %q: provider %q (from prefix) is not registered", fullModel, provID)
	}
	if !hasCapability(p, cap) {
		return nil, fmt.Errorf("model %q: provider %q does not support capability %q", fullModel, provID, cap)
	}
	models, listErr := p.ListModels(ctx)
	if listErr == nil && models != nil {
		for _, m := range models.Data {
			if strings.EqualFold(m.ID, bareModel) {
				log.Printf("[router] prefix-routed: %q → provider=%s upstream=%q", fullModel, p.ID(), bareModel)
				return &RouteResult{Provider: p, ResolvedModel: bareModel}, nil
			}
		}
	}
	log.Printf("[router] model %q not found in %s catalog", bareModel, provID)
	return nil, fmt.Errorf("model %q is unavailable for capability %q from provider %q", fullModel, cap, provID)
}

func (r *ModelRouter) discoverFromCatalogs(ctx context.Context, model string, cap provider.Capability) []RouteResult {
	var found []RouteResult
	for _, p := range r.registry.All() {
		if !hasCapability(p, cap) {
			continue
		}
		models, err := p.ListModels(ctx)
		if err != nil || models == nil {
			continue
		}
		for _, m := range models.Data {
			if strings.EqualFold(m.ID, model) {
				found = append(found, RouteResult{Provider: p, ResolvedModel: model})
				break
			}
		}
	}
	return found
}

func (r *ModelRouter) defaultProviderRoute(model string, cap provider.Capability) (*RouteResult, error) {
	defID := provider.ProviderID(r.routing.DefaultProvider)
	if defID == "" {
		defID = provider.ProviderCopilot
	}
	p, err := r.registry.Get(defID)
	if err != nil {
		return nil, fmt.Errorf("model %q not found in any provider and default provider %q is unavailable", model, defID)
	}
	if !hasCapability(p, cap) {
		return nil, fmt.Errorf("default provider %q does not support capability %q", defID, cap)
	}
	return &RouteResult{Provider: p, ResolvedModel: model}, nil
}

func hasCapability(p provider.Provider, cap provider.Capability) bool {
	for _, c := range p.Capabilities() {
		if c == cap {
			return true
		}
	}
	return false
}
