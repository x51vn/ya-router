package routing

import (
	"sort"
	"sync"

	"github.com/duvu/ya-router/internal/availability"
)

// Metrics records bounded umbrella-routing counters. Cardinality is bounded by
// configuration: labels are only configured virtual-model IDs, configured
// target IDs, and stable availability reason codes. No prompt, secret, or
// arbitrary upstream value is ever used as a label.
//
// Metrics is safe for concurrent use. It distinguishes selection from
// provider-internal retry and never records cross-provider failover, which does
// not occur.
type Metrics struct {
	mu sync.Mutex
	// selections counts successful selections by "virtualModel\x00target".
	selections map[string]uint64
	// noActiveTarget counts no-active-target decisions by virtual model.
	noActiveTarget map[string]uint64
	// skipped counts skipped targets by "target\x00reason".
	skipped map[string]uint64
	// staleCatalog counts selections/skips that observed a stale catalog, by
	// virtual model.
	staleCatalog    map[string]uint64
	cooldownEntries map[string]uint64
	cooldownExits   map[string]uint64
}

// NewMetrics returns an empty metrics sink.
func NewMetrics() *Metrics {
	return &Metrics{
		selections:      make(map[string]uint64),
		noActiveTarget:  make(map[string]uint64),
		skipped:         make(map[string]uint64),
		staleCatalog:    make(map[string]uint64),
		cooldownEntries: make(map[string]uint64),
		cooldownExits:   make(map[string]uint64),
	}
}

func (m *Metrics) RecordCooldownEntry(target string, reason availability.CooldownReason) {
	if m == nil || target == "" || reason == "" {
		return
	}
	m.mu.Lock()
	m.cooldownEntries[metricKey(target, string(reason))]++
	m.mu.Unlock()
}

func (m *Metrics) RecordCooldownExit(target, reason string) {
	if m == nil || target == "" || reason == "" {
		return
	}
	m.mu.Lock()
	m.cooldownExits[metricKey(target, reason)]++
	m.mu.Unlock()
}

func metricKey(a, b string) string { return a + "\x00" + b }

// RecordSelection records one successful selection decision and its skipped
// targets. It is a no-op on a nil receiver so callers need not branch.
func (m *Metrics) RecordSelection(decision *SelectionDecision) {
	if m == nil || decision == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.selections[metricKey(decision.VirtualModel, decision.SelectedTarget)]++
	for _, skip := range decision.Skipped {
		m.skipped[metricKey(skip.Target, string(skip.Reason))]++
		if skip.Reason == availability.ReasonCatalogStale {
			m.staleCatalog[decision.VirtualModel]++
		}
	}
}

// RecordNoActiveTarget records one no-active-target decision.
func (m *Metrics) RecordNoActiveTarget(err *NoActiveTargetError) {
	if m == nil || err == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.noActiveTarget[err.VirtualModel]++
	for _, skip := range err.Skipped {
		m.skipped[metricKey(skip.Target, string(skip.Reason))]++
		if skip.Reason == availability.ReasonCatalogStale {
			m.staleCatalog[err.VirtualModel]++
		}
	}
}

// Counter is one exported metric sample with bounded labels.
type Counter struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	Value  uint64            `json:"value"`
}

// Snapshot returns a deterministic, sorted copy of all counters for scraping or
// diagnostics.
func (m *Metrics) Snapshot() []Counter {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []Counter
	for key, value := range m.selections {
		vm, target := splitKey(key)
		out = append(out, Counter{Name: "umbrella_selections_total", Labels: map[string]string{"virtual_model": vm, "target": target}, Value: value})
	}
	for vm, value := range m.noActiveTarget {
		out = append(out, Counter{Name: "umbrella_no_active_target_total", Labels: map[string]string{"virtual_model": vm}, Value: value})
	}
	for key, value := range m.skipped {
		target, reason := splitKey(key)
		out = append(out, Counter{Name: "umbrella_skipped_targets_total", Labels: map[string]string{"target": target, "reason": reason}, Value: value})
	}
	for vm, value := range m.staleCatalog {
		out = append(out, Counter{Name: "umbrella_stale_catalog_total", Labels: map[string]string{"virtual_model": vm}, Value: value})
	}
	for key, value := range m.cooldownEntries {
		target, reason := splitKey(key)
		out = append(out, Counter{Name: "umbrella_cooldown_entries_total", Labels: map[string]string{"target": target, "reason": reason}, Value: value})
	}
	for key, value := range m.cooldownExits {
		target, reason := splitKey(key)
		out = append(out, Counter{Name: "umbrella_cooldown_exits_total", Labels: map[string]string{"target": target, "reason": reason}, Value: value})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return labelString(out[i].Labels) < labelString(out[j].Labels)
	})
	return out
}

func splitKey(key string) (string, string) {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

func labelString(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := ""
	for _, k := range keys {
		s += k + "=" + labels[k] + ";"
	}
	return s
}
