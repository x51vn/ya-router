package yarouter

import (
	"encoding/json"
	"testing"
)

func TestResponsesToAnthropicMessage_preservesTextAndToolUse(t *testing.T) {
	// Given
	body := []byte(`{
		"id":"resp_1",
		"model":"codex/gpt-5.4",
		"output":[
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"x"}]},
			{"type":"function_call","call_id":"toolu_1","name":"weather","arguments":"{\"city\":\"hanoi\"}"}
		],
		"usage":{"input_tokens":3,"output_tokens":5}
	}`)

	// When
	converted, err := responsesToAnthropicMessage(body, "claude-ya-codex-gpt-5-4")

	// Then
	if err != nil {
		t.Fatalf("responsesToAnthropicMessage: %v", err)
	}
	var message struct {
		ID         string `json:"id"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(converted, &message); err != nil {
		t.Fatalf("decode Anthropic response: %v", err)
	}
	if message.ID != "resp_1" || message.Model != "claude-ya-codex-gpt-5-4" || message.StopReason != "tool_use" {
		t.Fatalf("message=%+v", message)
	}
	if len(message.Content) != 2 || message.Content[1].Type != "tool_use" || message.Content[1].ID != "toolu_1" || message.Content[1].Name != "weather" {
		t.Fatalf("content=%+v", message.Content)
	}
	if message.Usage.InputTokens != 3 || message.Usage.OutputTokens != 5 {
		t.Fatalf("usage=%+v", message.Usage)
	}
}
