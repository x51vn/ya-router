package yarouter

import (
	"context"
	"testing"
)

func TestModelRouterResolve_GithubProviderPrefixedModelRoutesToCopilot(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-4", Object: "model"}}},
	})
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4-mini", Object: "model"}}},
	})

	router := NewModelRouter(registry, defaultConfig().Routing)

	got, err := router.Resolve(context.Background(), "github/gpt-4", CapabilityChat)
	if err != nil {
		t.Fatalf("Resolve(%q, %q) error = %v, want nil", "github/gpt-4", CapabilityChat, err)
	}
	if got.Provider.ID() != ProviderCopilot {
		t.Fatalf("Resolve(%q, %q) provider = %q, want %q", "github/gpt-4", CapabilityChat, got.Provider.ID(), ProviderCopilot)
	}
	if got.ResolvedModel != "gpt-4" {
		t.Fatalf("Resolve(%q, %q) resolved model = %q, want gpt-4", "github/gpt-4", CapabilityChat, got.ResolvedModel)
	}
}

func TestModelRouterResolve_CodexProviderPrefixedModelRoutesToCodex(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-4", Object: "model"}}},
	})
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4-mini", Object: "model"}}},
	})

	router := NewModelRouter(registry, defaultConfig().Routing)

	got, err := router.Resolve(context.Background(), "codex/gpt-5.4-mini", CapabilityChat)
	if err != nil {
		t.Fatalf("Resolve(%q, %q) error = %v, want nil", "codex/gpt-5.4-mini", CapabilityChat, err)
	}
	if got.Provider.ID() != ProviderCodex {
		t.Fatalf("Resolve(%q, %q) provider = %q, want %q", "codex/gpt-5.4-mini", CapabilityChat, got.Provider.ID(), ProviderCodex)
	}
	if got.ResolvedModel != "gpt-5.4-mini" {
		t.Fatalf("Resolve(%q, %q) resolved model = %q, want gpt-5.4-mini", "codex/gpt-5.4-mini", CapabilityChat, got.ResolvedModel)
	}
}

func TestModelRouterResolve_GivenExactCodexProviderPrefixedModelMapEntry_WhenResolving_ThenUsesBareUpstreamModel(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4-mini", Object: "model"}}},
	})

	cfg := defaultConfig()
	cfg.Routing.ModelMap = map[string]ModelMapEntry{
		"codex/gpt-5.4-mini": {Provider: string(ProviderCodex)},
	}
	router := NewModelRouter(registry, cfg.Routing)

	got, err := router.Resolve(context.Background(), "codex/gpt-5.4-mini", CapabilityChat)
	if err != nil {
		t.Fatalf("Resolve(%q, %q) error = %v, want nil", "codex/gpt-5.4-mini", CapabilityChat, err)
	}
	if got.Provider.ID() != ProviderCodex {
		t.Fatalf("Resolve(%q, %q) provider = %q, want %q", "codex/gpt-5.4-mini", CapabilityChat, got.Provider.ID(), ProviderCodex)
	}
	if got.ResolvedModel != "gpt-5.4-mini" {
		t.Fatalf("Resolve(%q, %q) resolved model = %q, want gpt-5.4-mini", "codex/gpt-5.4-mini", CapabilityChat, got.ResolvedModel)
	}
}

func TestModelRouterResolve_GivenOmittedModel_WhenDefaultModelIsUnavailable_ThenUsesDefaultProvider(t *testing.T) {
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCopilot,
		name:   "Copilot",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-4", Object: "model"}}},
	})
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: nil},
	})

	cfg := defaultConfig()
	cfg.Routing.DefaultModel = "default-model"
	cfg.Routing.DefaultProvider = string(ProviderCodex)
	router := NewModelRouter(registry, cfg.Routing)

	got, err := router.Resolve(context.Background(), "", CapabilityChat)
	if err != nil {
		t.Fatalf("Resolve omitted model error = %v, want nil", err)
	}
	if got.Provider.ID() != ProviderCodex {
		t.Fatalf("Resolve omitted model provider = %q, want %q", got.Provider.ID(), ProviderCodex)
	}
	if got.ResolvedModel != "default-model" {
		t.Fatalf("Resolve omitted model resolved model = %q, want default-model", got.ResolvedModel)
	}
}
