// client_output.go renders human-readable text for `ya` read commands. JSON
// output is produced separately by emitJSON; these renderers are for terminal
// operators and never print secret material (control resources are already
// redacted server-side).
package yarouter

import (
	"fmt"
	"strings"
	"time"

	clientpkg "github.com/duvu/ya-router/internal/client"
	controlpkg "github.com/duvu/ya-router/internal/control"
	secretpkg "github.com/duvu/ya-router/internal/secret"
)

func emitText(_ string, result any) int {
	switch value := result.(type) {
	case controlpkg.MetaResponse:
		renderMeta(value)
	case []controlpkg.ProviderResource:
		renderProviders(value)
	case []controlpkg.AccountResource:
		renderAccounts(value)
	case controlpkg.ModelCatalogResponse:
		renderModels(value)
	case controlpkg.ConfigResource:
		renderConfig(value)
	case controlpkg.RoutingStatusResource:
		renderRoutingStatus(value)
	case []controlpkg.OperationResource:
		renderOperations(value)
	case controlpkg.OperationResource:
		renderOperations([]controlpkg.OperationResource{value})
	case controlpkg.EventPage:
		renderEvents(value)
	case []secretpkg.Metadata:
		renderSecrets(value)
	default:
		fmt.Printf("%v\n", value)
	}
	return clientExitOK
}

func renderRoutingStatus(status controlpkg.RoutingStatusResource) {
	w := tabWriter()
	fmt.Fprintf(w, "Public model:\t%s\n", status.PublicID)
	fmt.Fprintf(w, "Config revision:\t%d\n", status.ConfigRevision)
	fmt.Fprintf(w, "Runtime generation:\t%d\n", status.Generation)
	fmt.Fprintln(w, "CAPABILITY\tACTIVE\tSELECTED TARGET\tTARGET\tROUTABLE\tREASON\tCOOLDOWN UNTIL\tCOOLDOWN REASON\tCATALOG FETCHED\tCATALOG STALE")
	for _, capability := range status.Capabilities {
		for _, target := range capability.Targets {
			fmt.Fprintf(w, "%s\t%t\t%s\t%s\t%t\t%s\t%d\t%s\t%d\t%t\n", capability.Capability, capability.Active, capability.SelectedTarget, target.Target, target.Routable, target.Reason, target.CooldownUntil, target.CooldownReason, target.CatalogFetchedAt, target.CatalogStale)
		}
	}
	for _, counter := range status.Counters {
		fmt.Fprintf(w, "Counter:\t%s\t%v\n", counter.Name, counter.Labels)
	}
	_ = w.Flush()
}

func renderMeta(meta controlpkg.MetaResponse) {
	w := tabWriter()
	fmt.Fprintf(w, "Service version:\t%s\n", meta.ServiceVersion)
	fmt.Fprintf(w, "Control APIs:\t%s\n", strings.Join(meta.ControlAPIs, ", "))
	fmt.Fprintf(w, "Deployment mode:\t%s\n", meta.DeploymentMode)
	fmt.Fprintf(w, "Config revision:\t%d\n", meta.ConfigRevision)
	fmt.Fprintf(w, "Restart required:\t%t\n", meta.RestartRequired)
	fmt.Fprintf(w, "Client version:\t%s (compatible=%t, window=[%s, %s])\n",
		clientpkg.ClientVersion, meta.Client.Compatible, meta.Client.Minimum, meta.Client.Maximum)
	if len(meta.Features) > 0 {
		fmt.Fprintf(w, "Features:\t%s\n", strings.Join(meta.Features, ", "))
	}
	_ = w.Flush()
}

func renderProviders(providers []controlpkg.ProviderResource) {
	if len(providers) == 0 {
		fmt.Println("No providers.")
		return
	}
	w := tabWriter()
	fmt.Fprintln(w, "PROVIDER\tENABLED\tSTATE\tAUTHENTICATED\tCAPABILITIES\tGENERATION")
	for _, provider := range providers {
		caps := make([]string, 0, len(provider.EffectiveCapabilities))
		for _, capability := range provider.EffectiveCapabilities {
			caps = append(caps, string(capability))
		}
		fmt.Fprintf(w, "%s\t%t\t%s\t%t\t%s\t%d\n",
			provider.Descriptor.ID,
			provider.Enabled,
			provider.Health.State,
			provider.Health.Health.Authenticated,
			strings.Join(caps, ","),
			provider.Generation,
		)
	}
	_ = w.Flush()
}

func renderAccounts(accounts []controlpkg.AccountResource) {
	if len(accounts) == 0 {
		fmt.Println("No accounts.")
		return
	}
	w := tabWriter()
	fmt.Fprintln(w, "PROVIDER\tACCOUNT\tLABEL\tENABLED\tPRIORITY\tCREDENTIAL\tSOURCE")
	for _, account := range accounts {
		credential := "unconfigured"
		if account.Credential.Configured {
			credential = "configured"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%d\t%s\t%s\n",
			account.ProviderID, account.ID, account.Label, account.Enabled,
			account.Priority, credential, account.Credential.Source)
	}
	_ = w.Flush()
}

func renderModels(catalog controlpkg.ModelCatalogResponse) {
	if len(catalog.Catalogs) == 0 {
		fmt.Println("No model catalogs.")
		return
	}
	w := tabWriter()
	fmt.Fprintln(w, "PROVIDER\tMODEL\tAVAILABLE\tSTALE\tAGE")
	for _, provider := range catalog.Catalogs {
		age := "-"
		if provider.AgeSeconds > 0 {
			age = (time.Duration(provider.AgeSeconds) * time.Second).String()
		}
		if len(provider.Models) == 0 {
			fmt.Fprintf(w, "%s\t(none)\t%t\t%t\t%s\n", provider.ProviderID, provider.Available, provider.Stale, age)
			continue
		}
		for _, model := range provider.Models {
			fmt.Fprintf(w, "%s\t%s\t%t\t%t\t%s\n",
				provider.ProviderID, model.ID, model.Available, provider.Stale, age)
		}
	}
	_ = w.Flush()
}

func renderConfig(config controlpkg.ConfigResource) {
	w := tabWriter()
	fmt.Fprintf(w, "Revision:\t%d\n", config.Revision)
	fmt.Fprintf(w, "Digest:\t%s\n", config.Digest)
	fmt.Fprintf(w, "Effective digest:\t%s\n", config.EffectiveDigest)
	if len(config.RestartRequired) > 0 {
		fmt.Fprintf(w, "Restart required:\t%s\n", strings.Join(config.RestartRequired, ", "))
	} else {
		fmt.Fprintf(w, "Restart required:\tno\n")
	}
	if config.Effective != nil {
		fmt.Fprintf(w, "Default model:\t%s\n", config.Effective.Routing.DefaultModel)
		fmt.Fprintf(w, "Default provider:\t%s\n", config.Effective.Routing.DefaultProvider)
		fmt.Fprintf(w, "Virtual models:\t%d\n", len(config.Effective.Routing.VirtualModels))
	}
	_ = w.Flush()
}

func renderOperations(operations []controlpkg.OperationResource) {
	if len(operations) == 0 {
		fmt.Println("No operations.")
		return
	}
	w := tabWriter()
	fmt.Fprintln(w, "ID\tKIND\tSTATE\tPROGRESS\tOWNER\tUPDATED")
	for _, operation := range operations {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d%%\t%s\t%s\n",
			operation.ID, operation.Kind, operation.State, operation.Progress,
			operation.Owner, operation.UpdatedAt.Format(time.RFC3339))
	}
	_ = w.Flush()
}

func renderSecrets(secrets []secretpkg.Metadata) {
	if len(secrets) == 0 {
		fmt.Println("No secrets.")
		return
	}
	w := tabWriter()
	fmt.Fprintln(w, "SLOT\tSOURCE\tCONFIGURED\tREAD-ONLY\tVERSION")
	for _, meta := range secrets {
		fmt.Fprintf(w, "%s\t%s\t%t\t%t\t%d\n",
			meta.ID, meta.Source, meta.Configured, meta.ReadOnly, meta.Version)
	}
	_ = w.Flush()
}

func renderEvents(page controlpkg.EventPage) {
	if len(page.Data) == 0 {
		fmt.Printf("No events (next_after=%d).\n", page.NextAfter)
		return
	}
	w := tabWriter()
	fmt.Fprintln(w, "SEQUENCE\tPROVIDER\tTYPE\tGENERATION")
	for _, event := range page.Data {
		fmt.Fprintf(w, "%d\t%s\t%s\t%d\n", event.Sequence, event.ProviderID, event.Type, event.Generation)
	}
	fmt.Fprintf(w, "\t\t\tnext_after=%d\n", page.NextAfter)
	_ = w.Flush()
}
