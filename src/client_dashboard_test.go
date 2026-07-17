package yarouter

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	clientpkg "github.com/duvu/ya-router/internal/client"
	controlpkg "github.com/duvu/ya-router/internal/control"
	providerpkg "github.com/duvu/ya-router/internal/provider"
	routingpkg "github.com/duvu/ya-router/internal/routing"
	"github.com/duvu/ya-router/internal/secret"
)

func TestClientWithoutCommandStartsDashboard(t *testing.T) {
	original := clientDashboardRunner
	t.Cleanup(func() { clientDashboardRunner = original })
	called := false
	clientDashboardRunner = func(clientCommonFlags) int {
		called = true
		return clientExitOK
	}

	if got := runClientCLI([]string{"ya"}); got != clientExitOK {
		t.Fatalf("exit code = %d", got)
	}
	if !called {
		t.Fatal("dashboard was not started")
	}
}

func TestDashboardModelOpensPalette(t *testing.T) {
	model := newDashboardModel(clientCommonFlags{}, dashboardTestBackend{})
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	updated := next.(dashboardModel)
	if updated.mode != dashboardPaletteMode {
		t.Fatalf("mode = %v, want palette", updated.mode)
	}
}

func TestDashboardViewShowsCooldownWithoutSecretValue(t *testing.T) {
	model := newDashboardModel(clientCommonFlags{}, dashboardTestBackend{})
	model.width = 120
	model.height = 40
	model.connected = true
	model.snapshot.routing = controlpkg.RoutingStatusResource{Capabilities: []routingpkg.VirtualModelReadiness{{
		VirtualModel:   "thiendu",
		Capability:     "chat",
		SelectedTarget: "codex/gpt-5.4-mini",
		Targets: []routingpkg.TargetReadiness{{
			Target:         "github/gpt-5-mini",
			Reason:         "cooldown",
			CooldownReason: "rate_limited",
			CooldownUntil:  1784174400,
		}},
	}}}
	model.snapshot.secrets = []secret.Metadata{{ID: "kilo/api_key", Configured: true}}
	model.mode = dashboardSecretMode
	model.secretSlot = "kilo/api_key"
	model.secretValue = "secret-value"

	view := model.View()
	if !strings.Contains(view, "cooldown") {
		t.Fatalf("view did not show cooldown: %s", view)
	}
	if strings.Contains(view, "secret-value") {
		t.Fatalf("view exposed secret value: %s", view)
	}
}

func TestDashboardToggleUsesCurrentConfigRevision(t *testing.T) {
	backend := &dashboardMutationBackend{}
	model := newDashboardModel(clientCommonFlags{}, backend)
	model.snapshot.config.Revision = 12
	model.snapshot.providers = []controlpkg.ProviderResource{{
		Descriptor: providerpkg.Descriptor{ID: "copilot"},
		Enabled:    true,
	}}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	next, _ = next.(dashboardModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	next, command := next.(dashboardModel).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if command == nil {
		t.Fatal("confirmation did not submit a mutation")
	}
	if _, ok := command().(dashboardActionMsg); !ok {
		t.Fatal("mutation did not produce an action message")
	}
	if backend.request.ExpectedRevision != 12 || backend.request.Enabled == nil || *backend.request.Enabled {
		t.Fatalf("mutation = %+v", backend.request)
	}
	_ = next
}

func TestDashboardShowsDeviceInstructionsForWaitingOperation(t *testing.T) {
	model := newDashboardModel(clientCommonFlags{}, dashboardTestBackend{})
	model.width, model.height = 120, 40
	model.snapshot.operations = []controlpkg.OperationResource{{
		Kind:  "auth_session",
		State: "waiting_for_user",
		Metadata: map[string]string{
			"verification_uri": "https://example.test/device",
			"user_code":        "ABCD-EFGH",
		},
	}}
	view := model.View()
	if !strings.Contains(view, "Open https://example.test/device and enter ABCD-EFGH.") {
		t.Fatalf("device instructions not rendered: %s", view)
	}
}

type dashboardTestBackend struct{}

type dashboardMutationBackend struct {
	dashboardTestBackend
	request controlpkg.MutationRequest
}

func (backend *dashboardMutationBackend) ApplyMutation(_ context.Context, request controlpkg.MutationRequest) (clientpkg.MutationResult, error) {
	backend.request = request
	return clientpkg.MutationResult{Applied: true}, nil
}

func (dashboardTestBackend) Meta(context.Context) (controlpkg.MetaResponse, error) {
	return controlpkg.MetaResponse{}, nil
}
func (dashboardTestBackend) Providers(context.Context) ([]controlpkg.ProviderResource, error) {
	return nil, nil
}
func (dashboardTestBackend) Accounts(context.Context) ([]controlpkg.AccountResource, error) {
	return nil, nil
}
func (dashboardTestBackend) Models(context.Context, bool) (controlpkg.ModelCatalogResponse, error) {
	return controlpkg.ModelCatalogResponse{}, nil
}
func (dashboardTestBackend) Configuration(context.Context) (controlpkg.ConfigResource, error) {
	return controlpkg.ConfigResource{}, nil
}
func (dashboardTestBackend) RoutingStatus(context.Context) (controlpkg.RoutingStatusResource, error) {
	return controlpkg.RoutingStatusResource{}, nil
}
func (dashboardTestBackend) Operations(context.Context) ([]controlpkg.OperationResource, error) {
	return nil, nil
}
func (dashboardTestBackend) Events(context.Context, uint64) (controlpkg.EventPage, error) {
	return controlpkg.EventPage{}, nil
}
func (dashboardTestBackend) Secrets(context.Context) ([]secret.Metadata, error) { return nil, nil }
func (dashboardTestBackend) ApplyMutation(context.Context, controlpkg.MutationRequest) (clientpkg.MutationResult, error) {
	return clientpkg.MutationResult{}, nil
}
func (dashboardTestBackend) CreateAuthSession(context.Context, clientpkg.AuthSessionRequest) (controlpkg.OperationResource, error) {
	return controlpkg.OperationResource{}, nil
}
func (dashboardTestBackend) CancelAuthSession(context.Context, string) (controlpkg.OperationResource, error) {
	return controlpkg.OperationResource{}, nil
}
func (dashboardTestBackend) SetSecret(context.Context, string, string) (secret.Metadata, error) {
	return secret.Metadata{}, nil
}

var _ dashboardBackend = dashboardTestBackend{}
