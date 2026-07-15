package yarouter

import (
	"testing"
)

func TestExtractModelFromBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"normal", `{"model":"gpt-4","messages":[]}`, "gpt-4"},
		{"empty model", `{"model":"","messages":[]}`, ""},
		{"no model field", `{"messages":[]}`, ""},
		{"invalid json", `not json`, ""},
		{"empty body", ``, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractModelFromBody([]byte(tt.body))
			if got != tt.want {
				t.Errorf("extractModelFromBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPatchBodyModel(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		model     string
		wantModel string
	}{
		{
			name:      "replaces model",
			body:      `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`,
			model:     "gpt-5-mini",
			wantModel: "gpt-5-mini",
		},
		{
			name:      "adds model when missing",
			body:      `{"messages":[]}`,
			model:     "gpt-4",
			wantModel: "gpt-4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patched := patchBodyModel([]byte(tt.body), tt.model)
			got := extractModelFromBody(patched)
			if got != tt.wantModel {
				t.Errorf("after patchBodyModel, model = %q, want %q", got, tt.wantModel)
			}
		})
	}
}

func TestPatchBodyModel_InvalidJSON(t *testing.T) {
	body := []byte("not json")
	patched := patchBodyModel(body, "gpt-4")
	if string(patched) != "not json" {
		t.Errorf("expected original body returned on invalid JSON")
	}
}

func TestIsModelAllowed(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		allowed []string
		want    bool
	}{
		{"empty list allows all", "gpt-4", nil, true},
		{"empty list allows all 2", "anything", []string{}, true},
		{"exact match", "gpt-4", []string{"gpt-4", "gpt-5-mini"}, true},
		{"case insensitive", "GPT-4", []string{"gpt-4"}, true},
		{"not allowed", "claude-3.5", []string{"gpt-4", "gpt-5-mini"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isModelAllowed(tt.model, tt.allowed)
			if got != tt.want {
				t.Errorf("isModelAllowed(%q, %v) = %v, want %v", tt.model, tt.allowed, got, tt.want)
			}
		})
	}
}
