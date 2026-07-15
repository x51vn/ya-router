package proxy

import (
	"testing"

	"github.com/duvu/ya-router/internal/provider"
)

func TestRequestBoundary(t *testing.T) {
	capability, err := CapabilityFromPath("/v1/responses")
	if err != nil || capability != provider.CapabilityResponses {
		t.Fatalf("capability=%q err=%v", capability, err)
	}
	body := []byte(`{"model":"codex/gpt-test","input":"hello"}`)
	if got := ExtractModel(body); got != "codex/gpt-test" {
		t.Fatalf("model=%q", got)
	}
	if got := ExtractModel(PatchModel(body, "gpt-test")); got != "gpt-test" {
		t.Fatalf("patched model=%q", got)
	}
}
