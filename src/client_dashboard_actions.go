package yarouter

import (
	"context"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	clientpkg "github.com/duvu/ya-router/internal/client"
	controlpkg "github.com/duvu/ya-router/internal/control"
)

func (model dashboardModel) handleSecretKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		model.clearSecret()
		model.mode = dashboardPaletteMode
		model.status = "Secret entry cancelled."
	case "enter":
		if model.secretValue != "" {
			slot, value := model.secretSlot, model.secretValue
			model.clearSecret()
			model.mode = dashboardPaletteMode
			return model, model.action(func(ctx context.Context) error {
				if _, err := model.backend.SetSecret(ctx, slot, value); err != nil {
					return err
				}
				providerID := strings.TrimSuffix(slot, "/api_key")
				_, err := model.backend.CreateAuthSession(ctx, clientpkg.AuthSessionRequest{ProviderID: providerID, Method: "api_key"})
				return err
			})
		}
	case "backspace":
		if length := len([]rune(model.secretValue)); length > 0 {
			model.secretValue = string([]rune(model.secretValue)[:length-1])
		}
	default:
		if key.Type == tea.KeyRunes && !key.Alt && !key.Paste {
			for _, r := range key.Runes {
				if unicode.IsPrint(r) && r != '\n' && r != '\r' {
					model.secretValue += string(r)
				}
			}
		}
	}
	return model, nil
}

func (model *dashboardModel) prepareToggle() {
	provider, ok := model.currentProvider()
	if !ok {
		model.status = "Choose a provider first."
		return
	}
	enabled := !provider.Enabled
	model.pending = func(ctx context.Context) error {
		_, err := model.backend.ApplyMutation(ctx, controlpkg.MutationRequest{
			Kind: controlpkg.MutationProviderEnabled, ExpectedRevision: model.snapshot.config.Revision,
			Provider: string(provider.Descriptor.ID), Enabled: &enabled,
		})
		return err
	}
	model.confirmSecret = false
	model.mode = dashboardConfirmMode
	if provider.Enabled {
		model.confirm = "Disable the selected provider? Existing requests will drain safely."
		return
	}
	model.confirm = "Enable the selected provider?"
}

func (model dashboardModel) startAuth() tea.Cmd {
	provider, ok := model.currentProvider()
	if !ok {
		return nil
	}
	method := "device_code"
	if provider.Descriptor.ID == "kilo" {
		method = "anonymous"
	}
	return model.action(func(ctx context.Context) error {
		_, err := model.backend.CreateAuthSession(ctx, clientpkg.AuthSessionRequest{ProviderID: string(provider.Descriptor.ID), Method: method})
		return err
	})
}

func (model *dashboardModel) cancelAuth() tea.Cmd {
	for _, operation := range model.snapshot.operations {
		if operation.Cancelable && !operation.State.Terminal() {
			return model.action(func(ctx context.Context) error {
				_, err := model.backend.CancelAuthSession(ctx, operation.ID)
				return err
			})
		}
	}
	model.status = "No cancelable authentication operation."
	return nil
}

func (model *dashboardModel) prepareSecret() {
	provider, ok := model.currentProvider()
	if !ok || (provider.Descriptor.ID != "codex" && provider.Descriptor.ID != "kilo") {
		model.status = "API-key entry is available for Codex or Kilo."
		return
	}
	slot := string(provider.Descriptor.ID) + "/api_key"
	model.secretSlot = slot
	model.confirmSecret = false
	for _, item := range model.snapshot.secrets {
		if item.ID == slot && item.Configured {
			model.confirm = "Replace the existing provider credential?"
			model.confirmSecret = true
			model.mode = dashboardConfirmMode
			return
		}
	}
	model.mode = dashboardSecretMode
}

func (model *dashboardModel) clearSecret() {
	model.secretSlot = ""
	model.secretValue = ""
}

func (model *dashboardModel) clampSelection() {
	if len(model.snapshot.providers) == 0 {
		model.selectedProvider = 0
		return
	}
	if model.selectedProvider < 0 {
		model.selectedProvider = len(model.snapshot.providers) - 1
	}
	if model.selectedProvider >= len(model.snapshot.providers) {
		model.selectedProvider = 0
	}
}

func (model dashboardModel) currentProvider() (controlpkg.ProviderResource, bool) {
	if model.selectedProvider < 0 || model.selectedProvider >= len(model.snapshot.providers) {
		return controlpkg.ProviderResource{}, false
	}
	return model.snapshot.providers[model.selectedProvider], true
}
