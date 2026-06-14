// prefixes.go — provider-namespaced model ID prefix helpers.
//
// All models returned from /v1/models carry a provider-specific prefix so
// clients can target a provider deterministically:
//
//	gc-<id>   →  GitHub Copilot
//	oc-<id>   →  OpenAI Codex
//
// The prefix is a proxy-internal naming convention only; it is stripped before
// requests are forwarded to the upstream provider API.
package provider

import "strings"

const (
	// CopilotModelPrefix is the namespace prefix for GitHub Copilot models.
	CopilotModelPrefix = "gc-"
	// CodexModelPrefix is the namespace prefix for OpenAI Codex models.
	CodexModelPrefix = "oc-"
)

// ProviderPrefix returns the model ID prefix for the given provider.
// Returns an empty string for unrecognised providers.
func ProviderPrefix(providerID ProviderID) string {
	switch providerID {
	case ProviderCopilot:
		return CopilotModelPrefix
	case ProviderCodex:
		return CodexModelPrefix
	default:
		return ""
	}
}

// ProviderOwnedBy returns the canonical "owned_by" value for a provider.
func ProviderOwnedBy(providerID ProviderID) string {
	switch providerID {
	case ProviderCopilot:
		return "github-copilot"
	case ProviderCodex:
		return "openai"
	default:
		return "openai"
	}
}

// AddModelPrefix returns the model ID with the provider's namespace prefix
// prepended.  If the model already starts with the correct prefix it is
// returned unchanged.  If the provider has no prefix, the bare ID is returned.
func AddModelPrefix(providerID ProviderID, modelID string) string {
	prefix := ProviderPrefix(providerID)
	if prefix == "" || strings.HasPrefix(modelID, prefix) {
		return modelID
	}
	return prefix + modelID
}

// StripModelPrefix removes a known provider prefix from modelID and returns
// the bare ID, the identified provider, and true.  If no known prefix is found,
// the original modelID, an empty ProviderID, and false are returned.
func StripModelPrefix(modelID string) (bare string, provider ProviderID, ok bool) {
	switch {
	case strings.HasPrefix(modelID, CopilotModelPrefix):
		return modelID[len(CopilotModelPrefix):], ProviderCopilot, true
	case strings.HasPrefix(modelID, CodexModelPrefix):
		return modelID[len(CodexModelPrefix):], ProviderCodex, true
	default:
		return modelID, "", false
	}
}
