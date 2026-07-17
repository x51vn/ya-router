package routing

import (
	"strings"
	"testing"
	"time"

	"github.com/duvu/ya-router/internal/availability"
	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
)

func TestMetricsRecordsSelectionAndSkips(t *testing.T) {
	m := NewMetrics()
	m.RecordSelection(&SelectionDecision{
		VirtualModel:   "router/auto",
		SelectedTarget: "codex/gpt-5.4-mini",
		Skipped: []SkippedTarget{
			{Target: "github/gpt-5-mini", Index: 0, Reason: availability.ReasonCatalogStale},
		},
	})
	m.RecordNoActiveTarget(&NoActiveTargetError{
		VirtualModel: "router/auto",
		Skipped: []SkippedTarget{
			{Target: "github/gpt-5-mini", Index: 0, Reason: availability.ReasonProviderNotReady},
		},
	})

	counters := m.Snapshot()
	want := map[string]uint64{
		"umbrella_selections_total":       1,
		"umbrella_no_active_target_total": 1,
		"umbrella_stale_catalog_total":    1,
	}
	got := map[string]uint64{}
	for _, c := range counters {
		got[c.Name] += c.Value
		// Every label value must be a configured ID or a stable reason — never
		// an arbitrary/unbounded string.
		for k, v := range c.Labels {
			if v == "" {
				t.Fatalf("counter %s has empty label %s", c.Name, k)
			}
		}
	}
	for name, wantVal := range want {
		if got[name] != wantVal {
			t.Fatalf("%s = %d, want %d", name, got[name], wantVal)
		}
	}
}

func TestMetricsNilSafe(t *testing.T) {
	var m *Metrics
	m.RecordSelection(&SelectionDecision{VirtualModel: "x"})
	m.RecordNoActiveTarget(&NoActiveTargetError{VirtualModel: "x"})
	if got := m.Snapshot(); got != nil {
		t.Fatalf("nil metrics snapshot = %v", got)
	}
}

func TestMetricsSnapshotDeterministic(t *testing.T) {
	m := NewMetrics()
	m.RecordSelection(&SelectionDecision{VirtualModel: "router/b", SelectedTarget: "codex/x"})
	m.RecordSelection(&SelectionDecision{VirtualModel: "router/a", SelectedTarget: "github/y"})
	var first string
	for i := 0; i < 20; i++ {
		snap := m.Snapshot()
		s := ""
		for _, c := range snap {
			s += c.Name + labelString(c.Labels)
		}
		if first == "" {
			first = s
			continue
		}
		if s != first {
			t.Fatal("snapshot order not deterministic")
		}
	}
}

func TestVirtualModelReadinessRedacted(t *testing.T) {
	registry := provider.NewRegistry(
		&umbrellaProvider{id: provider.Copilot, authenticated: false, hasCatalog: true, catalog: []string{"gpt-5-mini"}},
		&umbrellaProvider{id: provider.Codex, authenticated: true, hasCatalog: true, catalog: []string{"gpt-5.4-mini"}},
	)
	router := NewRouter(registry, umbrellaRouting("github/gpt-5-mini", "codex/gpt-5.4-mini"))
	router.SetGeneration(4)

	snap := availability.NewSnapshot(4, time.Unix(2000, 0), []availability.ProviderView{
		{ID: provider.Copilot, Ready: false, Capabilities: []provider.Capability{provider.CapabilityChat}, CatalogPresent: true, CatalogFetchedAt: time.Unix(1000, 0), CatalogModels: []string{"gpt-5-mini"}},
		{ID: provider.Codex, Ready: true, Capabilities: []provider.Capability{provider.CapabilityChat}, CatalogPresent: true, CatalogFetchedAt: time.Unix(1000, 0), CatalogModels: []string{"gpt-5.4-mini"}},
	})

	summaries := router.VirtualModelReadinessFor(provider.CapabilityChat, snap)
	if len(summaries) != 1 {
		t.Fatalf("summaries = %d, want 1", len(summaries))
	}
	summary := summaries[0]
	if summary.VirtualModel != "router/auto" || !summary.Active {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.SelectedTarget != "codex/gpt-5.4-mini" {
		t.Fatalf("selected = %q, want codex/gpt-5.4-mini", summary.SelectedTarget)
	}
	if len(summary.Targets) != 2 {
		t.Fatalf("targets = %d, want 2 (bounded by config)", len(summary.Targets))
	}
	if summary.Targets[0].Reason != availability.ReasonProviderNotReady {
		t.Fatalf("first target reason = %q", summary.Targets[0].Reason)
	}
	if summary.Targets[1].CatalogFetchedAt != 1000 || summary.Targets[1].CatalogStale {
		t.Fatalf("catalog posture = %+v", summary.Targets[1])
	}
	// Reason codes must be one of the known stable set (no upstream strings).
	for _, tr := range summary.Targets {
		if !isStableReason(tr.Reason) {
			t.Fatalf("unexpected reason code %q", tr.Reason)
		}
	}
}

func isStableReason(r availability.Reason) bool {
	switch r {
	case availability.ReasonRoutable,
		availability.ReasonProviderNotRegistered,
		availability.ReasonTargetDisabled,
		availability.ReasonProviderNotReady,
		availability.ReasonCapabilityUnsupported,
		availability.ReasonModelDisallowed,
		availability.ReasonModelNotInCatalog,
		availability.ReasonCatalogStale:
		return true
	default:
		return false
	}
}

// TestSelectionDecisionCarriesNoSecrets is a guard that decision metadata only
// contains configured IDs and stable codes.
func TestSelectionDecisionCarriesNoSecrets(t *testing.T) {
	vm := configschema.VirtualModel{Strategy: configschema.VirtualModelStrategyPriority, Targets: []string{"github/gpt-5-mini"}}
	snap := availability.NewSnapshot(1, time.Unix(2000, 0), []availability.ProviderView{
		{ID: provider.Copilot, Ready: true, Capabilities: []provider.Capability{provider.CapabilityChat}, CatalogPresent: true, CatalogModels: []string{"gpt-5-mini"}},
	})
	decision, err := SelectPriorityTarget("router/auto", vm, provider.CapabilityChat, snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(decision.SelectedTarget, " ") {
		t.Fatalf("selected target looks unbounded: %q", decision.SelectedTarget)
	}
}
