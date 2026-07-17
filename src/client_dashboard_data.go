package yarouter

import (
	"context"
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	clientpkg "github.com/duvu/ya-router/internal/client"
)

func (model dashboardModel) load(refresh bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), dashboardRequestTimeout(model.common))
		defer cancel()
		snapshot, err := loadDashboard(ctx, model.backend, refresh)
		return dashboardLoadedMsg{snapshot: snapshot, err: err}
	}
}

func loadDashboard(ctx context.Context, backend dashboardBackend, refresh bool) (dashboardSnapshot, error) {
	var snapshot dashboardSnapshot
	var err error
	if snapshot.meta, err = backend.Meta(ctx); err != nil {
		return snapshot, err
	}
	if snapshot.providers, err = backend.Providers(ctx); err != nil {
		return snapshot, err
	}
	if snapshot.accounts, err = backend.Accounts(ctx); err != nil {
		return snapshot, err
	}
	if snapshot.models, err = backend.Models(ctx, refresh); err != nil {
		return snapshot, err
	}
	if snapshot.config, err = backend.Configuration(ctx); err != nil {
		return snapshot, err
	}
	if snapshot.routing, err = backend.RoutingStatus(ctx); err != nil {
		var apiErr *clientpkg.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 404 {
			return snapshot, err
		}
	}
	if snapshot.operations, err = backend.Operations(ctx); err != nil {
		return snapshot, err
	}
	if snapshot.events, err = backend.Events(ctx, 0); err != nil {
		return snapshot, err
	}
	snapshot.secrets, err = backend.Secrets(ctx)
	return snapshot, err
}

func dashboardTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return dashboardTickMsg{} })
}

func dashboardRequestTimeout(common clientCommonFlags) time.Duration {
	if common.timeout > 0 {
		return time.Duration(common.timeout+5) * time.Second
	}
	return clientpkg.DefaultTimeout + 5*time.Second
}

func (model dashboardModel) action(action func(context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), dashboardRequestTimeout(model.common))
		defer cancel()
		return dashboardActionMsg{err: action(ctx)}
	}
}
