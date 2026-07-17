package yarouter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMVPThienduVerticalSliceSelectsAcrossThreeProviders(t *testing.T) {
	var selected []ProviderID
	registry := NewProviderRegistry()
	copilot := mvpMockProvider(ProviderCopilot, []Capability{CapabilityChat}, &selected)
	codex := mvpMockProvider(ProviderCodex, []Capability{CapabilityChat, CapabilityResponses}, &selected)
	kilo := mvpMockProvider(ProviderKilo, []Capability{CapabilityChat, CapabilityResponses}, &selected)
	registry.Register(copilot)
	registry.Register(codex)
	registry.Register(kilo)
	cfg := defaultConfig()
	cfg.Routing.VirtualModels = map[string]VirtualModelConfig{
		"thiendu": {Strategy: "priority", Targets: []string{"github/gpt-5-mini", "codex/gpt-5.4-mini", "kilo/kilo-auto/free"}},
	}
	router := NewModelRouter(registry, cfg.Routing)

	models := httptest.NewRecorder()
	modelsHandler(registry, cfg).ServeHTTP(models, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	var catalog ModelList
	if err := json.NewDecoder(models.Body).Decode(&catalog); err != nil {
		t.Fatal(err)
	}
	if !modelListContains(catalog, "thiendu") {
		t.Fatalf("thiendu absent from /v1/models: %+v", catalog.Data)
	}

	callThiendu(t, proxyHandler(registry, router, cfg), "/v1/chat/completions")
	copilot.health.Authenticated = false
	callThiendu(t, proxyHandler(registry, router, cfg), "/v1/chat/completions")
	codex.health.Authenticated = false
	callThiendu(t, proxyHandler(registry, router, cfg), "/v1/chat/completions")

	want := []ProviderID{ProviderCopilot, ProviderCodex, ProviderKilo}
	if strings.Join(providerIDs(selected), ",") != strings.Join(providerIDs(want), ",") {
		t.Fatalf("selected = %v, want %v", selected, want)
	}
}

func mvpMockProvider(id ProviderID, capabilities []Capability, selected *[]ProviderID) *mockProvider {
	model := "gpt-5-mini"
	if id == ProviderCodex {
		model = "gpt-5.4-mini"
	}
	if id == ProviderKilo {
		model = "kilo-auto/free"
	}
	return &mockProvider{
		id: id, name: string(id), caps: capabilities, health: ProviderHealth{Authenticated: true}, lastKnown: []string{model},
		proxyFunc: func(_ context.Context, writer http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			*selected = append(*selected, id)
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"model":"` + model + `","choices":[]}`))
			return nil
		},
	}
}

func callThiendu(t *testing.T, handler http.Handler, path string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"thiendu","messages":[]}`)))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"model":"thiendu"`) {
		t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
	}
}

func modelListContains(models ModelList, id string) bool {
	for _, model := range models.Data {
		if model.ID == id {
			return true
		}
	}
	return false
}

func providerIDs(ids []ProviderID) []string {
	values := make([]string, 0, len(ids))
	for _, id := range ids {
		values = append(values, string(id))
	}
	return values
}
