// prefixes.go preserves the existing public helpers over internal routing.
package yarouter

import routingpkg "github.com/duvu/ya-router/internal/routing"

const (
	CopilotModelPrefix = routingpkg.CopilotModelPrefix
	CodexModelPrefix   = routingpkg.CodexModelPrefix
	KiloModelPrefix    = routingpkg.KiloModelPrefix
)

func ProviderPrefix(providerID ProviderID) string {
	return routingpkg.ProviderPrefix(providerID)
}

func ProviderOwnedBy(providerID ProviderID) string {
	return routingpkg.ProviderOwnedBy(providerID)
}

func AddModelPrefix(providerID ProviderID, modelID string) string {
	return routingpkg.AddModelPrefix(providerID, modelID)
}

func StripModelPrefix(modelID string) (bare string, providerID ProviderID, ok bool) {
	return routingpkg.StripModelPrefix(modelID)
}
