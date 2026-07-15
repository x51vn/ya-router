package provider

import (
	"sort"
	"sync"
	"time"
)

// State separates provider lifecycle from authentication health.
type State string

const (
	StateDisabled            State = "disabled"
	StateEnabledUnconfigured State = "enabled_unconfigured"
	StateReady               State = "ready"
	StateDegraded            State = "degraded"
	StateError               State = "error"
	StateDraining            State = "draining"
)

// HealthRecord is independent from the runtime registry and contains no
// provider object or credential material.
type HealthRecord struct {
	ProviderID ID        `json:"provider_id"`
	State      State     `json:"state"`
	Health     Health    `json:"health"`
	Generation uint64    `json:"generation"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// HealthRegistry stores lifecycle/health observations independently from
// request routing registries.
type HealthRegistry struct {
	mu      sync.RWMutex
	records map[ID]HealthRecord
}

func NewHealthRegistry() *HealthRegistry {
	return &HealthRegistry{records: make(map[ID]HealthRecord)}
}

func (registry *HealthRegistry) Set(record HealthRecord) {
	if registry == nil || record.ProviderID == "" {
		return
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	}
	registry.mu.Lock()
	registry.records[record.ProviderID] = record
	registry.mu.Unlock()
}

func (registry *HealthRegistry) Get(id ID) (HealthRecord, bool) {
	if registry == nil {
		return HealthRecord{}, false
	}
	registry.mu.RLock()
	record, exists := registry.records[id]
	registry.mu.RUnlock()
	return record, exists
}

func (registry *HealthRegistry) Remove(id ID) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	delete(registry.records, id)
	registry.mu.Unlock()
}

func (registry *HealthRegistry) All() []HealthRecord {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	records := make([]HealthRecord, 0, len(registry.records))
	for _, record := range registry.records {
		records = append(records, record)
	}
	registry.mu.RUnlock()
	sort.Slice(records, func(i, j int) bool { return records[i].ProviderID < records[j].ProviderID })
	return records
}
