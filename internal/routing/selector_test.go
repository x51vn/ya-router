package routing

import (
	"errors"
	"testing"
	"time"

	"github.com/duvu/ya-router/internal/availability"
	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
)

// snapshotWith builds an availability snapshot where each named provider is
// ready with a chat catalog containing the given bare models.
func snapshotWith(models map[provider.ID][]string) *availability.Snapshot {
	views := make([]availability.ProviderView, 0, len(models))
	for id, list := range models {
		views = append(views, availability.ProviderView{
			ID:               id,
			Ready:            true,
			Capabilities:     []provider.Capability{provider.CapabilityChat},
			CatalogPresent:   len(list) > 0,
			CatalogModels:    list,
			CatalogFetchedAt: time.Unix(1000, 0),
		})
	}
	return availability.NewSnapshot(5, time.Unix(2000, 0), views)
}

func priorityVM(targets ...string) configschema.VirtualModel {
	return configschema.VirtualModel{Strategy: configschema.VirtualModelStrategyPriority, Targets: targets}
}

func TestSelectPriorityTarget(t *testing.T) {
	cases := []struct {
		name       string
		vm         configschema.VirtualModel
		snapshot   *availability.Snapshot
		wantTarget string
		wantIndex  int
		wantErr    bool
	}{
		{
			name:       "first target active",
			vm:         priorityVM("github/gpt-5-mini", "codex/gpt-5.4-mini"),
			snapshot:   snapshotWith(map[provider.ID][]string{provider.Copilot: {"gpt-5-mini"}, provider.Codex: {"gpt-5.4-mini"}}),
			wantTarget: "github/gpt-5-mini",
			wantIndex:  0,
		},
		{
			name:       "later target active when first unavailable",
			vm:         priorityVM("github/gpt-5-mini", "codex/gpt-5.4-mini"),
			snapshot:   snapshotWith(map[provider.ID][]string{provider.Codex: {"gpt-5.4-mini"}}),
			wantTarget: "codex/gpt-5.4-mini",
			wantIndex:  1,
		},
		{
			name:     "no target active",
			vm:       priorityVM("github/gpt-5-mini", "codex/gpt-5.4-mini"),
			snapshot: snapshotWith(map[provider.ID][]string{}),
			wantErr:  true,
		},
		{
			name:     "model missing from catalog",
			vm:       priorityVM("github/gpt-5-mini"),
			snapshot: snapshotWith(map[provider.ID][]string{provider.Copilot: {"other"}}),
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := SelectPriorityTarget("router/auto", tc.vm, provider.CapabilityChat, tc.snapshot)
			if tc.wantErr {
				var noTarget *NoActiveTargetError
				if !errors.As(err, &noTarget) {
					t.Fatalf("expected NoActiveTargetError, got %v", err)
				}
				if len(noTarget.Skipped) != len(tc.vm.Targets) {
					t.Fatalf("skipped %d, want %d", len(noTarget.Skipped), len(tc.vm.Targets))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if decision.SelectedTarget != tc.wantTarget || decision.SelectedIndex != tc.wantIndex {
				t.Fatalf("selected %q@%d, want %q@%d", decision.SelectedTarget, decision.SelectedIndex, tc.wantTarget, tc.wantIndex)
			}
			if decision.Strategy != configschema.VirtualModelStrategyPriority {
				t.Fatalf("strategy = %q", decision.Strategy)
			}
			if decision.Generation != 5 {
				t.Fatalf("generation = %d, want 5", decision.Generation)
			}
		})
	}
}

func TestSelectPriorityCapabilityMismatch(t *testing.T) {
	snap := snapshotWith(map[provider.ID][]string{provider.Copilot: {"gpt-5-mini"}})
	// Provider only supports chat; request embeddings.
	_, err := SelectPriorityTarget("router/auto", priorityVM("github/gpt-5-mini"), provider.CapabilityEmbeddings, snap)
	var noTarget *NoActiveTargetError
	if !errors.As(err, &noTarget) {
		t.Fatalf("expected NoActiveTargetError, got %v", err)
	}
	if noTarget.Skipped[0].Reason != availability.ReasonCapabilityUnsupported {
		t.Fatalf("reason = %q, want capability_unsupported", noTarget.Skipped[0].Reason)
	}
}

func TestSelectPriorityStaleCatalogSkipped(t *testing.T) {
	snap := availability.NewSnapshot(9, time.Unix(2000, 0), []availability.ProviderView{{
		ID:             provider.Copilot,
		Ready:          true,
		Capabilities:   []provider.Capability{provider.CapabilityChat},
		CatalogPresent: true,
		CatalogStale:   true,
		CatalogModels:  []string{"gpt-5-mini"},
	}})
	_, err := SelectPriorityTarget("router/auto", priorityVM("github/gpt-5-mini"), provider.CapabilityChat, snap)
	var noTarget *NoActiveTargetError
	if !errors.As(err, &noTarget) {
		t.Fatalf("expected NoActiveTargetError, got %v", err)
	}
	if noTarget.Skipped[0].Reason != availability.ReasonCatalogStale {
		t.Fatalf("reason = %q, want catalog_stale", noTarget.Skipped[0].Reason)
	}
}

// TestSelectPriorityDeterministic proves repeated selection returns the same
// target for the same inputs.
func TestSelectPriorityDeterministic(t *testing.T) {
	vm := priorityVM("github/gpt-5-mini", "codex/gpt-5.4-mini", "kilo/kilo-auto")
	snap := snapshotWith(map[provider.ID][]string{provider.Codex: {"gpt-5.4-mini"}, provider.Kilo: {"kilo-auto"}})
	var first string
	for i := 0; i < 100; i++ {
		decision, err := SelectPriorityTarget("router/auto", vm, provider.CapabilityChat, snap)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if first == "" {
			first = decision.SelectedTarget
			continue
		}
		if decision.SelectedTarget != first {
			t.Fatalf("non-deterministic selection: %q != %q", decision.SelectedTarget, first)
		}
	}
	if first != "codex/gpt-5.4-mini" {
		t.Fatalf("selected %q, want codex/gpt-5.4-mini", first)
	}
}

func TestSelectPriorityUnknownPrefixSkipped(t *testing.T) {
	snap := snapshotWith(map[provider.ID][]string{provider.Copilot: {"gpt-5-mini"}})
	// "bogus/x" has no known prefix — must be skipped, then github wins.
	decision, err := SelectPriorityTarget("router/auto", priorityVM("bogus/x", "github/gpt-5-mini"), provider.CapabilityChat, snap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.SelectedTarget != "github/gpt-5-mini" || decision.SelectedIndex != 1 {
		t.Fatalf("selected %q@%d", decision.SelectedTarget, decision.SelectedIndex)
	}
	if decision.Skipped[0].Reason != availability.ReasonProviderNotRegistered {
		t.Fatalf("skipped reason = %q", decision.Skipped[0].Reason)
	}
}

// TestSelectPriorityTrimsTarget proves a whitespace-padded target (which config
// validation accepts after trimming) is still routable at selection time.
func TestSelectPriorityTrimsTarget(t *testing.T) {
	snap := snapshotWith(map[provider.ID][]string{provider.Copilot: {"gpt-5-mini"}})
	decision, err := SelectPriorityTarget("router/auto", priorityVM("  github/gpt-5-mini  "), provider.CapabilityChat, snap)
	if err != nil {
		t.Fatalf("padded target unroutable: %v", err)
	}
	if decision.SelectedIndex != 0 {
		t.Fatalf("selected index = %d, want 0", decision.SelectedIndex)
	}
}

// FuzzSelectPriorityTarget ensures malformed umbrella definitions never panic
// or produce an out-of-range selection index.
func FuzzSelectPriorityTarget(f *testing.F) {
	f.Add("github/gpt-5-mini|codex/x", "gpt-5-mini")
	f.Add("", "")
	f.Add("kilo/|/|//", "x")
	f.Fuzz(func(t *testing.T, targetsRaw, catalogModel string) {
		targets := splitNonEmpty(targetsRaw, '|')
		snap := snapshotWith(map[provider.ID][]string{provider.Copilot: {catalogModel}})
		vm := configschema.VirtualModel{Strategy: configschema.VirtualModelStrategyPriority, Targets: targets}
		decision, err := SelectPriorityTarget("router/fuzz", vm, provider.CapabilityChat, snap)
		if err != nil {
			return
		}
		if decision.SelectedIndex < 0 || decision.SelectedIndex >= len(targets) {
			t.Fatalf("selected index %d out of range for %d targets", decision.SelectedIndex, len(targets))
		}
		if len(decision.Skipped) > len(targets) {
			t.Fatalf("skipped %d exceeds target count %d", len(decision.Skipped), len(targets))
		}
	})
}

func splitNonEmpty(s string, sep rune) []string {
	var out []string
	current := ""
	for _, r := range s {
		if r == sep {
			out = append(out, current)
			current = ""
			continue
		}
		current += string(r)
	}
	out = append(out, current)
	return out
}
