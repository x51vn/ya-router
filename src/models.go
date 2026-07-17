// models.go — unified /v1/models handler and shared model utilities.
package yarouter

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// filterAllowedModels returns a copy of modelList containing only models that
// appear in allowedModels.  An empty (nil or zero-length) allowedModels slice
// means "allow all".
//
// The filter is prefix-tolerant: a model ID with a provider prefix matches an
// allowed entry that specifies only the bare ID, and vice versa.  For example,
// an allowed list entry "gpt-4o" will match the model "github/gpt-4o", and an
// entry "github/gpt-4o" will match that prefixed model exactly.
//
// If the allowedModels list is set but no models match, a synthetic entry for
// each allowed ID is returned so clients can still see the allowed model names.
func filterAllowedModels(modelList *ModelList, allowedModels []string) *ModelList {
	if len(allowedModels) == 0 {
		return modelList
	}

	var filtered []Model
	for _, m := range modelList.Data {
		if isModelAllowedWithPrefix(m.ID, allowedModels) {
			filtered = append(filtered, m)
		}
	}

	if len(filtered) == 0 {
		// Synthesise entries for each allowed ID so callers aren't left empty.
		for _, id := range allowedModels {
			filtered = append(filtered, Model{
				ID:      id,
				Object:  "model",
				Created: time.Now().Unix(),
				OwnedBy: "openai",
			})
		}
	}
	return &ModelList{Object: "list", Data: filtered}
}

func shouldRefreshModels(r *http.Request) bool {
	val := strings.TrimSpace(r.URL.Query().Get("refresh"))
	return val == "1" || strings.EqualFold(val, "true") || strings.EqualFold(val, "yes") || strings.EqualFold(val, "on")
}

func invalidateProvidersModelCache(registry *ProviderRegistry) {
	type cacheAware interface {
		InvalidateModelCache()
	}
	for _, p := range registry.All() {
		if cache, ok := p.(cacheAware); ok {
			cache.InvalidateModelCache()
		}
	}
}

// isModelAllowedWithPrefix reports whether modelID is permitted by the
// allowedModels list, comparing both the full ID and the bare (prefix-stripped)
// ID against each allowed entry (also checked bare and prefixed).
func isModelAllowedWithPrefix(modelID string, allowedModels []string) bool {
	if len(allowedModels) == 0 {
		return true
	}
	bareID, _, _ := StripModelPrefix(modelID)
	for _, allowed := range allowedModels {
		bareAllowed, _, _ := StripModelPrefix(allowed)
		if strings.EqualFold(modelID, allowed) ||
			strings.EqualFold(bareID, allowed) ||
			strings.EqualFold(modelID, bareAllowed) ||
			strings.EqualFold(bareID, bareAllowed) {
			return true
		}
	}
	return false
}

// modelsHandler returns an HTTP handler that merges model lists from all
// enabled providers and writes the combined list as JSON.
//
// Each model ID is prefixed with the provider's namespace prefix (for example,
// "github/", "codex/", or "kilo/") so clients can target a provider
// deterministically.  The prefix is stripped by the router before any request
// is forwarded to the upstream.
func modelsHandler(registry *ProviderRegistry, cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		if shouldRefreshModels(r) {
			invalidateProvidersModelCache(registry)
		}

		seen := make(map[string]bool)
		var allModels []Model

		// 1. Collect models from each provider's upstream API and apply prefix.
		//    Call EnsureAuthenticated first to give providers a chance
		//    to refresh expired tokens before we check Health.
		for _, p := range registry.All() {
			if err := p.EnsureAuthenticated(ctx); err != nil {
				log.Printf("modelsHandler: provider %s auth: %v", p.ID(), err)
			}
			h := p.Health(ctx)
			if !h.Authenticated && !h.CanRefresh && !cfg.Routing.ShowUnavailableModels {
				continue
			}
			ml, err := p.ListModels(ctx)
			if err != nil {
				log.Printf("modelsHandler: provider %s list error: %v", p.ID(), err)
				continue
			}
			if ml != nil {
				ownedBy := ProviderOwnedBy(p.ID())
				for _, m := range ml.Data {
					prefixedID := AddModelPrefix(p.ID(), m.ID)
					if !seen[prefixedID] {
						seen[prefixedID] = true
						allModels = append(allModels, Model{
							ID:      prefixedID,
							Object:  m.Object,
							Created: m.Created,
							OwnedBy: ownedBy,
						})
					}
				}
			}
		}

		// 2. Ensure every model in routing.model_map is represented.
		//    Derive OwnedBy from the entry's target provider.
		for modelID, entry := range cfg.Routing.ModelMap {
			if !seen[modelID] {
				seen[modelID] = true
				ownedBy := ProviderOwnedBy(ProviderID(entry.Provider))
				allModels = append(allModels, Model{
					ID:      modelID,
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: ownedBy,
				})
			}
		}

		// 3. Expose each configured umbrella (virtual) model once, owned by
		//    ya-router. The catalog does not claim it is a specific upstream
		//    model; the router selects a concrete target per request.
		for modelID := range cfg.Routing.VirtualModels {
			if !seen[modelID] {
				seen[modelID] = true
				allModels = append(allModels, Model{
					ID:      modelID,
					Object:  "model",
					Created: time.Now().Unix(),
					OwnedBy: "ya-router",
				})
			}
		}

		for _, alias := range discoverClaudeAliases(registry, cfg.Routing.ClaudeAliases, seen) {
			seen[alias.ID] = true
			allModels = append(allModels, alias)
		}

		resp := &ModelList{Object: "list", Data: allModels}
		if allModels == nil {
			resp.Data = []Model{}
		}
		log.Printf("modelsHandler: returning %d models", len(resp.Data))

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("modelsHandler: encode error: %v", err)
		}
	}
}

func discoverClaudeAliases(registry *ProviderRegistry, aliases map[string]string, known map[string]bool) []Model {
	models := make([]Model, 0, len(aliases))
	for alias, target := range aliases {
		if known[alias] || !known[target] || !isClaudeDiscoveryID(alias) {
			continue
		}
		_, providerID, prefixed := StripModelPrefix(target)
		if !prefixed {
			continue
		}
		provider, err := registry.Get(providerID)
		if err != nil || !providerSupportsResponses(provider) {
			continue
		}
		models = append(models, Model{ID: alias, Object: "model", Created: time.Now().Unix(), OwnedBy: "ya-router"})
	}
	return models
}

func isClaudeDiscoveryID(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(model, "claude") || strings.HasPrefix(model, "anthropic")
}

func providerSupportsResponses(provider Provider) bool {
	for _, capability := range provider.Capabilities() {
		if capability == CapabilityResponses {
			return true
		}
	}
	return false
}
