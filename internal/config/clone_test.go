package config

import "testing"

func TestCloneDoesNotShareMutableConfiguration(t *testing.T) {
	original := &Config{
		Routing: Routing{
			ModelMap: map[string]ModelMapEntry{
				"client-model": {Provider: "copilot", UpstreamModel: "upstream-model"},
			},
			VirtualModels: map[string]VirtualModel{
				"router/auto": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini"}},
			},
		},
		Providers: Providers{
			Copilot: CopilotProvider{
				AllowedModels: []string{"gpt"},
				Accounts:      []CopilotAccount{{Label: "primary"}},
			},
			Codex: CodexProvider{
				AllowedModels: []string{"o3"},
				Accounts:      []CodexAccount{{Label: "primary"}},
			},
			Kilo: KiloProvider{AllowedModels: []string{"free"}},
		},
	}

	cloned := Clone(original)
	original.Routing.ModelMap["client-model"] = ModelMapEntry{Provider: "kilo"}
	original.Routing.VirtualModels["router/auto"] = VirtualModel{Strategy: "changed"}
	original.Providers.Copilot.AllowedModels[0] = "changed"
	original.Providers.Copilot.Accounts[0].Label = "changed"
	original.Providers.Codex.AllowedModels[0] = "changed"
	original.Providers.Codex.Accounts[0].Label = "changed"
	original.Providers.Kilo.AllowedModels[0] = "changed"

	if cloned.Routing.ModelMap["client-model"].Provider != "copilot" {
		t.Fatal("routing map was shared")
	}
	if cloned.Routing.VirtualModels["router/auto"].Strategy != "priority" {
		t.Fatal("virtual model map was shared")
	}
	if got := cloned.Routing.VirtualModels["router/auto"].Targets; len(got) != 2 || got[0] != "github/gpt-5-mini" {
		t.Fatalf("virtual model targets slice was shared: %v", got)
	}
	if cloned.Providers.Copilot.AllowedModels[0] != "gpt" || cloned.Providers.Copilot.Accounts[0].Label != "primary" {
		t.Fatal("copilot configuration was shared")
	}
	if cloned.Providers.Codex.AllowedModels[0] != "o3" || cloned.Providers.Codex.Accounts[0].Label != "primary" {
		t.Fatal("codex configuration was shared")
	}
	if cloned.Providers.Kilo.AllowedModels[0] != "free" {
		t.Fatal("kilo configuration was shared")
	}
}
