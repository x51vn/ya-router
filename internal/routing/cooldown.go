package routing

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/duvu/ya-router/internal/availability"
	"github.com/duvu/ya-router/internal/provider"
)

const (
	baseCooldown = 15 * time.Second
	maxCooldown  = 5 * time.Minute
)

type cooldownEntry struct {
	providerID provider.ID
	model      string
	capability provider.Capability
	until      time.Time
	reason     availability.CooldownReason
	failures   int
}

// CooldownRegistry stores bounded target feedback independently from immutable
// runtime snapshots. Snapshot builders copy its current state before routing.
type CooldownRegistry struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]cooldownEntry
}

func NewCooldownRegistry() *CooldownRegistry {
	return &CooldownRegistry{now: time.Now, entries: make(map[string]cooldownEntry)}
}

func cooldownEntryKey(providerID provider.ID, model string, capability provider.Capability) string {
	return string(providerID) + "\x00" + string(capability) + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

func (registry *CooldownRegistry) Record(providerID provider.ID, model string, capability provider.Capability, reason availability.CooldownReason, hint time.Duration) (time.Time, bool) {
	if registry == nil || providerID == "" || strings.TrimSpace(model) == "" || reason == "" {
		return time.Time{}, false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.now().UTC()
	key := cooldownEntryKey(providerID, model, capability)
	existing, exists := registry.entries[key]
	failures := 1
	if exists && existing.until.After(now) {
		failures = existing.failures + 1
	}
	duration := baseCooldown
	for step := 1; step < failures && duration < maxCooldown; step++ {
		duration *= 2
	}
	if duration > maxCooldown {
		duration = maxCooldown
	}
	if hint > 0 {
		if hint > maxCooldown {
			hint = maxCooldown
		}
		if hint > duration {
			duration = hint
		}
	}
	until := now.Add(duration)
	registry.entries[key] = cooldownEntry{
		providerID: providerID,
		model:      strings.TrimSpace(model),
		capability: capability,
		until:      until,
		reason:     reason,
		failures:   failures,
	}
	return until, true
}

func (registry *CooldownRegistry) Clear(providerID provider.ID, model string, capability provider.Capability) bool {
	if registry == nil {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := cooldownEntryKey(providerID, model, capability)
	if _, exists := registry.entries[key]; !exists {
		return false
	}
	delete(registry.entries, key)
	return true
}

func (registry *CooldownRegistry) Cooldowns(providerID provider.ID) (active []availability.Cooldown, expired []availability.Cooldown) {
	if registry == nil {
		return nil, nil
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := registry.now().UTC()
	for key, entry := range registry.entries {
		if entry.providerID != providerID {
			continue
		}
		cooldown := availability.Cooldown{Model: entry.model, Capability: entry.capability, Until: entry.until, Reason: entry.reason}
		if entry.until.After(now) {
			active = append(active, cooldown)
			continue
		}
		delete(registry.entries, key)
		expired = append(expired, cooldown)
	}
	return active, expired
}

func cooldownReason(status int, err error) (availability.CooldownReason, bool) {
	if errors.Is(err, context.Canceled) {
		return "", false
	}
	if errors.Is(err, context.DeadlineExceeded) || status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout {
		return availability.CooldownTimeout, true
	}
	switch status {
	case http.StatusTooManyRequests:
		return availability.CooldownRateLimited, true
	case http.StatusUnauthorized:
		return availability.CooldownAuthRequired, true
	case http.StatusPaymentRequired, http.StatusForbidden:
		return availability.CooldownEntitlementDenied, true
	}
	if status >= http.StatusInternalServerError || err != nil {
		return availability.CooldownTransientFailure, true
	}
	return "", false
}

// RecordOutcome consumes a selected route's redacted result after dispatch.
// It only affects later automatic-routing requests; callers never retry the
// triggering request through another provider.
func (router *Router) RecordOutcome(decision *SelectionDecision, status int, err error, retryAfter time.Duration) {
	if router == nil || decision == nil {
		return
	}
	bare, providerID, hasPrefix := StripModelPrefix(decision.SelectedTarget)
	if !hasPrefix {
		return
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices && err == nil {
		if router.cooldowns.Clear(providerID, bare, decision.Capability) {
			router.metrics.RecordCooldownExit(decision.SelectedTarget, "recovered")
		}
		return
	}
	reason, shouldCooldown := cooldownReason(status, err)
	if !shouldCooldown {
		return
	}
	if _, entered := router.cooldowns.Record(providerID, bare, decision.Capability, reason, retryAfter); entered {
		router.metrics.RecordCooldownEntry(decision.SelectedTarget, reason)
	}
}
