package control

import (
	"fmt"
	"sort"
	"strings"

	configschema "github.com/duvu/ya-router/internal/config"
)

// MutationKind enumerates the supported revision-safe configuration mutations.
type MutationKind string

const (
	MutationProviderEnabled MutationKind = "provider_enabled"
	MutationDefaultModel    MutationKind = "default_model"
	MutationDefaultProvider MutationKind = "default_provider"
	MutationAllowedModels   MutationKind = "allowed_models"
	MutationModelMapSet     MutationKind = "model_map_set"
	MutationModelMapDelete  MutationKind = "model_map_delete"
)

// MutationRequest is one revision-safe change. ExpectedRevision enables
// compare-and-swap so two writers cannot silently overwrite each other.
type MutationRequest struct {
	Kind             MutationKind `json:"kind"`
	ExpectedRevision uint64       `json:"expected_revision"`
	DryRun           bool         `json:"dry_run,omitempty"`

	// Provider is the target provider ID for provider-scoped mutations.
	Provider string `json:"provider,omitempty"`
	// Enabled is the desired provider state for MutationProviderEnabled.
	Enabled *bool `json:"enabled,omitempty"`
	// Model is the client-facing model ID for model-map mutations, or the
	// default model value for MutationDefaultModel.
	Model string `json:"model,omitempty"`
	// UpstreamModel is the optional upstream alias for MutationModelMapSet.
	UpstreamModel string `json:"upstream_model,omitempty"`
	// AllowedModels is the replacement allowlist for MutationAllowedModels.
	AllowedModels []string `json:"allowed_models,omitempty"`
	// Value is the scalar value for default-model/default-provider mutations.
	Value string `json:"value,omitempty"`
}

// knownProviderIDs lists the provider IDs a mutation may target. Kept local to
// avoid a control→provider import for a small fixed set.
var knownProviderIDs = map[string]struct{}{
	"copilot": {},
	"codex":   {},
	"kilo":    {},
}

// ApplyMutation returns a new configuration with the mutation applied. It never
// mutates the input. It enforces the same invariants config validation does and
// additionally rejects unknown providers and empty required fields, so a
// rejected mutation makes no change. The caller is responsible for compare-and-
// swap persistence and reconcile.
func ApplyMutation(current *configschema.Config, request MutationRequest) (*configschema.Config, error) {
	if current == nil {
		return nil, fmt.Errorf("current configuration is required")
	}
	next := configschema.Clone(current)

	switch request.Kind {
	case MutationProviderEnabled:
		if request.Enabled == nil {
			return nil, fmt.Errorf("provider_enabled requires an enabled value")
		}
		if err := setProviderEnabled(next, request.Provider, *request.Enabled); err != nil {
			return nil, err
		}
	case MutationDefaultModel:
		value := strings.TrimSpace(firstNonEmptyMutation(request.Value, request.Model))
		if value == "" {
			return nil, fmt.Errorf("default_model requires a non-empty value")
		}
		next.Routing.DefaultModel = value
	case MutationDefaultProvider:
		value := strings.TrimSpace(request.Value)
		if _, ok := knownProviderIDs[value]; !ok {
			return nil, fmt.Errorf("default_provider %q is not a known provider", value)
		}
		next.Routing.DefaultProvider = value
	case MutationAllowedModels:
		if err := setAllowedModels(next, request.Provider, request.AllowedModels); err != nil {
			return nil, err
		}
	case MutationModelMapSet:
		if err := setModelMapEntry(next, request); err != nil {
			return nil, err
		}
	case MutationModelMapDelete:
		key := strings.TrimSpace(request.Model)
		if key == "" {
			return nil, fmt.Errorf("model_map_delete requires a model")
		}
		if next.Routing.ModelMap != nil {
			delete(next.Routing.ModelMap, key)
		}
	default:
		return nil, fmt.Errorf("unsupported mutation kind %q", request.Kind)
	}
	return next, nil
}

func setProviderEnabled(config *configschema.Config, provider string, enabled bool) error {
	switch strings.TrimSpace(provider) {
	case "copilot":
		config.Providers.Copilot.Enabled = enabled
	case "codex":
		config.Providers.Codex.Enabled = enabled
	case "kilo":
		config.Providers.Kilo.Enabled = enabled
	default:
		return fmt.Errorf("unknown provider %q", provider)
	}
	return nil
}

func setAllowedModels(config *configschema.Config, provider string, models []string) error {
	cleaned := cleanModelList(models)
	switch strings.TrimSpace(provider) {
	case "copilot":
		config.Providers.Copilot.AllowedModels = cleaned
	case "codex":
		config.Providers.Codex.AllowedModels = cleaned
	case "kilo":
		config.Providers.Kilo.AllowedModels = cleaned
	default:
		return fmt.Errorf("unknown provider %q", provider)
	}
	return nil
}

func setModelMapEntry(config *configschema.Config, request MutationRequest) error {
	key := strings.TrimSpace(request.Model)
	if key == "" {
		return fmt.Errorf("model_map_set requires a model")
	}
	provider := strings.TrimSpace(request.Provider)
	if _, ok := knownProviderIDs[provider]; !ok {
		return fmt.Errorf("model_map_set provider %q is not a known provider", provider)
	}
	// A model-map key must not collide with a configured umbrella model, which
	// preserves the routing precedence invariant.
	if _, exists := config.Routing.VirtualModels[key]; exists {
		return fmt.Errorf("model %q is a configured virtual model and cannot also be a model_map entry", key)
	}
	if config.Routing.ModelMap == nil {
		config.Routing.ModelMap = map[string]configschema.ModelMapEntry{}
	}
	config.Routing.ModelMap[key] = configschema.ModelMapEntry{
		Provider:      provider,
		UpstreamModel: strings.TrimSpace(request.UpstreamModel),
	}
	return nil
}

func cleanModelList(models []string) []string {
	if len(models) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(models))
	cleaned := make([]string, 0, len(models))
	for _, model := range models {
		trimmed := strings.TrimSpace(model)
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		cleaned = append(cleaned, trimmed)
	}
	sort.Strings(cleaned)
	return cleaned
}

func firstNonEmptyMutation(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
