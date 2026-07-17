package routing

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/duvu/ya-router/internal/availability"
	"github.com/duvu/ya-router/internal/provider"
)

func TestCooldownSkipsLaterRequestAndExpires(t *testing.T) {
	now := time.Unix(2_000, 0).UTC()
	originalNow := nowFunc
	nowFunc = func() time.Time { return now }
	t.Cleanup(func() { nowFunc = originalNow })
	cooldowns := NewCooldownRegistry()
	cooldowns.now = func() time.Time { return now }
	copilot := &umbrellaProvider{id: provider.Copilot, authenticated: true, hasCatalog: true, catalog: []string{"gpt-5-mini"}}
	codex := &umbrellaProvider{id: provider.Codex, authenticated: true, hasCatalog: true, catalog: []string{"gpt-5.4-mini"}}
	registry := provider.NewRegistry(copilot, codex)
	router := NewRouterWithCooldowns(registry, umbrellaRouting("github/gpt-5-mini", "codex/gpt-5.4-mini"), cooldowns)

	first, err := router.Resolve(context.Background(), "router/auto", provider.CapabilityChat)
	if err != nil || first.Provider.ID() != provider.Copilot {
		t.Fatalf("first route = %+v, err = %v", first, err)
	}
	router.RecordOutcome(first.Selection, http.StatusTooManyRequests, nil, 30*time.Second)

	second, err := router.Resolve(context.Background(), "router/auto", provider.CapabilityChat)
	if err != nil || second.Provider.ID() != provider.Codex {
		t.Fatalf("second route = %+v, err = %v", second, err)
	}
	readiness := router.VirtualModelReadinessFor(provider.CapabilityChat, router.AvailabilitySnapshot())
	if len(readiness) != 1 || len(readiness[0].Targets) != 2 {
		t.Fatalf("readiness = %+v", readiness)
	}
	cooled := readiness[0].Targets[0]
	if cooled.Reason != availability.ReasonCooldown || cooled.CooldownUntil != now.Add(30*time.Second).Unix() || cooled.CooldownReason != availability.CooldownRateLimited {
		t.Fatalf("cooldown state = %+v", cooled)
	}

	now = now.Add(31 * time.Second)
	third, err := router.Resolve(context.Background(), "router/auto", provider.CapabilityChat)
	if err != nil || third.Provider.ID() != provider.Copilot {
		t.Fatalf("third route = %+v, err = %v", third, err)
	}
	counts := map[string]uint64{}
	for _, counter := range router.Metrics().Snapshot() {
		counts[counter.Name] += counter.Value
	}
	if counts["umbrella_cooldown_entries_total"] != 1 || counts["umbrella_cooldown_exits_total"] != 1 {
		t.Fatalf("cooldown counters = %+v", counts)
	}
}
