package routing

import (
	"context"
	"sort"

	"github.com/duvu/ya-router/internal/availability"
	"github.com/duvu/ya-router/internal/provider"
)

// TargetReadiness is the redacted readiness of one umbrella target for one
// capability. It contains only configured target IDs and stable reason codes.
type TargetReadiness struct {
	Target           string                      `json:"target"`
	Index            int                         `json:"index"`
	Routable         bool                        `json:"routable"`
	Reason           availability.Reason         `json:"reason"`
	CooldownUntil    int64                       `json:"cooldown_until,omitempty"`
	CooldownReason   availability.CooldownReason `json:"cooldown_reason,omitempty"`
	CatalogFetchedAt int64                       `json:"catalog_fetched_at,omitempty"`
	CatalogStale     bool                        `json:"catalog_stale"`
}

// VirtualModelReadiness summarizes one umbrella model's current selection state.
type VirtualModelReadiness struct {
	VirtualModel   string            `json:"virtual_model"`
	Strategy       string            `json:"strategy"`
	Capability     string            `json:"capability"`
	Generation     uint64            `json:"generation"`
	SelectedTarget string            `json:"selected_target,omitempty"`
	Active         bool              `json:"active"`
	Targets        []TargetReadiness `json:"targets"`
}

// VirtualModelReadinessFor computes redacted readiness for every configured
// umbrella model against a network-free availability snapshot. It performs no
// provider I/O and returns deterministic, bounded output suitable for
// diagnostics. Output arrays are bounded by the configured target count.
func (r *Router) VirtualModelReadinessFor(capability provider.Capability, snapshot *availability.Snapshot) []VirtualModelReadiness {
	ids := make([]string, 0, len(r.routing.VirtualModels))
	for id := range r.routing.VirtualModels {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]VirtualModelReadiness, 0, len(ids))
	for _, id := range ids {
		vm := r.routing.VirtualModels[id]
		summary := VirtualModelReadiness{
			VirtualModel: id,
			Strategy:     vm.Strategy,
			Capability:   string(capability),
			Generation:   snapshot.Generation(),
			Targets:      make([]TargetReadiness, 0, len(vm.Targets)),
		}
		for index, target := range vm.Targets {
			bare, providerID, hasPrefix := StripModelPrefix(target)
			reason := availability.ReasonProviderNotRegistered
			routable := false
			if hasPrefix {
				result := snapshot.Evaluate(providerID, bare, capability)
				reason = result.Reason
				routable = result.Routable
				cooldownUntil := int64(0)
				if !result.CooldownUntil.IsZero() {
					cooldownUntil = result.CooldownUntil.Unix()
				}
				catalogFetchedAt := int64(0)
				if !result.CatalogFetchedAt.IsZero() {
					catalogFetchedAt = result.CatalogFetchedAt.Unix()
				}
				summary.Targets = append(summary.Targets, TargetReadiness{
					Target: target, Index: index, Routable: routable, Reason: reason,
					CooldownUntil: cooldownUntil, CooldownReason: result.CooldownReason,
					CatalogFetchedAt: catalogFetchedAt, CatalogStale: result.CatalogStale,
				})
				if routable && !summary.Active {
					summary.Active = true
					summary.SelectedTarget = target
				}
				continue
			}
			summary.Targets = append(summary.Targets, TargetReadiness{
				Target:   target,
				Index:    index,
				Routable: routable,
				Reason:   reason,
			})
			if routable && !summary.Active {
				summary.Active = true
				summary.SelectedTarget = target
			}
		}
		out = append(out, summary)
	}
	return out
}

// AvailabilitySnapshot exposes a network-free availability view for diagnostics
// callers (health/control plane). It is the same view umbrella selection uses.
func (r *Router) AvailabilitySnapshot() *availability.Snapshot {
	return r.buildAvailabilitySnapshot(context.Background())
}
