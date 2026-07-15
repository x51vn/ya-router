// provider.go keeps source compatibility while provider contracts live in an
// importable internal package.
package yarouter

import providerpkg "github.com/duvu/ya-router/internal/provider"

type ProviderID = providerpkg.ID
type Capability = providerpkg.Capability
type ProviderHealth = providerpkg.Health
type Provider = providerpkg.Provider

const (
	ProviderCopilot = providerpkg.Copilot
	ProviderCodex   = providerpkg.Codex
	ProviderKilo    = providerpkg.Kilo

	CapabilityChat       = providerpkg.CapabilityChat
	CapabilityResponses  = providerpkg.CapabilityResponses
	CapabilityEmbeddings = providerpkg.CapabilityEmbeddings
)
