package control

import (
	"context"
	"net/http"

	routingpkg "github.com/duvu/ya-router/internal/routing"
)

type RoutingStatusResource struct {
	PublicID       string                             `json:"public_id"`
	ConfigRevision uint64                             `json:"config_revision"`
	Generation     uint64                             `json:"generation"`
	Capabilities   []routingpkg.VirtualModelReadiness `json:"capabilities"`
	Counters       []routingpkg.Counter               `json:"counters"`
}

type RoutingStatusReader interface {
	RoutingStatus(context.Context, string) (RoutingStatusResource, bool, error)
}

func RegisterRoutingStatusRoutes(api *API, reader RoutingStatusReader) {
	if reader == nil {
		return
	}
	api.Handle(http.MethodGet, "/control/v1/routing/thiendu", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		resource, exists, err := reader.RoutingStatus(request.Context(), "thiendu")
		if err != nil {
			writeError(writer, request, http.StatusServiceUnavailable, "routing_status_unavailable", "The routing status is temporarily unavailable.", true, nil)
			return
		}
		if !exists {
			writeError(writer, request, http.StatusNotFound, "routing_status_not_found", "The requested routing status was not found.", false, nil)
			return
		}
		writeJSON(writer, http.StatusOK, resource)
	}))
}
