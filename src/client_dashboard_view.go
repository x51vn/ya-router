package yarouter

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	controlpkg "github.com/duvu/ya-router/internal/control"
	"github.com/duvu/ya-router/internal/routing"
)

func (model dashboardModel) View() string {
	if model.width > 0 && (model.width < 72 || model.height < 20) {
		return "ya-router dashboard\n\nTerminal is too small. Resize to at least 72x20.\n\nq quit\n"
	}
	var view strings.Builder
	state := "offline"
	if model.connected {
		state = "connected"
	}
	fmt.Fprintf(&view, "ya-router dashboard  [%s]\n", state)
	fmt.Fprintf(&view, "daemon %s  config revision %d  deployment %s\n", nonEmpty(model.snapshot.meta.ServiceVersion, "unknown"), model.snapshot.config.Revision, nonEmpty(model.snapshot.meta.DeploymentMode, "unknown"))
	fmt.Fprintf(&view, "%s\n\n", model.status)
	model.writeProviders(&view)
	model.writeRouting(&view)
	model.writeCatalogs(&view)
	model.writeOperations(&view)
	model.writeEvents(&view)
	model.writeFooter(&view)
	return view.String()
}

func (model dashboardModel) writeProviders(view *strings.Builder) {
	view.WriteString("Providers\n")
	if len(model.snapshot.providers) == 0 {
		view.WriteString("  No provider state is available.\n\n")
		return
	}
	for index, provider := range model.snapshot.providers {
		marker := " "
		if model.mode == dashboardPaletteMode && index == model.selectedProvider {
			marker = ">"
		}
		credential := providerCredential(model.snapshot.accounts, string(provider.Descriptor.ID))
		fmt.Fprintf(view, "%s %-8s enabled=%-5t state=%-22s auth=%-5t credential=%s\n",
			marker, provider.Descriptor.ID, provider.Enabled, provider.Health.State, provider.Health.Health.Authenticated, credential)
	}
	view.WriteString("\n")
}

func providerCredential(accounts []controlpkg.AccountResource, providerID string) string {
	for _, account := range accounts {
		if string(account.ProviderID) == providerID && account.Credential.Configured {
			return string(account.Credential.Source)
		}
	}
	return "redacted"
}

func (model dashboardModel) writeRouting(view *strings.Builder) {
	view.WriteString("thiendu routing\n")
	if len(model.snapshot.routing.Capabilities) == 0 {
		view.WriteString("  Routing status is unavailable.\n\n")
		return
	}
	for _, capability := range model.snapshot.routing.Capabilities {
		selected := capability.SelectedTarget
		if selected == "" {
			selected = "no eligible target"
		}
		fmt.Fprintf(view, "  %-10s selected=%s\n", capability.Capability, selected)
		for _, target := range capability.Targets {
			fmt.Fprintf(view, "    %-28s %s\n", target.Target, readinessText(target))
		}
	}
	view.WriteString("\n")
}

func readinessText(target routing.TargetReadiness) string {
	if target.Routable {
		return "ready"
	}
	if target.Reason == "cooldown" {
		until := ""
		if target.CooldownUntil > 0 {
			until = " until " + time.Unix(target.CooldownUntil, 0).UTC().Format("15:04:05Z")
		}
		return "cooldown " + string(target.CooldownReason) + until
	}
	return string(target.Reason)
}

func (model dashboardModel) writeCatalogs(view *strings.Builder) {
	view.WriteString("Available prefixed models\n")
	if len(model.snapshot.models.Catalogs) == 0 {
		view.WriteString("  No provider catalogs are available.\n\n")
		return
	}
	for _, catalog := range model.snapshot.models.Catalogs {
		modelIDs := make([]string, 0, min(len(catalog.Models), 5))
		for index, item := range catalog.Models {
			if index == 5 {
				break
			}
			modelIDs = append(modelIDs, item.ID)
		}
		fmt.Fprintf(view, "  %s: %s", catalog.ProviderID, strings.Join(modelIDs, ", "))
		if len(catalog.Models) > len(modelIDs) {
			fmt.Fprintf(view, " (+%d)", len(catalog.Models)-len(modelIDs))
		}
		if catalog.Stale {
			view.WriteString(" [stale]")
		}
		view.WriteString("\n")
	}
	view.WriteString("\n")
}

func (model dashboardModel) writeOperations(view *strings.Builder) {
	view.WriteString("Current operations\n")
	if len(model.snapshot.operations) == 0 {
		view.WriteString("  None.\n\n")
		return
	}
	for index, operation := range model.snapshot.operations {
		if index == 5 {
			break
		}
		fmt.Fprintf(view, "  %-18s %-17s %3d%% cancelable=%t\n", operation.Kind, operation.State, operation.Progress, operation.Cancelable)
		if operation.State == "waiting_for_user" {
			writeDeviceInstructions(view, operation.Metadata)
		}
	}
	view.WriteString("\n")
}

func writeDeviceInstructions(view *strings.Builder, metadata map[string]string) {
	uri := strings.TrimSpace(metadata["verification_uri"])
	code := strings.TrimSpace(metadata["user_code"])
	if uri == "" || code == "" {
		view.WriteString("    Complete the device authorization in its trusted browser flow.\n")
		return
	}
	fmt.Fprintf(view, "    Open %s and enter %s.\n", uri, code)
}

func (model dashboardModel) writeEvents(view *strings.Builder) {
	view.WriteString("Recent lifecycle events\n")
	if len(model.snapshot.events.Data) == 0 {
		view.WriteString("  None.\n\n")
		return
	}
	start := len(model.snapshot.events.Data) - 5
	if start < 0 {
		start = 0
	}
	for _, event := range model.snapshot.events.Data[start:] {
		fmt.Fprintf(view, "  %-22s provider=%s reason=%s\n", event.Type, event.ProviderID, event.Reason)
	}
	view.WriteString("\n")
}

func (model dashboardModel) writeFooter(view *strings.Builder) {
	switch model.mode {
	case dashboardPaletteMode:
		view.WriteString("Actions: ↑/↓ select  e enable/disable  c authenticate  p API key  x cancel auth  m refresh catalog  r reconnect  a close  q quit\n")
	case dashboardConfirmMode:
		fmt.Fprintf(view, "%s  [y] confirm  [n] cancel\n", model.confirm)
	case dashboardSecretMode:
		fmt.Fprintf(view, "Enter API key for %s: %s  [enter] save  [esc] cancel\n", model.secretSlot, strings.Repeat("•", utf8.RuneCountInString(model.secretValue)))
	default:
		view.WriteString("a actions  r reconnect  q quit\n")
	}
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
