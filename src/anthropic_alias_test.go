package yarouter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsHandler_exposesDiscoverableClaudeAlias(t *testing.T) {
	// Given
	registry := NewProviderRegistry()
	registry.Register(&mockProvider{
		id:     ProviderCodex,
		name:   "Codex",
		caps:   []Capability{CapabilityResponses},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "gpt-5.4", Object: "model"}}},
	})
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = nil
	cfg.Routing.ClaudeAliases = map[string]string{"claude-ya-codex-gpt-5-4": "codex/gpt-5.4"}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/models?limit=1000", nil)

	// When
	modelsHandler(registry, cfg).ServeHTTP(response, request)

	// Then
	var models ModelList
	if err := json.NewDecoder(response.Body).Decode(&models); err != nil {
		t.Fatalf("decode models: %v", err)
	}
	seen := make(map[string]bool, len(models.Data))
	for _, model := range models.Data {
		seen[model.ID] = true
	}
	if !seen["codex/gpt-5.4"] || !seen["claude-ya-codex-gpt-5-4"] {
		t.Fatalf("model IDs=%v", seen)
	}
}
