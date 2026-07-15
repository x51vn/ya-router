package config

import "testing"

func TestCloneDoesNotShareMutableConfiguration(t *testing.T) {
	original := &Config{
		Routing: Routing{ModelMap: map[string]ModelMapEntry{
			"client-model": {Provider: "copilot", UpstreamModel: "upstream-model"},
		}},
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
	original.Providers.Copilot.AllowedModels[0] = "changed"
	original.Providers.Copilot.Accounts[0].Label = "changed"
	original.Providers.Codex.AllowedModels[0] = "changed"
	original.Providers.Codex.Accounts[0].Label = "changed"
	original.Providers.Kilo.AllowedModels[0] = "changed"

	if cloned.Routing.ModelMap["client-model"].Provider != "copilot" {
		t.Fatal("routing map was shared")
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
