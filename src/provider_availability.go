// provider_availability.go exposes network-free, last-known-good catalog and
// allowlist views used by umbrella-model availability evaluation. These methods
// never trigger an upstream fetch: they read only cached state.
package yarouter

import "time"

// lastKnownModelIDs returns the bare model IDs from a model cache's
// last-known-good snapshot without initiating a fetch.
func lastKnownModelIDs(cache *ModelCache) (models []string, fetchedAt time.Time, stale bool, ok bool) {
	list, fetched, isStale, present := cache.LastKnownGood()
	if !present || list == nil {
		return nil, time.Time{}, false, false
	}
	ids := make([]string, 0, len(list.Data))
	for _, model := range list.Data {
		bare, _, _ := StripModelPrefix(model.ID)
		ids = append(ids, bare)
	}
	return ids, fetched, isStale, true
}

// bareAllowedModels returns the configured allowlist as bare (prefix-stripped)
// model IDs. An empty result means "no explicit allowlist".
func bareAllowedModels(allowed []string) []string {
	if len(allowed) == 0 {
		return nil
	}
	ids := make([]string, 0, len(allowed))
	for _, entry := range allowed {
		bare, _, _ := StripModelPrefix(entry)
		ids = append(ids, bare)
	}
	return ids
}

// LastKnownModels reports the Copilot provider's last-known-good catalog.
func (p *CopilotProvider) LastKnownModels() ([]string, time.Time, bool, bool) {
	return lastKnownModelIDs(p.cache)
}

// AllowedModelIDs reports the Copilot provider's configured allowlist.
func (p *CopilotProvider) AllowedModelIDs() []string {
	return bareAllowedModels(p.cfg.Providers.Copilot.AllowedModels)
}

// LastKnownModels reports the Codex provider's last-known-good catalog.
func (p *CodexProvider) LastKnownModels() ([]string, time.Time, bool, bool) {
	return lastKnownModelIDs(p.cache)
}

// AllowedModelIDs reports the Codex provider's configured allowlist.
func (p *CodexProvider) AllowedModelIDs() []string {
	return bareAllowedModels(p.cfg.Providers.Codex.AllowedModels)
}

// LastKnownModels reports the Kilo provider's last-known-good catalog.
func (p *KiloProvider) LastKnownModels() ([]string, time.Time, bool, bool) {
	return lastKnownModelIDs(p.cache)
}

// AllowedModelIDs reports the Kilo provider's configured allowlist.
func (p *KiloProvider) AllowedModelIDs() []string {
	return bareAllowedModels(p.cfg.Providers.Kilo.AllowedModels)
}
