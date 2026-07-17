// Package availability provides a concurrency-safe, redacted source of truth
// for whether one canonical provider/model target is currently routable for a
// requested capability. It holds no provider objects, credentials, prompts, or
// arbitrary upstream error strings; reads never perform network I/O.
//
// Umbrella-model selection (see internal/routing) evaluates targets against an
// immutable Snapshot built from the runtime provider registry, provider health,
// and the last-known-good model catalog. Catalog refresh remains owned by the
// provider/cache layer: a Snapshot only reflects state that was already
// observed.
package availability

import (
	"sort"
	"strings"
	"time"

	"github.com/duvu/ya-router/internal/provider"
)

// Reason is a stable, bounded diagnostic code explaining why a target is or is
// not routable. Reason values never contain provider-supplied strings.
type Reason string

const (
	// ReasonRoutable indicates the target is active for the capability.
	ReasonRoutable Reason = "routable"
	// ReasonProviderNotRegistered indicates the target's provider is absent
	// from the runtime snapshot.
	ReasonProviderNotRegistered Reason = "provider_not_registered"
	// ReasonTargetDisabled indicates the provider is disabled by configuration.
	ReasonTargetDisabled Reason = "target_disabled"
	// ReasonProviderNotReady indicates the provider health state is not ready.
	ReasonProviderNotReady Reason = "provider_not_ready"
	// ReasonCapabilityUnsupported indicates the provider does not support the
	// requested capability.
	ReasonCapabilityUnsupported Reason = "capability_unsupported"
	// ReasonModelDisallowed indicates provider allowlist/entitlement policy
	// forbids the target model.
	ReasonModelDisallowed Reason = "model_disallowed"
	// ReasonModelNotInCatalog indicates the last-known-good catalog does not
	// contain the target model.
	ReasonModelNotInCatalog Reason = "model_not_in_catalog"
	// ReasonCatalogStale indicates the model exists in the catalog but the
	// catalog is older than the acceptable freshness window; the conservative
	// v1 policy treats a stale catalog as not routable.
	ReasonCatalogStale Reason = "catalog_stale"
	// ReasonCooldown indicates recent bounded feedback made a target temporarily
	// unavailable for later automatic-routing requests.
	ReasonCooldown Reason = "cooldown"
)

// CooldownReason classifies the bounded upstream outcome that started a
// temporary target cooldown. It never contains an upstream error message.
type CooldownReason string

const (
	CooldownRateLimited       CooldownReason = "rate_limited"
	CooldownEntitlementDenied CooldownReason = "entitlement_denied"
	CooldownAuthRequired      CooldownReason = "auth_required"
	CooldownTransientFailure  CooldownReason = "transient_failure"
	CooldownTimeout           CooldownReason = "timeout"
)

// Cooldown is one immutable, capability-scoped target cooldown supplied while
// building an availability snapshot.
type Cooldown struct {
	Model      string
	Capability provider.Capability
	Until      time.Time
	Reason     CooldownReason
}

// ProviderView is the immutable, redacted per-provider input used to build a
// Snapshot. Callers derive it from the runtime registry, the health registry,
// and the provider's last-known-good catalog. It intentionally contains no
// provider object, credential, or raw upstream data.
type ProviderView struct {
	ID           provider.ID
	Disabled     bool
	Ready        bool
	Capabilities []provider.Capability
	// AllowedModels is the provider's configured allowlist of bare model IDs.
	// An empty slice means "no explicit allowlist" (allow all catalog models).
	AllowedModels []string
	// CatalogPresent reports whether any last-known-good catalog exists.
	CatalogPresent bool
	// CatalogStale reports whether the last-known-good catalog is older than
	// the acceptable freshness window.
	CatalogStale bool
	// CatalogFetchedAt is when the last-known-good catalog was stored.
	CatalogFetchedAt time.Time
	// CatalogModels lists bare (unprefixed) model IDs from the last-known-good
	// catalog.
	CatalogModels []string
	Cooldowns     []Cooldown
}

type providerEntry struct {
	disabled         bool
	ready            bool
	capabilities     map[provider.Capability]struct{}
	allowed          map[string]struct{}
	hasAllowlist     bool
	catalogPresent   bool
	catalogStale     bool
	catalogFetchedAt time.Time
	models           map[string]struct{}
	cooldowns        map[string]Cooldown
}

// Snapshot is an immutable, atomically publishable availability view. All
// accessors are read-only and safe for concurrent use.
type Snapshot struct {
	generation uint64
	builtAt    time.Time
	providers  map[provider.ID]providerEntry
}

// TargetResult is the redacted evaluation of one target for one capability.
type TargetResult struct {
	Routable         bool
	Reason           Reason
	Generation       uint64
	CatalogFetchedAt time.Time
	CatalogStale     bool
	CooldownUntil    time.Time
	CooldownReason   CooldownReason
}

// NewSnapshot builds an immutable availability snapshot from provider views.
// generation identifies the runtime generation the views were derived from and
// builtAt is the observation timestamp used for diagnostics.
func NewSnapshot(generation uint64, builtAt time.Time, views []ProviderView) *Snapshot {
	providers := make(map[provider.ID]providerEntry, len(views))
	for _, view := range views {
		entry := providerEntry{
			disabled:         view.Disabled,
			ready:            view.Ready,
			capabilities:     make(map[provider.Capability]struct{}, len(view.Capabilities)),
			catalogPresent:   view.CatalogPresent,
			catalogStale:     view.CatalogStale,
			catalogFetchedAt: view.CatalogFetchedAt,
			models:           make(map[string]struct{}, len(view.CatalogModels)),
			cooldowns:        make(map[string]Cooldown, len(view.Cooldowns)),
		}
		for _, capability := range view.Capabilities {
			entry.capabilities[capability] = struct{}{}
		}
		for _, model := range view.CatalogModels {
			entry.models[normalizeModel(model)] = struct{}{}
		}
		for _, cooldown := range view.Cooldowns {
			if cooldown.Until.After(builtAt) {
				cooldown.Model = normalizeModel(cooldown.Model)
				entry.cooldowns[cooldownKey(cooldown.Model, cooldown.Capability)] = cooldown
			}
		}
		if len(view.AllowedModels) > 0 {
			entry.hasAllowlist = true
			entry.allowed = make(map[string]struct{}, len(view.AllowedModels))
			for _, model := range view.AllowedModels {
				entry.allowed[normalizeModel(model)] = struct{}{}
			}
		}
		providers[view.ID] = entry
	}
	return &Snapshot{generation: generation, builtAt: builtAt, providers: providers}
}

// Generation returns the runtime generation this snapshot was built from.
func (s *Snapshot) Generation() uint64 {
	if s == nil {
		return 0
	}
	return s.generation
}

// BuiltAt returns the observation timestamp of this snapshot.
func (s *Snapshot) BuiltAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.builtAt
}

// Evaluate reports whether the canonical target (providerID + bare model) is
// routable for the requested capability. It performs no I/O and never mutates
// snapshot state. The first failing condition determines the returned reason so
// results are deterministic for identical inputs.
func (s *Snapshot) Evaluate(providerID provider.ID, bareModel string, capability provider.Capability) TargetResult {
	result := TargetResult{}
	if s == nil {
		result.Reason = ReasonProviderNotRegistered
		return result
	}
	result.Generation = s.generation
	entry, ok := s.providers[providerID]
	if !ok {
		result.Reason = ReasonProviderNotRegistered
		return result
	}
	result.CatalogFetchedAt = entry.catalogFetchedAt
	result.CatalogStale = entry.catalogStale

	switch {
	case entry.disabled:
		result.Reason = ReasonTargetDisabled
	case !entry.ready:
		result.Reason = ReasonProviderNotReady
	case !s.supportsCapability(entry, capability):
		result.Reason = ReasonCapabilityUnsupported
	case entry.cooldown(bareModel, capability, s.builtAt).Until.After(s.builtAt):
		cooldown := entry.cooldown(bareModel, capability, s.builtAt)
		result.Reason = ReasonCooldown
		result.CooldownUntil = cooldown.Until
		result.CooldownReason = cooldown.Reason
	case entry.hasAllowlist && !entry.isAllowed(bareModel):
		result.Reason = ReasonModelDisallowed
	case !entry.catalogPresent || !entry.hasModel(bareModel):
		result.Reason = ReasonModelNotInCatalog
	case entry.catalogStale:
		result.Reason = ReasonCatalogStale
	default:
		result.Routable = true
		result.Reason = ReasonRoutable
	}
	return result
}

func (entry providerEntry) cooldown(model string, capability provider.Capability, now time.Time) Cooldown {
	cooldown := entry.cooldowns[cooldownKey(normalizeModel(model), capability)]
	if !cooldown.Until.After(now) {
		return Cooldown{}
	}
	return cooldown
}

func cooldownKey(model string, capability provider.Capability) string {
	return string(capability) + "\x00" + normalizeModel(model)
}

func (s *Snapshot) supportsCapability(entry providerEntry, capability provider.Capability) bool {
	_, ok := entry.capabilities[capability]
	return ok
}

func (entry providerEntry) isAllowed(bareModel string) bool {
	_, ok := entry.allowed[normalizeModel(bareModel)]
	return ok
}

func (entry providerEntry) hasModel(bareModel string) bool {
	_, ok := entry.models[normalizeModel(bareModel)]
	return ok
}

// ProviderIDs returns the provider IDs represented in this snapshot, sorted for
// deterministic diagnostics.
func (s *Snapshot) ProviderIDs() []provider.ID {
	if s == nil {
		return nil
	}
	ids := make([]provider.ID, 0, len(s.providers))
	for id := range s.providers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func normalizeModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}
