package yarouter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTranslateAnthropicRequest_buildsNativeResponsesToolTurn(t *testing.T) {
	// Given
	body := []byte(`{
		"model":"claude-ya-codex-gpt-5-4",
		"system":[{"type":"text","text":"x"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"x"}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"weather","input":{"city":"hanoi"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"x"}]}
		],
		"max_tokens":64,
		"tools":[{"name":"weather","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"tool","name":"weather"},
		"output_config":{"effort":"high","format":{"type":"json_schema","schema":{"type":"object"}}},
		"thinking":{"type":"adaptive"},
		"stream":true
	}`)

	// When
	translated, err := translateAnthropicRequest(body, map[string]string{
		"claude-ya-codex-gpt-5-4": "codex/gpt-5.4",
	})

	// Then
	if err != nil {
		t.Fatalf("translateAnthropicRequest: %v", err)
	}
	if translated.Model != "codex/gpt-5.4" || !translated.Stream {
		t.Fatalf("translated route = %#v", translated)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(translated.Body, &got); err != nil {
		t.Fatalf("decode native Responses request: %v", err)
	}
	if got["instructions"] == nil || got["max_output_tokens"] == nil || got["reasoning"] == nil || got["text"] == nil {
		t.Fatalf("missing translated Responses fields: %s", translated.Body)
	}
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(got["input"], &input); err != nil {
		t.Fatalf("decode input: %v", err)
	}
	if len(input) != 3 {
		t.Fatalf("input items=%d, want 3", len(input))
	}
	var functionCall struct {
		Type   string `json:"type"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal(input[1]["type"], &functionCall.Type); err != nil {
		t.Fatalf("decode assistant input type: %v", err)
	}
	if functionCall.Type != "function_call" {
		t.Fatalf("assistant item type=%q", functionCall.Type)
	}
	if err := json.Unmarshal(input[1]["call_id"], &functionCall.CallID); err != nil {
		t.Fatalf("decode function call ID: %v", err)
	}
	if err := json.Unmarshal(input[1]["name"], &functionCall.Name); err != nil {
		t.Fatalf("decode function name: %v", err)
	}
	if functionCall.CallID != "toolu_1" || functionCall.Name != "weather" {
		t.Fatalf("function call=%+v", functionCall)
	}
}

func TestTranslateAnthropicRequest_rejectsUnknownRequiredField(t *testing.T) {
	// Given
	body := []byte(`{"model":"claude-ya-codex-gpt-5-4","messages":[],"context_management":{"edits":[]}}`)

	// When
	_, err := translateAnthropicRequest(body, map[string]string{
		"claude-ya-codex-gpt-5-4": "codex/gpt-5.4",
	})

	// Then
	if err == nil {
		t.Fatal("expected unsupported capability error")
	}
}

func TestTranslateAnthropicRequest_rejectsOversizedToolSchema(t *testing.T) {
	// Given
	schema := `{"type":"object","description":"` + strings.Repeat("x", 256*1024) + `"}`
	body := []byte(`{"model":"codex/gpt-5.4","messages":[{"role":"user","content":"x"}],"max_tokens":8,"tools":[{"name":"weather","input_schema":` + schema + `}]}`)

	// When
	_, err := translateAnthropicRequest(body, nil)

	// Then
	if err == nil {
		t.Fatal("expected oversized tool schema to be rejected")
	}
}

func TestTranslateAnthropicRequest_rejectsUnsupportedEffortForSelectedModel(t *testing.T) {
	// Given
	body := []byte(`{"model":"kilo/kilo-auto/free","messages":[{"role":"user","content":"x"}],"max_tokens":8,"output_config":{"effort":"xhigh"}}`)

	// When
	_, err := translateAnthropicRequest(body, nil)

	// Then
	if err == nil {
		t.Fatal("expected selected model effort capability error")
	}
}
