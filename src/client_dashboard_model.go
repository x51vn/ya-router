package yarouter

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	controlpkg "github.com/duvu/ya-router/internal/control"
	"github.com/duvu/ya-router/internal/secret"
)

type dashboardMode uint8

const (
	dashboardMainMode dashboardMode = iota
	dashboardPaletteMode
	dashboardConfirmMode
	dashboardSecretMode
)

type dashboardSnapshot struct {
	meta       controlpkg.MetaResponse
	providers  []controlpkg.ProviderResource
	accounts   []controlpkg.AccountResource
	models     controlpkg.ModelCatalogResponse
	config     controlpkg.ConfigResource
	routing    controlpkg.RoutingStatusResource
	operations []controlpkg.OperationResource
	events     controlpkg.EventPage
	secrets    []secret.Metadata
}

type dashboardLoadedMsg struct {
	snapshot dashboardSnapshot
	err      error
}

type dashboardActionMsg struct{ err error }
type dashboardTickMsg struct{}

type dashboardModel struct {
	backend          dashboardBackend
	common           clientCommonFlags
	snapshot         dashboardSnapshot
	mode             dashboardMode
	selectedProvider int
	width            int
	height           int
	connected        bool
	status           string
	confirm          string
	confirmSecret    bool
	pending          func(context.Context) error
	secretSlot       string
	secretValue      string
}

func newDashboardModel(common clientCommonFlags, backend dashboardBackend) dashboardModel {
	return dashboardModel{backend: backend, common: common, status: "Connecting to daemon..."}
}

func (model dashboardModel) Init() tea.Cmd {
	return tea.Batch(model.load(false), dashboardTick())
}

func (model dashboardModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		model.width, model.height = msg.Width, msg.Height
	case dashboardLoadedMsg:
		if msg.err != nil {
			model.connected = false
			model.status = "Daemon unavailable; press r to reconnect."
			return model, nil
		}
		model.snapshot = msg.snapshot
		model.connected = true
		if model.status == "Connecting to daemon..." || !strings.HasPrefix(model.status, "Action") {
			model.status = "Connected."
		}
		model.clampSelection()
	case dashboardActionMsg:
		if msg.err != nil {
			model.status = "Action was not applied; state reloaded."
		} else {
			model.status = "Action accepted; state refreshed."
		}
		return model, model.load(false)
	case dashboardTickMsg:
		return model, tea.Batch(model.load(false), dashboardTick())
	case tea.KeyMsg:
		return model.handleKey(msg)
	}
	return model, nil
}

func (model dashboardModel) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "ctrl+c" || (key.String() == "q" && model.mode != dashboardSecretMode) {
		return model, tea.Quit
	}
	switch model.mode {
	case dashboardSecretMode:
		return model.handleSecretKey(key)
	case dashboardConfirmMode:
		return model.handleConfirmKey(key)
	case dashboardPaletteMode:
		return model.handlePaletteKey(key)
	default:
		if key.String() == "a" {
			model.mode = dashboardPaletteMode
			model.status = "Action palette open."
			return model, nil
		}
		if key.String() == "r" {
			model.status = "Reconnecting..."
			return model, model.load(false)
		}
	}
	return model, nil
}

func (model dashboardModel) handlePaletteKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "a":
		model.mode = dashboardMainMode
	case "up", "k":
		model.selectedProvider--
		model.clampSelection()
	case "down", "j":
		model.selectedProvider++
		model.clampSelection()
	case "r":
		model.status = "Reconnecting..."
		return model, model.load(false)
	case "m":
		model.status = "Refreshing provider catalogs..."
		return model, model.load(true)
	case "e":
		model.prepareToggle()
	case "c":
		model.status = "Starting authentication operation..."
		return model, model.startAuth()
	case "x":
		return model, model.cancelAuth()
	case "p":
		model.prepareSecret()
	}
	return model, nil
}

func (model dashboardModel) handleConfirmKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.String() == "y" {
		if model.confirmSecret {
			model.confirmSecret = false
			model.pending = nil
			model.mode = dashboardSecretMode
			return model, nil
		}
		model.mode = dashboardPaletteMode
		action := model.pending
		model.pending = nil
		if action != nil {
			return model, model.action(action)
		}
	}
	if key.String() == "n" || key.String() == "esc" {
		model.mode = dashboardPaletteMode
		model.pending = nil
		model.status = "Action cancelled."
	}
	return model, nil
}
