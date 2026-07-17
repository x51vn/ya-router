package availability

import (
	"sync"
	"testing"
	"time"

	"github.com/duvu/ya-router/internal/provider"
)

func baseView() ProviderView {
	return ProviderView{
		ID:               provider.Copilot,
		Ready:            true,
		Capabilities:     []provider.Capability{provider.CapabilityChat},
		CatalogPresent:   true,
		CatalogFetchedAt: time.Unix(1000, 0),
		CatalogModels:    []string{"gpt-5-mini"},
	}
}

func TestEvaluateReasonCodes(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(v *ProviderView)
		providerID provider.ID
		model      string
		capability provider.Capability
		wantReason Reason
		wantRoute  bool
	}{
		{
			name:       "routable",
			providerID: provider.Copilot,
			model:      "gpt-5-mini",
			capability: provider.CapabilityChat,
			wantReason: ReasonRoutable,
			wantRoute:  true,
		},
		{
			name:       "provider not registered",
			providerID: provider.Codex,
			model:      "gpt-5-mini",
			capability: provider.CapabilityChat,
			wantReason: ReasonProviderNotRegistered,
		},
		{
			name:       "target disabled",
			mutate:     func(v *ProviderView) { v.Disabled = true },
			providerID: provider.Copilot,
			model:      "gpt-5-mini",
			capability: provider.CapabilityChat,
			wantReason: ReasonTargetDisabled,
		},
		{
			name:       "provider not ready",
			mutate:     func(v *ProviderView) { v.Ready = false },
			providerID: provider.Copilot,
			model:      "gpt-5-mini",
			capability: provider.CapabilityChat,
			wantReason: ReasonProviderNotReady,
		},
		{
			name:       "capability unsupported",
			providerID: provider.Copilot,
			model:      "gpt-5-mini",
			capability: provider.CapabilityEmbeddings,
			wantReason: ReasonCapabilityUnsupported,
		},
		{
			name:       "model disallowed",
			mutate:     func(v *ProviderView) { v.AllowedModels = []string{"other-model"} },
			providerID: provider.Copilot,
			model:      "gpt-5-mini",
			capability: provider.CapabilityChat,
			wantReason: ReasonModelDisallowed,
		},
		{
			name:       "model not in catalog",
			mutate:     func(v *ProviderView) { v.CatalogModels = []string{"different"} },
			providerID: provider.Copilot,
			model:      "gpt-5-mini",
			capability: provider.CapabilityChat,
			wantReason: ReasonModelNotInCatalog,
		},
		{
			name:       "catalog absent",
			mutate:     func(v *ProviderView) { v.CatalogPresent = false; v.CatalogModels = nil },
			providerID: provider.Copilot,
			model:      "gpt-5-mini",
			capability: provider.CapabilityChat,
			wantReason: ReasonModelNotInCatalog,
		},
		{
			name:       "catalog stale",
			mutate:     func(v *ProviderView) { v.CatalogStale = true },
			providerID: provider.Copilot,
			model:      "gpt-5-mini",
			capability: provider.CapabilityChat,
			wantReason: ReasonCatalogStale,
		},
		{
			name:       "allowlist permits model",
			mutate:     func(v *ProviderView) { v.AllowedModels = []string{"gpt-5-mini"} },
			providerID: provider.Copilot,
			model:      "gpt-5-mini",
			capability: provider.CapabilityChat,
			wantReason: ReasonRoutable,
			wantRoute:  true,
		},
		{
			name:       "model match is case-insensitive",
			providerID: provider.Copilot,
			model:      "GPT-5-Mini",
			capability: provider.CapabilityChat,
			wantReason: ReasonRoutable,
			wantRoute:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := baseView()
			if tc.mutate != nil {
				tc.mutate(&view)
			}
			snap := NewSnapshot(7, time.Unix(2000, 0), []ProviderView{view})
			got := snap.Evaluate(tc.providerID, tc.model, tc.capability)
			if got.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if got.Routable != tc.wantRoute {
				t.Fatalf("routable = %v, want %v", got.Routable, tc.wantRoute)
			}
			if got.Generation != 7 {
				t.Fatalf("generation = %d, want 7", got.Generation)
			}
		})
	}
}

// TestEvaluatePriorityOfReasons proves the first failing condition wins, so
// results are deterministic and diagnostics are stable.
func TestEvaluatePriorityOfReasons(t *testing.T) {
	view := baseView()
	view.Disabled = true
	view.Ready = false // both disabled and not ready; disabled must win
	snap := NewSnapshot(1, time.Unix(2000, 0), []ProviderView{view})
	got := snap.Evaluate(provider.Copilot, "gpt-5-mini", provider.CapabilityChat)
	if got.Reason != ReasonTargetDisabled {
		t.Fatalf("reason = %q, want %q", got.Reason, ReasonTargetDisabled)
	}
}

func TestEvaluateNilSnapshot(t *testing.T) {
	var snap *Snapshot
	got := snap.Evaluate(provider.Copilot, "x", provider.CapabilityChat)
	if got.Reason != ReasonProviderNotRegistered || got.Routable {
		t.Fatalf("nil snapshot evaluate = %+v", got)
	}
}

// TestSnapshotImmutableUnderConcurrency runs many concurrent reads against one
// snapshot to prove reads are race-free (exercised under `go test -race`).
func TestSnapshotImmutableUnderConcurrency(t *testing.T) {
	views := []ProviderView{
		baseView(),
		{ID: provider.Codex, Ready: true, Capabilities: []provider.Capability{provider.CapabilityChat}, CatalogPresent: true, CatalogModels: []string{"gpt-5.4-mini"}},
	}
	snap := NewSnapshot(3, time.Unix(2000, 0), views)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = snap.Evaluate(provider.Copilot, "gpt-5-mini", provider.CapabilityChat)
			_ = snap.Evaluate(provider.Codex, "gpt-5.4-mini", provider.CapabilityChat)
			_ = snap.ProviderIDs()
		}()
	}
	wg.Wait()

	// A snapshot read must not have mutated the source view slices.
	if got := snap.Evaluate(provider.Copilot, "gpt-5-mini", provider.CapabilityChat); !got.Routable {
		t.Fatalf("expected copilot routable after concurrent reads")
	}
}

func TestProviderIDsSorted(t *testing.T) {
	snap := NewSnapshot(1, time.Unix(2000, 0), []ProviderView{
		{ID: provider.Kilo}, {ID: provider.Codex}, {ID: provider.Copilot},
	})
	ids := snap.ProviderIDs()
	for i := 1; i < len(ids); i++ {
		if ids[i-1] > ids[i] {
			t.Fatalf("provider IDs not sorted: %v", ids)
		}
	}
}
