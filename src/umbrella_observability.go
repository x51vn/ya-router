// umbrella_observability.go emits structured, redacted routing-decision logs
// for umbrella (virtual) model selection. It records selection only — never
// cross-provider failover, which does not occur — and never logs prompts,
// completions, tokens, secrets, raw account IDs, or arbitrary upstream bodies.
package yarouter

import (
	"log"
	"strings"

	routingpkg "github.com/duvu/ya-router/internal/routing"
)

// logUmbrellaSelection records a single umbrella selection decision with stable
// fields bounded to the configured target list.
func logUmbrellaSelection(decision *routingpkg.SelectionDecision, finalProvider ProviderID) {
	if decision == nil {
		return
	}
	log.Printf("[umbrella] decision virtual_model=%q strategy=%q selected_target=%q target_index=%d provider=%s generation=%d catalog_fetched_at=%d skipped=[%s]",
		decision.VirtualModel,
		decision.Strategy,
		decision.SelectedTarget,
		decision.SelectedIndex,
		finalProvider,
		decision.Generation,
		decision.CatalogFetchedAt,
		formatSkipped(decision.Skipped),
	)
}

// logUmbrellaNoTarget records a no-active-target outcome. Each configured target
// is listed with only its stable reason code.
func logUmbrellaNoTarget(virtualModel string, err *routingpkg.NoActiveTargetError) {
	if err == nil {
		return
	}
	log.Printf("[umbrella] no_active_target virtual_model=%q skipped=[%s]",
		virtualModel, formatSkipped(err.Skipped))
}

// formatSkipped renders skipped targets as "index:target=reason" pairs. Target
// IDs come from configuration and reasons are stable codes, so cardinality is
// bounded by the configured target count.
func formatSkipped(skipped []routingpkg.SkippedTarget) string {
	if len(skipped) == 0 {
		return ""
	}
	parts := make([]string, 0, len(skipped))
	for _, item := range skipped {
		parts = append(parts, item.Target+"="+string(item.Reason))
	}
	return strings.Join(parts, " ")
}
