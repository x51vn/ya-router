// router.go preserves the existing service API over the isolated routing package.
package yarouter

import routingpkg "github.com/duvu/ya-router/internal/routing"

type RouteResult = routingpkg.Result
type ModelRouter = routingpkg.Router

func NewModelRouter(registry *ProviderRegistry, routing RoutingConfig) *ModelRouter {
	return routingpkg.NewRouter(registry, routing)
}
