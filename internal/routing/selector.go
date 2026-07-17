package routing

import (
	"fmt"
	"strings"

	"github.com/duvu/ya-router/internal/availability"
	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
)

// SkippedTarget records why one umbrella target was not selected. It carries
// only bounded, redacted data suitable for logs and diagnostics.
type SkippedTarget struct {
	Target        string
	Index         int
	Reason        availability.Reason
	CooldownUntil int64
}

// SelectionDecision is the sanitized result of umbrella selection. It contains
// no provider objects, credentials, prompts, or arbitrary upstream data and is
// safe to log or expose through redacted diagnostics.
type SelectionDecision struct {
	VirtualModel     string
	Strategy         string
	SelectedTarget   string
	SelectedIndex    int
	Generation       uint64
	CatalogFetchedAt int64 // unix seconds; 0 when unknown
	Capability       provider.Capability
	Skipped          []SkippedTarget
}

// NoActiveTargetError is returned when no target in an umbrella model is
// routable. Its message and fields are bounded to configured targets and stable
// reason codes.
type NoActiveTargetError struct {
	VirtualModel string
	Skipped      []SkippedTarget
}

func (e *NoActiveTargetError) Error() string {
	return fmt.Sprintf("no active target is available for model %q", e.VirtualModel)
}

// SelectPriorityTarget picks the first routable target from an umbrella model's
// ordered target list for the requested capability. It is a pure function: it
// performs no network I/O, calls no provider, and mutates nothing.
//
// The virtual model must already be validated (see
// config.Routing.ValidateVirtualModels); targets must carry a known provider
// prefix. A target whose prefix is not recognized is skipped with a
// provider_not_registered reason rather than causing a panic.
func SelectPriorityTarget(virtualModelID string, vm configschema.VirtualModel, capability provider.Capability, snapshot *availability.Snapshot) (*SelectionDecision, error) {
	skipped := make([]SkippedTarget, 0, len(vm.Targets))
	for index, target := range vm.Targets {
		// Trim to match config validation, which trims before classifying a
		// target's prefix. Without this a whitespace-padded target that passed
		// validation would be silently unroutable here.
		bare, providerID, hasPrefix := StripModelPrefix(strings.TrimSpace(target))
		if !hasPrefix {
			skipped = append(skipped, SkippedTarget{Target: target, Index: index, Reason: availability.ReasonProviderNotRegistered})
			continue
		}
		result := snapshot.Evaluate(providerID, bare, capability)
		if result.Routable {
			var fetched int64
			if !result.CatalogFetchedAt.IsZero() {
				fetched = result.CatalogFetchedAt.Unix()
			}
			return &SelectionDecision{
				VirtualModel:     virtualModelID,
				Strategy:         configschema.VirtualModelStrategyPriority,
				SelectedTarget:   target,
				SelectedIndex:    index,
				Generation:       result.Generation,
				CatalogFetchedAt: fetched,
				Capability:       capability,
				Skipped:          skipped,
			}, nil
		}
		cooldownUntil := int64(0)
		if !result.CooldownUntil.IsZero() {
			cooldownUntil = result.CooldownUntil.Unix()
		}
		skipped = append(skipped, SkippedTarget{Target: target, Index: index, Reason: result.Reason, CooldownUntil: cooldownUntil})
	}
	return nil, &NoActiveTargetError{VirtualModel: virtualModelID, Skipped: skipped}
}
