package config

import (
	"encoding/json"
	"testing"
)

func TestConfigSchemaRoundTrip(t *testing.T) {
	want := Config{
		Port:          7071,
		ConfigVersion: 1,
		Routing: Routing{
			DefaultProvider: "copilot",
			ModelMap: map[string]ModelMapEntry{
				"gpt-test": {Provider: "codex", UpstreamModel: "gpt-upstream"},
			},
		},
	}
	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	if got.Port != want.Port || got.Routing.ModelMap["gpt-test"].UpstreamModel != "gpt-upstream" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}
