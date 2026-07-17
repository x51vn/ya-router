package routing

import (
	"strings"

	"github.com/duvu/ya-router/internal/provider"
)

const (
	CopilotModelPrefix = "github/"
	CodexModelPrefix   = "codex/"
	KiloModelPrefix    = "kilo/"
)

// KnownPrefixes returns every provider model-ID prefix in deterministic order.
// Umbrella-model validation uses it to classify target IDs.
func KnownPrefixes() []string {
	return []string{CopilotModelPrefix, CodexModelPrefix, KiloModelPrefix}
}

// ProviderPrefix returns the model ID prefix for the given provider.
func ProviderPrefix(providerID provider.ID) string {
	switch providerID {
	case provider.Copilot:
		return CopilotModelPrefix
	case provider.Codex:
		return CodexModelPrefix
	case provider.Kilo:
		return KiloModelPrefix
	default:
		return ""
	}
}

// ProviderOwnedBy returns the canonical owned_by value for a provider.
func ProviderOwnedBy(providerID provider.ID) string {
	switch providerID {
	case provider.Copilot:
		return "github-copilot"
	case provider.Codex:
		return "openai"
	case provider.Kilo:
		return "kilo"
	default:
		return "openai"
	}
}

// AddModelPrefix adds the provider namespace unless it is already present.
func AddModelPrefix(providerID provider.ID, modelID string) string {
	prefix := ProviderPrefix(providerID)
	if prefix == "" || strings.HasPrefix(modelID, prefix) {
		return modelID
	}
	return prefix + modelID
}

// StripModelPrefix separates a known provider prefix from a model ID.
func StripModelPrefix(modelID string) (bare string, providerID provider.ID, ok bool) {
	switch {
	case strings.HasPrefix(modelID, CopilotModelPrefix):
		return modelID[len(CopilotModelPrefix):], provider.Copilot, true
	case strings.HasPrefix(modelID, CodexModelPrefix):
		return modelID[len(CodexModelPrefix):], provider.Codex, true
	case strings.HasPrefix(modelID, KiloModelPrefix):
		return modelID[len(KiloModelPrefix):], provider.Kilo, true
	default:
		return modelID, "", false
	}
}
