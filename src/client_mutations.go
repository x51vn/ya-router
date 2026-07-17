// client_mutations.go adds scriptable revision-safe mutation commands to the
// `ya` client (YA-TUI-08 client surface). Every mutation requires an explicit
// --revision for compare-and-swap and supports --dry-run to preview the diff
// without committing. Conflicts and validation failures map to stable exit
// codes so automation can branch safely.
package yarouter

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	clientpkg "github.com/duvu/ya-router/internal/client"
	controlpkg "github.com/duvu/ya-router/internal/control"
)

// runClientMutationCommand handles the mutating client verbs.
func runClientMutationCommand(command string, args []string) int {
	set := flag.NewFlagSet(command, flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	var (
		common   clientCommonFlags
		revision uint64
		dryRun   bool
		provider string
		model    string
		upstream string
		value    string
		models   string
	)
	set.BoolVar(&common.jsonOut, "json", false, "Emit machine-readable JSON output")
	set.StringVar(&common.socket, "socket", "", "Control Unix socket path (overrides env)")
	set.StringVar(&common.address, "address", "", "Control HTTPS address host:port (overrides env)")
	set.IntVar(&common.timeout, "timeout", 0, "Per-request timeout in seconds")
	var rev uint64Flag
	rev.target = &revision
	set.Var(&rev, "revision", "Expected config revision for compare-and-swap (required)")
	set.BoolVar(&dryRun, "dry-run", false, "Validate and preview the change without committing")
	set.StringVar(&provider, "provider", "", "Target provider ID")
	set.StringVar(&model, "model", "", "Model ID (for default-model or model-map)")
	set.StringVar(&upstream, "upstream-model", "", "Upstream model alias (model-map-set)")
	set.StringVar(&value, "value", "", "Scalar value (default-provider)")
	set.StringVar(&models, "models", "", "Comma-separated allowed model list (allowed-models)")
	if err := set.Parse(args); err != nil {
		return clientExitUsage
	}

	revisionProvided := false
	set.Visit(func(f *flag.Flag) {
		if f.Name == "revision" {
			revisionProvided = true
		}
	})
	if !revisionProvided {
		fmt.Fprintln(os.Stderr, "ya: --revision is required for a mutation (use `ya config` to read the current revision)")
		return clientExitUsage
	}

	request, err := buildMutationRequest(command, revision, dryRun, provider, model, upstream, value, models)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ya: %v\n", err)
		return clientExitUsage
	}

	profile, err := resolveClientProfile(common)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ya: %v\n", err)
		return clientExitUsage
	}
	cl, err := clientpkg.New(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ya: %v\n", err)
		return clientExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), clientRequestTimeout(profile))
	defer cancel()

	result, err := cl.ApplyMutation(ctx, request)
	if err != nil {
		return reportClientError(err)
	}
	if common.jsonOut {
		return emitJSON(result)
	}
	renderMutationResult(result)
	return clientExitOK
}

func buildMutationRequest(command string, revision uint64, dryRun bool, provider, model, upstream, value, models string) (controlpkg.MutationRequest, error) {
	request := controlpkg.MutationRequest{ExpectedRevision: revision, DryRun: dryRun}
	switch command {
	case "provider-enable", "provider-disable":
		if strings.TrimSpace(provider) == "" {
			return request, fmt.Errorf("%s requires --provider", command)
		}
		enabled := command == "provider-enable"
		request.Kind = controlpkg.MutationProviderEnabled
		request.Provider = provider
		request.Enabled = &enabled
	case "default-model":
		if strings.TrimSpace(model) == "" {
			return request, fmt.Errorf("default-model requires --model")
		}
		request.Kind = controlpkg.MutationDefaultModel
		request.Model = model
	case "default-provider":
		if strings.TrimSpace(value) == "" {
			return request, fmt.Errorf("default-provider requires --value")
		}
		request.Kind = controlpkg.MutationDefaultProvider
		request.Value = value
	case "allowed-models":
		if strings.TrimSpace(provider) == "" {
			return request, fmt.Errorf("allowed-models requires --provider")
		}
		request.Kind = controlpkg.MutationAllowedModels
		request.Provider = provider
		request.AllowedModels = splitCommaList(models)
	case "model-map-set":
		if strings.TrimSpace(model) == "" || strings.TrimSpace(provider) == "" {
			return request, fmt.Errorf("model-map-set requires --model and --provider")
		}
		request.Kind = controlpkg.MutationModelMapSet
		request.Model = model
		request.Provider = provider
		request.UpstreamModel = upstream
	case "model-map-delete":
		if strings.TrimSpace(model) == "" {
			return request, fmt.Errorf("model-map-delete requires --model")
		}
		request.Kind = controlpkg.MutationModelMapDelete
		request.Model = model
	default:
		return request, fmt.Errorf("unknown mutation command %q", command)
	}
	return request, nil
}

func splitCommaList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func renderMutationResult(result clientpkg.MutationResult) {
	w := tabWriter()
	action := "applied"
	if result.DryRun {
		action = "dry-run (not committed)"
	} else if !result.Applied {
		action = "no change"
	}
	fmt.Fprintf(w, "Outcome:\t%s\n", action)
	fmt.Fprintf(w, "Runtime published:\t%t\n", result.RuntimePublished)
	fmt.Fprintf(w, "Current revision:\t%d\n", result.CurrentRevision)
	fmt.Fprintf(w, "Next revision:\t%d\n", result.NextRevision)
	fmt.Fprintf(w, "Changed:\t%t\n", result.Changed)
	if len(result.ChangedPaths) > 0 {
		fmt.Fprintf(w, "Changed paths:\t%s\n", strings.Join(result.ChangedPaths, ", "))
	}
	if len(result.RestartRequired) > 0 {
		fmt.Fprintf(w, "Restart required:\t%s\n", strings.Join(result.RestartRequired, ", "))
	}
	_ = w.Flush()
}
