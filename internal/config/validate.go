package config

import (
	"fmt"
	"sort"
	"strings"
)

// VirtualModelNamespace is the recommended reserved namespace for umbrella
// model IDs. IDs outside this namespace are allowed but discouraged.
const VirtualModelNamespace = "router/"

// ValidateVirtualModels validates the routing.virtual_models block against the
// set of known provider prefixes (for example "github/", "codex/", "kilo/")
// and the explicit routing.model_map. Prefixes are supplied by the caller so
// this package stays free of provider/routing dependencies.
//
// Errors are stable and actionable so a bad configuration fails at load time
// rather than at request time. Validation is order-independent: model IDs and
// targets are examined in sorted order so repeated runs report the same first
// error.
func (r Routing) ValidateVirtualModels(knownPrefixes []string) error {
	if len(r.VirtualModels) == 0 {
		return nil
	}

	ids := make([]string, 0, len(r.VirtualModels))
	for id := range r.VirtualModels {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		vm := r.VirtualModels[id]
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("routing.virtual_models: virtual model ID must not be empty")
		}
		if prefix, ok := matchKnownPrefix(id, knownPrefixes); ok {
			return fmt.Errorf("routing.virtual_models[%q]: ID collides with the reserved provider prefix %q; use a non-provider ID such as %q", id, prefix, VirtualModelNamespace+"auto")
		}
		if _, shadows := r.ModelMap[id]; shadows {
			return fmt.Errorf("routing.virtual_models[%q]: ID shadows an explicit routing.model_map entry", id)
		}
		if vm.Strategy != VirtualModelStrategyPriority {
			return fmt.Errorf("routing.virtual_models[%q]: strategy %q is not supported; the only supported strategy is %q", id, vm.Strategy, VirtualModelStrategyPriority)
		}
		if len(vm.Targets) == 0 {
			return fmt.Errorf("routing.virtual_models[%q]: at least one target is required", id)
		}
		seen := make(map[string]struct{}, len(vm.Targets))
		for _, target := range vm.Targets {
			trimmed := strings.TrimSpace(target)
			if trimmed == "" {
				return fmt.Errorf("routing.virtual_models[%q]: target must not be empty", id)
			}
			if _, dup := seen[trimmed]; dup {
				return fmt.Errorf("routing.virtual_models[%q]: target %q is listed more than once", id, trimmed)
			}
			seen[trimmed] = struct{}{}
			if _, isVirtual := r.VirtualModels[trimmed]; isVirtual {
				return fmt.Errorf("routing.virtual_models[%q]: target %q references another virtual model; umbrella-to-umbrella references are forbidden", id, trimmed)
			}
			if _, ok := matchKnownPrefix(trimmed, knownPrefixes); !ok {
				return fmt.Errorf("routing.virtual_models[%q]: target %q must use a known provider prefix (one of %s)", id, trimmed, strings.Join(sortedCopy(knownPrefixes), ", "))
			}
		}
	}
	return nil
}

// matchKnownPrefix reports whether id begins with one of the known prefixes and
// returns the matched prefix.
func matchKnownPrefix(id string, knownPrefixes []string) (string, bool) {
	for _, prefix := range knownPrefixes {
		if prefix != "" && strings.HasPrefix(id, prefix) {
			return prefix, true
		}
	}
	return "", false
}

func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
