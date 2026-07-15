package yarouter

import (
	"encoding/json"
	"testing"
)

func TestNormalizeEmbeddingsRequestBody_StringInputBecomesArray(t *testing.T) {
	input := []byte(`{"model":"text-embedding-ada-002","input":"hello"}`)
	normalized, model := normalizeEmbeddingsRequestBody(input)

	if model != "text-embedding-ada-002" {
		t.Fatalf("model=%q, want %q", model, "text-embedding-ada-002")
	}

	var payload map[string]any
	if err := json.Unmarshal(normalized, &payload); err != nil {
		t.Fatalf("failed to unmarshal normalized payload: %v", err)
	}

	arr, ok := payload["input"].([]any)
	if !ok {
		t.Fatalf("input type=%T, want []any", payload["input"])
	}
	if len(arr) != 1 || arr[0] != "hello" {
		t.Fatalf("input=%v, want [\"hello\"]", arr)
	}
}

func TestEnsureEmbeddingsResponseCompat_AddsObjectAndModelWhenMissing(t *testing.T) {
	original := []byte(`{"data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`)
	out := ensureEmbeddingsResponseCompat(original, "text-embedding-ada-002")

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("failed to unmarshal transformed response: %v", err)
	}

	if payload["object"] != "list" {
		t.Fatalf("object=%v, want %q", payload["object"], "list")
	}
	if payload["model"] != "text-embedding-ada-002" {
		t.Fatalf("model=%v, want %q", payload["model"], "text-embedding-ada-002")
	}
}
