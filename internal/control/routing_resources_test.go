package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/duvu/ya-router/internal/availability"
	routingpkg "github.com/duvu/ya-router/internal/routing"
)

type routingStatusReader struct{}

func (routingStatusReader) RoutingStatus(context.Context, string) (RoutingStatusResource, bool, error) {
	return RoutingStatusResource{
		PublicID:       "thiendu",
		ConfigRevision: 9,
		Generation:     4,
		Capabilities: []routingpkg.VirtualModelReadiness{{
			VirtualModel: "thiendu", Capability: "chat", Active: true, SelectedTarget: "github/gpt-5-mini",
			Targets: []routingpkg.TargetReadiness{{Target: "github/gpt-5-mini", Routable: true, Reason: availability.ReasonRoutable}},
		}},
		Counters: []routingpkg.Counter{{Name: "umbrella_cooldown_entries_total", Labels: map[string]string{"target": "github/gpt-5-mini", "reason": "rate_limited"}, Value: 1}},
	}, true, nil
}

func TestRoutingStatusRouteReturnsCapabilitySelections(t *testing.T) {
	api := NewAPI(APIOptions{})
	RegisterRoutingStatusRoutes(api, routingStatusReader{})
	request := httptest.NewRequest(http.MethodGet, "/control/v1/routing/thiendu", nil)
	response := httptest.NewRecorder()
	api.Handler(FixedAuthenticator(Identity{Subject: "viewer", Role: RoleViewer, Source: "test"})).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if body := response.Body.String(); !containsAll(body, "thiendu", "github/gpt-5-mini", "routable", "umbrella_cooldown_entries_total") {
		t.Fatalf("routing status = %s", body)
	}
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
