package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	configschema "github.com/duvu/ya-router/internal/config"
)

func baseConfig() *configschema.Config {
	return &configschema.Config{
		Routing: configschema.Routing{
			DefaultModel:    "gpt-5-mini",
			DefaultProvider: "copilot",
			ModelMap:        map[string]configschema.ModelMapEntry{},
			VirtualModels: map[string]configschema.VirtualModel{
				"router/auto": {Strategy: "priority", Targets: []string{"github/gpt-5-mini"}},
			},
		},
		Providers: configschema.Providers{
			Copilot: configschema.CopilotProvider{Enabled: true},
			Codex:   configschema.CodexProvider{Enabled: false},
		},
	}
}

func boolPtr(b bool) *bool { return &b }

func TestApplyMutationDoesNotMutateInput(t *testing.T) {
	current := baseConfig()
	_, err := ApplyMutation(current, MutationRequest{Kind: MutationProviderEnabled, Provider: "codex", Enabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if current.Providers.Codex.Enabled {
		t.Fatal("input configuration was mutated")
	}
}

func TestApplyMutationProviderEnable(t *testing.T) {
	next, err := ApplyMutation(baseConfig(), MutationRequest{Kind: MutationProviderEnabled, Provider: "codex", Enabled: boolPtr(true)})
	if err != nil {
		t.Fatal(err)
	}
	if !next.Providers.Codex.Enabled {
		t.Fatal("codex not enabled")
	}
}

func TestApplyMutationUnknownProviderRejected(t *testing.T) {
	_, err := ApplyMutation(baseConfig(), MutationRequest{Kind: MutationProviderEnabled, Provider: "bogus", Enabled: boolPtr(true)})
	if err == nil {
		t.Fatal("expected unknown provider rejection")
	}
}

func TestApplyMutationModelMapSetAndDelete(t *testing.T) {
	next, err := ApplyMutation(baseConfig(), MutationRequest{Kind: MutationModelMapSet, Model: "research", Provider: "codex", UpstreamModel: "gpt-5.4"})
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := next.Routing.ModelMap["research"]
	if !ok || entry.Provider != "codex" || entry.UpstreamModel != "gpt-5.4" {
		t.Fatalf("model map entry = %+v ok=%v", entry, ok)
	}
	after, err := ApplyMutation(next, MutationRequest{Kind: MutationModelMapDelete, Model: "research"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Routing.ModelMap["research"]; ok {
		t.Fatal("entry not deleted")
	}
}

// TestModelMapCannotShadowVirtualModel enforces the routing precedence
// invariant: a model_map key must not collide with a configured umbrella model.
func TestModelMapCannotShadowVirtualModel(t *testing.T) {
	_, err := ApplyMutation(baseConfig(), MutationRequest{Kind: MutationModelMapSet, Model: "router/auto", Provider: "codex"})
	if err == nil {
		t.Fatal("expected rejection of model_map key colliding with virtual model")
	}
}

func TestApplyMutationDefaultProviderValidation(t *testing.T) {
	if _, err := ApplyMutation(baseConfig(), MutationRequest{Kind: MutationDefaultProvider, Value: "bogus"}); err == nil {
		t.Fatal("expected unknown default provider rejection")
	}
	next, err := ApplyMutation(baseConfig(), MutationRequest{Kind: MutationDefaultProvider, Value: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Routing.DefaultProvider != "codex" {
		t.Fatalf("default provider = %q", next.Routing.DefaultProvider)
	}
}

func TestApplyMutationAllowedModelsDedupAndSort(t *testing.T) {
	next, err := ApplyMutation(baseConfig(), MutationRequest{
		Kind: MutationAllowedModels, Provider: "copilot",
		AllowedModels: []string{"b", "a", "b", " ", "a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := next.Providers.Copilot.AllowedModels
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("allowed models = %v, want [a b]", got)
	}
}

func TestApplyMutationUnsupportedKind(t *testing.T) {
	if _, err := ApplyMutation(baseConfig(), MutationRequest{Kind: "bogus"}); err == nil {
		t.Fatal("expected unsupported kind rejection")
	}
}

func TestApplyMutationDefaultModelRequiresValue(t *testing.T) {
	if _, err := ApplyMutation(baseConfig(), MutationRequest{Kind: MutationDefaultModel}); err == nil {
		t.Fatal("expected empty default model rejection")
	}
}

type recoveredMutationResult struct{}

func (recoveredMutationResult) ErrorDetails() map[string]any {
	return map[string]any{"current_revision": uint64(3), "runtime_published": false}
}

type failedMutationExecutor struct{}

func (failedMutationExecutor) Execute(MutationRequest, string) (any, int, string, error) {
	return recoveredMutationResult{}, http.StatusBadRequest, "runtime_reconcile_failed", errors.New("provider reload failed")
}

func TestMutationFailureIncludesRecoveredState(t *testing.T) {
	api := newTestAPI(nil, "")
	RegisterMutationRoutes(api, failedMutationExecutor{})
	request := httptest.NewRequest(http.MethodPost, "/control/v1/config/mutations", strings.NewReader(`{"kind":"provider_enabled","expected_revision":1}`))
	request.Header.Set(ClientVersionHeader, CurrentClientVersion)
	request.Header.Set(IdempotencyKeyHeader, "recovered-mutation")
	response := httptest.NewRecorder()
	api.Handler(localAdmin()).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var envelope ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Details["current_revision"] != float64(3) || envelope.Error.Details["runtime_published"] != false {
		t.Fatalf("recovery details = %+v", envelope.Error.Details)
	}
}
