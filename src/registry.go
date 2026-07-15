// registry.go exposes the compatibility names used by existing providers and
// tests while construction remains side-effect free.
package yarouter

import providerpkg "github.com/duvu/ya-router/internal/provider"

type ProviderRegistry = providerpkg.Registry

func NewProviderRegistry() *ProviderRegistry {
	return providerpkg.NewRegistry()
}
