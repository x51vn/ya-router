// responses_adapter_test.go — Tests for Chat Completions ↔ Responses API adapter.
package main

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// normalizeContentParts
// ---------------------------------------------------------------------------

func TestNormalizeContentParts_PlainString(t *testing.T) {
	in := json.RawMessage(`"Hello world"`)
	out := normalizeContentParts(in)
	if string(out) != `"Hello world"` {
		t.Errorf("plain string should pass through unchanged, got %s", out)
	}
}

func TestNormalizeContentParts_TextToInputText(t *testing.T) {
	in := json.RawMessage(`[{"type":"text","text":"Hello"}]`)
	out := normalizeContentParts(in)

	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(out, &parts); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("expected 1 part, got %d", len(parts))
	}
	var typ string
	json.Unmarshal(parts[0]["type"], &typ)
	if typ != "input_text" {
		t.Errorf("type = %q, want input_text", typ)
	}
	var text string
	json.Unmarshal(parts[0]["text"], &text)
	if text != "Hello" {
		t.Errorf("text = %q, want Hello", text)
	}
}

func TestNormalizeContentParts_ImageUrlToInputImage(t *testing.T) {
	in := json.RawMessage(`[{"type":"image_url","image_url":{"url":"https://example.com/img.png"}}]`)
	out := normalizeContentParts(in)

	var parts []map[string]json.RawMessage
	json.Unmarshal(out, &parts)

	var typ string
	json.Unmarshal(parts[0]["type"], &typ)
	if typ != "input_image" {
		t.Errorf("type = %q, want input_image", typ)
	}
	// image_url object should still be preserved
	if _, ok := parts[0]["image_url"]; !ok {
		t.Error("image_url key should be preserved in output")
	}
}

func TestNormalizeContentParts_MixedParts(t *testing.T) {
	in := json.RawMessage(`[{"type":"text","text":"Say"},{"type":"image_url","image_url":{"url":"u"}}]`)
	out := normalizeContentParts(in)

	var parts []map[string]json.RawMessage
	json.Unmarshal(out, &parts)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}
	var t0, t1 string
	json.Unmarshal(parts[0]["type"], &t0)
	json.Unmarshal(parts[1]["type"], &t1)
	if t0 != "input_text" {
		t.Errorf("parts[0].type = %q, want input_text", t0)
	}
	if t1 != "input_image" {
		t.Errorf("parts[1].type = %q, want input_image", t1)
	}
}

func TestNormalizeContentParts_UnknownTypePassesThrough(t *testing.T) {
	// Unknown types must pass through unchanged (forward-compatibility).
	in := json.RawMessage(`[{"type":"computer_screenshot","data":"base64stuff"}]`)
	out := normalizeContentParts(in)

	var parts []map[string]json.RawMessage
	json.Unmarshal(out, &parts)
	var typ string
	json.Unmarshal(parts[0]["type"], &typ)
	if typ != "computer_screenshot" {
		t.Errorf("unknown type should pass through, got %q", typ)
	}
}

// ---------------------------------------------------------------------------
// extractMessages — array content
// ---------------------------------------------------------------------------

func TestExtractMessages_ArrayContentNormalized(t *testing.T) {
	msgs := json.RawMessage(`[
		{"role":"user","content":[{"type":"text","text":"Say hi"}]}
	]`)
	_, inputJSON, err := extractMessages(msgs)
	if err != nil {
		t.Fatalf("extractMessages: %v", err)
	}

	var input []map[string]json.RawMessage
	json.Unmarshal(inputJSON, &input)
	if len(input) != 1 {
		t.Fatalf("input len = %d, want 1", len(input))
	}

	var content []map[string]json.RawMessage
	json.Unmarshal(input[0]["content"], &content)
	if len(content) != 1 {
		t.Fatalf("content parts len = %d, want 1", len(content))
	}
	var typ string
	json.Unmarshal(content[0]["type"], &typ)
	if typ != "input_text" {
		t.Errorf("content[0].type = %q, want input_text (Chat Completions 'text' must be converted)", typ)
	}
}

func TestExtractMessages_SystemArrayContentExtracted(t *testing.T) {
	// System messages with array content: text must be extracted into instructions.
	msgs := json.RawMessage(`[
		{"role":"system","content":[{"type":"text","text":"Be concise."}]},
		{"role":"user","content":"Hello"}
	]`)
	instructions, inputJSON, err := extractMessages(msgs)
	if err != nil {
		t.Fatalf("extractMessages: %v", err)
	}
	if instructions != "Be concise." {
		t.Errorf("instructions = %q, want 'Be concise.'", instructions)
	}
	var input []map[string]json.RawMessage
	json.Unmarshal(inputJSON, &input)
	if len(input) != 1 {
		t.Fatalf("input len = %d, want 1 (only user message)", len(input))
	}
}

// ---------------------------------------------------------------------------
// buildChatGPTCodexRequest — array content
// ---------------------------------------------------------------------------

func TestBuildChatGPTCodexRequest_ArrayContentNormalized(t *testing.T) {
	// Simulates the failing client that sends content as array parts.
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "Hello"}]}
		],
		"stream": true
	}`

	out, _, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	var inputMsgs []map[string]json.RawMessage
	json.Unmarshal(m["input"], &inputMsgs)
	if len(inputMsgs) != 1 {
		t.Fatalf("input msgs = %d, want 1", len(inputMsgs))
	}
	var parts []map[string]json.RawMessage
	json.Unmarshal(inputMsgs[0]["content"], &parts)
	if len(parts) != 1 {
		t.Fatalf("content parts = %d, want 1", len(parts))
	}
	var typ string
	json.Unmarshal(parts[0]["type"], &typ)
	if typ != "input_text" {
		t.Errorf("content part type = %q, want input_text — 'text' must be rewritten to avoid OpenAIException upstream", typ)
	}
}

func TestBuildChatGPTCodexRequest_ImageUrlNormalized(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "Describe this"},
				{"type": "image_url", "image_url": {"url": "https://example.com/img.png"}}
			]}
		]
	}`

	out, _, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)
	var msgs []map[string]json.RawMessage
	json.Unmarshal(m["input"], &msgs)
	var parts []map[string]json.RawMessage
	json.Unmarshal(msgs[0]["content"], &parts)

	var t0, t1 string
	json.Unmarshal(parts[0]["type"], &t0)
	json.Unmarshal(parts[1]["type"], &t1)
	if t0 != "input_text" {
		t.Errorf("parts[0].type = %q, want input_text", t0)
	}
	if t1 != "input_image" {
		t.Errorf("parts[1].type = %q, want input_image", t1)
	}
}

// ---------------------------------------------------------------------------
// buildPlatformResponsesRequest
// ---------------------------------------------------------------------------

func TestBuildPlatformResponsesRequest_Basic(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hello"}
		],
		"max_tokens": 100,
		"temperature": 0.7,
		"stream": false
	}`

	out, _, err := buildPlatformResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildPlatformResponsesRequest: %v", err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}

	if _, ok := m["messages"]; ok {
		t.Error("output contains 'messages' — should be split into input/instructions")
	}
	if _, ok := m["input"]; !ok {
		t.Error("output missing 'input'")
	}
	if _, ok := m["instructions"]; !ok {
		t.Error("output missing 'instructions'")
	}
	if _, ok := m["max_tokens"]; ok {
		t.Error("output contains 'max_tokens' — should be renamed to 'max_output_tokens'")
	}
	if _, ok := m["max_output_tokens"]; !ok {
		t.Error("output missing 'max_output_tokens'")
	}
	for _, key := range []string{"model", "temperature", "stream"} {
		if _, ok := m[key]; !ok {
			t.Errorf("output missing preserved key %q", key)
		}
	}
}

func TestBuildPlatformResponsesRequest_DropsUnsupported(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "Hi"}],
		"n": 2,
		"stop": ["\n"],
		"frequency_penalty": 0.5,
		"presence_penalty": 0.5
	}`

	out, _, err := buildPlatformResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildPlatformResponsesRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	for _, key := range []string{"n", "stop", "frequency_penalty", "presence_penalty"} {
		if _, ok := m[key]; ok {
			t.Errorf("output should not contain dropped key %q", key)
		}
	}
}

func TestBuildPlatformResponsesRequest_MaxCompletionTokens(t *testing.T) {
	input := `{"model":"gpt-5.4","messages":[{"role":"user","content":"Hi"}],"max_completion_tokens":200}`

	out, _, err := buildPlatformResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildPlatformResponsesRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	if _, ok := m["max_completion_tokens"]; ok {
		t.Error("output contains 'max_completion_tokens' — should be renamed")
	}
	if _, ok := m["max_output_tokens"]; !ok {
		t.Error("output missing 'max_output_tokens'")
	}
}

func TestBuildPlatformResponsesRequest_StreamOptions(t *testing.T) {
	// stream_options should not be forwarded; include_usage extracted.
	input := `{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "Hi"}],
		"stream": true,
		"stream_options": {"include_usage": true}
	}`

	out, includeUsage, err := buildPlatformResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildPlatformResponsesRequest: %v", err)
	}
	if !includeUsage {
		t.Error("includeUsage should be true when stream_options.include_usage=true")
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)
	if _, ok := m["stream_options"]; ok {
		t.Error("output must not contain 'stream_options' — it is a client-contract field")
	}
}

// ---------------------------------------------------------------------------
// buildChatGPTCodexRequest
// ---------------------------------------------------------------------------

func TestBuildChatGPTCodexRequest_AllowlistOnly(t *testing.T) {
	// All of these should be dropped by the allowlist.
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "Be helpful."},
			{"role": "user", "content": "Hello"}
		],
		"stream": false,
		"store": true,
		"max_tokens": 100,
		"max_output_tokens": 100,
		"n": 2,
		"stop": ["\n"],
		"logprobs": true,
		"stream_options": {"include_usage": true},
		"frequency_penalty": 0.5,
		"presence_penalty": 0.5,
		"seed": 42,
		"response_format": {"type": "json_object"},
		"tools": [],
		"tool_choice": "auto",
		"parallel_tool_calls": true,
		"function_call": "auto",
		"functions": []
	}`

	out, includeUsage, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}
	if !includeUsage {
		t.Error("includeUsage should be true from stream_options.include_usage")
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	// Required fields.
	for _, k := range []string{"model", "input", "instructions", "stream", "store"} {
		if _, ok := m[k]; !ok {
			t.Errorf("output missing required field %q", k)
		}
	}
	// stream must be true, store must be false.
	var streamVal bool
	json.Unmarshal(m["stream"], &streamVal)
	if !streamVal {
		t.Error("stream must be forced to true in chatgpt mode")
	}
	var storeVal bool
	json.Unmarshal(m["store"], &storeVal)
	if storeVal {
		t.Error("store must be forced to false in chatgpt mode")
	}

	// None of these must appear.
	forbidden := []string{
		"max_tokens", "max_output_tokens", "max_completion_tokens",
		"n", "stop", "logprobs", "top_logprobs", "logit_bias",
		"stream_options", "frequency_penalty", "presence_penalty",
		"seed", "response_format", "tool_choice",
		"parallel_tool_calls", "function_call", "functions",
		"messages",
	}
	for _, k := range forbidden {
		if _, ok := m[k]; ok {
			t.Errorf("output must not contain %q — not in chatgpt.com allowlist", k)
		}
	}
}

func TestBuildChatGPTCodexRequest_SystemMessagesToInstructions(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [
			{"role": "system", "content": "First instruction."},
			{"role": "system", "content": "Second instruction."},
			{"role": "user", "content": "Hello"}
		]
	}`

	out, _, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	var instructions string
	json.Unmarshal(m["instructions"], &instructions)
	if instructions != "First instruction.\nSecond instruction." {
		t.Errorf("instructions = %q, want 'First instruction.\\nSecond instruction.'", instructions)
	}

	var inputMsgs []map[string]json.RawMessage
	json.Unmarshal(m["input"], &inputMsgs)
	if len(inputMsgs) != 1 {
		t.Errorf("input len = %d, want 1 (only user message)", len(inputMsgs))
	}
}

func TestBuildChatGPTCodexRequest_NoSystemMessages(t *testing.T) {
	// When there are no system messages, instructions must still be present as "".
	input := `{"model":"gpt-5.4","messages":[{"role":"user","content":"Hi"}]}`

	out, _, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}

	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)

	instrRaw, ok := m["instructions"]
	if !ok {
		t.Fatal("output missing 'instructions' — required even when empty")
	}
	var instructions string
	json.Unmarshal(instrRaw, &instructions)
	if instructions != "" {
		t.Errorf("instructions = %q, want empty string", instructions)
	}
}

func TestBuildChatGPTCodexRequest_StreamOptionsNoUsage(t *testing.T) {
	// stream_options absent → includeUsage=false
	input := `{"model":"gpt-5.4","messages":[{"role":"user","content":"Hi"}],"stream":true}`
	_, includeUsage, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}
	if includeUsage {
		t.Error("includeUsage should be false when stream_options is absent")
	}
}

// ---------------------------------------------------------------------------
// streamOptionsIncludeUsage
// ---------------------------------------------------------------------------

func TestStreamOptionsIncludeUsage(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"include_usage true", `{"stream_options":{"include_usage":true}}`, true},
		{"include_usage false", `{"stream_options":{"include_usage":false}}`, false},
		{"absent", `{"model":"gpt-5.4"}`, false},
		{"empty stream_options", `{"stream_options":{}}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw map[string]json.RawMessage
			json.Unmarshal([]byte(tt.body), &raw)
			got := streamOptionsIncludeUsage(raw)
			if got != tt.want {
				t.Errorf("streamOptionsIncludeUsage = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// responsesToChatCompletion
// ---------------------------------------------------------------------------

func TestResponsesToChatCompletion_Basic(t *testing.T) {
	respBody := `{
		"id": "resp_123",
		"object": "response",
		"created_at": 1709900000,
		"model": "gpt-5.4",
		"output": [
			{
				"type": "message",
				"id": "msg_1",
				"role": "assistant",
				"content": [
					{"type": "output_text", "text": "Hello! How can I help?"}
				]
			}
		],
		"usage": {
			"input_tokens": 10,
			"output_tokens": 8,
			"total_tokens": 18
		}
	}`

	out, err := responsesToChatCompletion([]byte(respBody))
	if err != nil {
		t.Fatalf("responsesToChatCompletion: %v", err)
	}

	var cc struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Index   int `json:"index"`
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &cc); err != nil {
		t.Fatalf("unmarshal chat completion: %v", err)
	}

	if cc.ID != "resp_123" {
		t.Errorf("id = %q, want resp_123", cc.ID)
	}
	if cc.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", cc.Object)
	}
	if cc.Model != "gpt-5.4" {
		t.Errorf("model = %q, want gpt-5.4", cc.Model)
	}
	if len(cc.Choices) != 1 {
		t.Fatalf("choices count = %d, want 1", len(cc.Choices))
	}
	if cc.Choices[0].Message.Role != "assistant" {
		t.Errorf("role = %q, want assistant", cc.Choices[0].Message.Role)
	}
	if cc.Choices[0].Message.Content != "Hello! How can I help?" {
		t.Errorf("content = %q", cc.Choices[0].Message.Content)
	}
	if cc.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", cc.Choices[0].FinishReason)
	}
	if cc.Usage.PromptTokens != 10 {
		t.Errorf("prompt_tokens = %d, want 10", cc.Usage.PromptTokens)
	}
	if cc.Usage.CompletionTokens != 8 {
		t.Errorf("completion_tokens = %d, want 8", cc.Usage.CompletionTokens)
	}
}

func TestResponsesToChatCompletion_Error(t *testing.T) {
	respBody := `{
		"id": "resp_err",
		"object": "response",
		"created_at": 1709900000,
		"model": "gpt-5.4",
		"output": [],
		"error": {
			"message": "Rate limit exceeded",
			"type": "rate_limit_error",
			"code": "rate_limit"
		}
	}`

	out, err := responsesToChatCompletion([]byte(respBody))
	if err != nil {
		t.Fatalf("responsesToChatCompletion: %v", err)
	}

	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &errResp); err != nil {
		t.Fatalf("unmarshal error response: %v", err)
	}
	if errResp.Error.Message != "Rate limit exceeded" {
		t.Errorf("error message = %q, want 'Rate limit exceeded'", errResp.Error.Message)
	}
}

func TestBuildChatCompletionsRequestFromResponses_Basic(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"instructions": "Be concise.",
		"input": [
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Hello"}]}
		],
		"stream": true,
		"max_output_tokens": 123
	}`

	out, streaming, err := buildChatCompletionsRequestFromResponses([]byte(input))
	if err != nil {
		t.Fatalf("buildChatCompletionsRequestFromResponses: %v", err)
	}
	if !streaming {
		t.Fatal("streaming = false, want true")
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got := string(m["model"]); got != `"gpt-5.4"` {
		t.Fatalf("model = %s, want \"gpt-5.4\"", got)
	}
	var messages []map[string]json.RawMessage
	if err := json.Unmarshal(m["messages"], &messages); err != nil {
		t.Fatalf("unmarshal messages: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(messages))
	}
	var systemRole string
	_ = json.Unmarshal(messages[0]["role"], &systemRole)
	if systemRole != "system" {
		t.Fatalf("messages[0].role = %q, want system", systemRole)
	}
	var userRole string
	_ = json.Unmarshal(messages[1]["role"], &userRole)
	if userRole != "user" {
		t.Fatalf("messages[1].role = %q, want user", userRole)
	}
}

func TestBuildChatCompletionsRequestFromResponses_RejectsUnsupportedInputItem(t *testing.T) {
	_, _, err := buildChatCompletionsRequestFromResponses([]byte(`{"model":"gpt-5.4","input":[{"type":"computer_call"}]}`))
	if err == nil {
		t.Fatal("expected error for unsupported input item")
	}
}

func TestBuildChatCompletionsRequestFromResponses_ReasoningEffortOnly(t *testing.T) {
	out, _, err := buildChatCompletionsRequestFromResponses([]byte(`{"model":"gpt-5.4","input":"hi","reasoning":{"effort":"high","summary":"detailed"}}`))
	if err != nil {
		t.Fatalf("buildChatCompletionsRequestFromResponses: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	var reasoning map[string]interface{}
	if err := json.Unmarshal(m["reasoning"], &reasoning); err != nil {
		t.Fatalf("unmarshal reasoning: %v", err)
	}
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning.effort = %#v, want high", reasoning["effort"])
	}
	if _, ok := reasoning["summary"]; ok {
		t.Fatal("reasoning.summary should not be forwarded")
	}
}

func TestBuildChatGPTCodexRequestFromResponses_EnforcesStoreFalseWhenOmitted(t *testing.T) {
	out, streaming, err := buildChatGPTCodexRequestFromResponses([]byte(`{"model":"gpt-5.4","input":"hi"}`))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequestFromResponses: %v", err)
	}
	if streaming {
		t.Fatal("streaming = true, want false")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	var store bool
	if err := json.Unmarshal(raw["store"], &store); err != nil {
		t.Fatalf("unmarshal store: %v", err)
	}
	if store {
		t.Fatal("store = true, want false")
	}
}

func TestBuildChatGPTCodexRequestFromResponses_OverridesStoreTrue(t *testing.T) {
	out, streaming, err := buildChatGPTCodexRequestFromResponses([]byte(`{"model":"gpt-5.4","input":"hi","stream":true,"store":true}`))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequestFromResponses: %v", err)
	}
	if !streaming {
		t.Fatal("streaming = false, want true")
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	var store bool
	if err := json.Unmarshal(raw["store"], &store); err != nil {
		t.Fatalf("unmarshal store: %v", err)
	}
	if store {
		t.Fatal("store = true, want false")
	}
}

func TestChatCompletionToResponses_Basic(t *testing.T) {
	chat := `{
		"id":"chatcmpl_1",
		"object":"chat.completion",
		"created":1700000000,
		"model":"gpt-5.4",
		"choices":[{"index":0,"message":{"role":"assistant","content":"Hello there"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":3,"total_tokens":13}
	}`
	out, err := chatCompletionToResponses([]byte(chat))
	if err != nil {
		t.Fatalf("chatCompletionToResponses: %v", err)
	}
	var resp struct {
		Object string `json:"object"`
		Model  string `json:"model"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Object != "response" {
		t.Fatalf("object = %q, want response", resp.Object)
	}
	if resp.Model != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", resp.Model)
	}
	if len(resp.Output) != 1 || resp.Output[0].Type != "message" {
		t.Fatalf("unexpected output = %+v", resp.Output)
	}
	if len(resp.Output[0].Content) != 1 || resp.Output[0].Content[0].Type != "output_text" {
		t.Fatalf("unexpected output content = %+v", resp.Output[0].Content)
	}
}

func TestChatCompletionStreamToResponses_Basic(t *testing.T) {
	chatSSE := "data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-5.4\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-5.4\",\"choices\":[{\"delta\":{\"content\":\"Hel\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl_1\",\"model\":\"gpt-5.4\",\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":null}]}\n\n" +
		"data: [DONE]\n\n"
	out := string(chatCompletionStreamToResponses([]byte(chatSSE)))
	if !strings.Contains(out, "event: response.created") {
		t.Fatalf("missing response.created event: %s", out)
	}
	if !strings.Contains(out, "event: response.output_text.delta") {
		t.Fatalf("missing response.output_text.delta event: %s", out)
	}
	if !strings.Contains(out, "event: response.completed") {
		t.Fatalf("missing response.completed event: %s", out)
	}
}

// ---------------------------------------------------------------------------
// isStreamingRequest
// ---------------------------------------------------------------------------

func TestIsStreamingRequest(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{`{"model":"gpt-5.4","stream":true}`, true},
		{`{"model":"gpt-5.4","stream":false}`, false},
		{`{"model":"gpt-5.4"}`, false},
		{`{"model":"gpt-5.4","stream":"yes"}`, false},
		{`invalid json`, false},
	}
	for _, tt := range tests {
		got := isStreamingRequest([]byte(tt.body))
		if got != tt.want {
			t.Errorf("isStreamingRequest(%s) = %v, want %v", tt.body, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// extractAccountIDFromJWT
// ---------------------------------------------------------------------------

func TestExtractAccountIDFromJWT(t *testing.T) {
	payload := map[string]interface{}{
		"https://api.openai.com/auth": map[string]string{
			"chatgpt_account_id": "acct-test-123",
		},
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)
	fakeJWT := "eyJhbGciOiJSUzI1NiJ9." + payloadB64 + ".fakesig"

	accountID := extractAccountIDFromJWT(fakeJWT)
	if accountID != "acct-test-123" {
		t.Errorf("accountID = %q, want acct-test-123", accountID)
	}
}

func TestExtractAccountIDFromJWT_Invalid(t *testing.T) {
	accountID := extractAccountIDFromJWT("not-a-jwt")
	if accountID != "" {
		t.Errorf("expected empty, got %q", accountID)
	}
	accountID = extractAccountIDFromJWT("")
	if accountID != "" {
		t.Errorf("expected empty for empty string, got %q", accountID)
	}
}

// -----------------------------------------------------------------------
// Tool conversion tests
// -----------------------------------------------------------------------

func TestConvertToolsForResponses(t *testing.T) {
	input := `[
		{
			"type": "function",
			"function": {
				"name": "get_weather",
				"description": "Get weather",
				"parameters": {"type":"object","properties":{"city":{"type":"string"}}}
			}
		},
		{
			"type": "function",
			"function": {
				"name": "list_files",
				"description": "List files in dir",
				"parameters": {"type":"object","properties":{"path":{"type":"string"}}},
				"strict": true
			}
		}
	]`
	out := convertToolsForResponses(json.RawMessage(input))
	var tools []map[string]interface{}
	if err := json.Unmarshal(out, &tools); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	// First tool: no strict field.
	if tools[0]["name"] != "get_weather" {
		t.Errorf("tool[0].name = %v, want get_weather", tools[0]["name"])
	}
	if tools[0]["type"] != "function" {
		t.Errorf("tool[0].type = %v, want function", tools[0]["type"])
	}
	if _, ok := tools[0]["function"]; ok {
		t.Error("tool[0] must not have nested 'function' key")
	}
	if _, ok := tools[0]["strict"]; ok {
		t.Error("tool[0] should not have strict (was nil)")
	}
	// Second tool: strict=true should be present.
	if tools[1]["name"] != "list_files" {
		t.Errorf("tool[1].name = %v, want list_files", tools[1]["name"])
	}
	if tools[1]["strict"] != true {
		t.Errorf("tool[1].strict = %v, want true", tools[1]["strict"])
	}
}

func TestBuildChatGPTCodexRequest_ToolsConverted(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "Use get_weather tool"}],
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "get_weather",
					"description": "Get weather",
					"parameters": {"type": "object"}
				}
			}
		],
		"stream": true
	}`
	out, _, err := buildChatGPTCodexRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildChatGPTCodexRequest: %v", err)
	}
	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)
	if _, ok := m["tools"]; !ok {
		t.Fatal("output must contain 'tools'")
	}
	// Verify Responses API format (flat, no nested "function").
	var tools []map[string]interface{}
	json.Unmarshal(m["tools"], &tools)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0]["name"] != "get_weather" {
		t.Errorf("tool.name = %v, want get_weather", tools[0]["name"])
	}
	if _, ok := tools[0]["function"]; ok {
		t.Error("Responses API tool must not have nested 'function' key")
	}
}

func TestBuildPlatformResponsesRequest_ToolsConverted(t *testing.T) {
	input := `{
		"model": "gpt-5.4",
		"messages": [{"role": "user", "content": "Hello"}],
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "read_file",
					"description": "Read file",
					"parameters": {"type": "object"}
				}
			}
		]
	}`
	out, _, err := buildPlatformResponsesRequest([]byte(input))
	if err != nil {
		t.Fatalf("buildPlatformResponsesRequest: %v", err)
	}
	var m map[string]json.RawMessage
	json.Unmarshal(out, &m)
	if _, ok := m["tools"]; !ok {
		t.Fatal("tools should be converted and forwarded, not dropped")
	}
	var tools []map[string]interface{}
	json.Unmarshal(m["tools"], &tools)
	if tools[0]["name"] != "read_file" {
		t.Errorf("tool.name = %v, want read_file", tools[0]["name"])
	}
}

// -----------------------------------------------------------------------
// Non-streaming tool_call conversion tests
// -----------------------------------------------------------------------

func TestResponsesToChatCompletion_FunctionCall(t *testing.T) {
	input := `{
		"id": "resp_123",
		"object": "response",
		"created_at": 1700000000,
		"model": "gpt-5.4",
		"output": [
			{
				"type": "function_call",
				"id": "fc_1",
				"call_id": "call_abc",
				"name": "get_weather",
				"arguments": "{\"city\":\"Hanoi\"}"
			}
		],
		"usage": {"input_tokens": 100, "output_tokens": 20, "total_tokens": 120}
	}`
	out, err := responsesToChatCompletion([]byte(input))
	if err != nil {
		t.Fatalf("responsesToChatCompletion: %v", err)
	}
	var cc map[string]interface{}
	json.Unmarshal(out, &cc)
	choices := cc["choices"].([]interface{})
	choice := choices[0].(map[string]interface{})
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]interface{})
	if msg["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", msg["role"])
	}
	toolCalls := msg["tool_calls"].([]interface{})
	if len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %d", len(toolCalls))
	}
	tc := toolCalls[0].(map[string]interface{})
	if tc["id"] != "call_abc" {
		t.Errorf("tool_call.id = %v, want call_abc", tc["id"])
	}
	fn := tc["function"].(map[string]interface{})
	if fn["name"] != "get_weather" {
		t.Errorf("function.name = %v, want get_weather", fn["name"])
	}
	if fn["arguments"] != "{\"city\":\"Hanoi\"}" {
		t.Errorf("function.arguments = %v", fn["arguments"])
	}
}

func TestResponsesToChatCompletion_MessageAndFunctionCall(t *testing.T) {
	// Mixed output: text message + tool call.
	input := `{
		"id": "resp_456",
		"object": "response",
		"created_at": 1700000000,
		"model": "gpt-5.4",
		"output": [
			{
				"type": "message",
				"id": "msg_1",
				"role": "assistant",
				"content": [{"type": "output_text", "text": "Let me check"}]
			},
			{
				"type": "function_call",
				"id": "fc_1",
				"call_id": "call_xyz",
				"name": "search",
				"arguments": "{\"q\":\"test\"}"
			}
		]
	}`
	out, err := responsesToChatCompletion([]byte(input))
	if err != nil {
		t.Fatalf("responsesToChatCompletion: %v", err)
	}
	var cc map[string]interface{}
	json.Unmarshal(out, &cc)
	choices := cc["choices"].([]interface{})
	choice := choices[0].(map[string]interface{})
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason should be tool_calls when tools present")
	}
	msg := choice["message"].(map[string]interface{})
	if msg["content"] != "Let me check" {
		t.Errorf("content = %v, want 'Let me check'", msg["content"])
	}
	if msg["tool_calls"] == nil {
		t.Error("tool_calls should be present")
	}
}

// -----------------------------------------------------------------------
// Streaming tool_call SSE tests
// -----------------------------------------------------------------------

func TestStreamResponsesAsChat_ToolCall(t *testing.T) {
	sseData := "event: response.created\n" +
		"data: {\"response\":{\"id\":\"resp_t1\",\"model\":\"gpt-5.4\"}}\n\n" +
		"event: response.output_item.added\n" +
		"data: {\"item\":{\"id\":\"fc_001\",\"type\":\"function_call\",\"call_id\":\"call_001\",\"name\":\"get_weather\"},\"output_index\":0}\n\n" +
		"event: response.function_call_arguments.delta\n" +
		"data: {\"delta\":\"{\\\"ci\",\"item_id\":\"fc_001\"}\n\n" +
		"event: response.function_call_arguments.delta\n" +
		"data: {\"delta\":\"ty\\\":\\\"HN\\\"}\",\"item_id\":\"fc_001\"}\n\n" +
		"event: response.completed\n" +
		"data: {\"response\":{\"usage\":{\"input_tokens\":50,\"output_tokens\":15,\"total_tokens\":65}}}\n\n"

	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(sseData)),
	}
	rec := httptest.NewRecorder()
	if err := streamResponsesAsChat(rec, resp, true); err != nil {
		t.Fatalf("streamResponsesAsChat: %v", err)
	}
	body := rec.Body.String()

	// Should have role=assistant in the first chunk.
	if !strings.Contains(body, `"role":"assistant"`) {
		t.Error("missing role:assistant in streaming output")
	}
	// Should have tool_calls with function name.
	if !strings.Contains(body, `"name":"get_weather"`) {
		t.Error("missing tool function name in streaming output")
	}
	// Should have argument deltas.
	if !strings.Contains(body, `"arguments":"{\"ci"`) {
		t.Errorf("missing first argument delta")
	}
	if !strings.Contains(body, `"arguments":"ty\":\"HN\"}"`) {
		t.Errorf("missing second argument delta")
	}
	// finish_reason should be tool_calls.
	if !strings.Contains(body, `"finish_reason":"tool_calls"`) {
		t.Error("finish_reason should be tool_calls")
	}
	// Usage should be present.
	if !strings.Contains(body, `"completion_tokens":15`) {
		t.Error("missing completion_tokens in usage")
	}
	// Should end with [DONE].
	if !strings.Contains(body, "data: [DONE]") {
		t.Error("missing [DONE] marker")
	}
}

func TestStreamResponsesAsChat_TextAndToolCall(t *testing.T) {
	// Mixed: text output then a tool call.
	sseData := "event: response.created\n" +
		"data: {\"response\":{\"id\":\"resp_mix\",\"model\":\"gpt-5.4\"}}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"delta\":\"Let me check\"}\n\n" +
		"event: response.output_item.added\n" +
		"data: {\"item\":{\"id\":\"fc_m1\",\"type\":\"function_call\",\"call_id\":\"call_m1\",\"name\":\"search\"},\"output_index\":1}\n\n" +
		"event: response.function_call_arguments.delta\n" +
		"data: {\"delta\":\"{}\",\"item_id\":\"fc_m1\"}\n\n" +
		"event: response.completed\n" +
		"data: {\"response\":{\"usage\":{\"input_tokens\":30,\"output_tokens\":10,\"total_tokens\":40}}}\n\n"

	resp := &http.Response{
		Body: io.NopCloser(strings.NewReader(sseData)),
	}
	rec := httptest.NewRecorder()
	if err := streamResponsesAsChat(rec, resp, false); err != nil {
		t.Fatalf("streamResponsesAsChat: %v", err)
	}
	body := rec.Body.String()

	// Text content should be present.
	if !strings.Contains(body, `"content":"Let me check"`) {
		t.Error("missing text content")
	}
	// Tool call should be present.
	if !strings.Contains(body, `"name":"search"`) {
		t.Error("missing tool call name")
	}
	// finish_reason should be tool_calls (tool calls override stop).
	if !strings.Contains(body, `"finish_reason":"tool_calls"`) {
		t.Error("finish_reason should be tool_calls when mixed output")
	}
}

// -----------------------------------------------------------------------
// extractMessages: tool role and assistant tool_calls conversion
// -----------------------------------------------------------------------

func TestExtractMessages_ToolRoleConversion(t *testing.T) {
	messages := `[
		{"role":"system","content":"You are helpful."},
		{"role":"user","content":"What's the weather?"},
		{"role":"assistant","content":"","tool_calls":[
			{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Hanoi\"}"}}
		]},
		{"role":"tool","tool_call_id":"call_abc","content":"25°C, sunny"}
	]`
	instr, inputJSON, err := extractMessages(json.RawMessage(messages))
	if err != nil {
		t.Fatalf("extractMessages: %v", err)
	}
	if instr != "You are helpful." {
		t.Errorf("instructions = %q, want 'You are helpful.'", instr)
	}
	// Parse the input array.
	var items []map[string]interface{}
	if err := json.Unmarshal(inputJSON, &items); err != nil {
		t.Fatalf("unmarshal inputJSON: %v", err)
	}
	// Item 0: user message
	if items[0]["role"] != "user" {
		t.Errorf("item[0].role = %v, want user", items[0]["role"])
	}
	// Item 1: function_call (from assistant tool_calls)
	if items[1]["type"] != "function_call" {
		t.Errorf("item[1].type = %v, want function_call", items[1]["type"])
	}
	if items[1]["call_id"] != "call_abc" {
		t.Errorf("item[1].call_id = %v, want call_abc", items[1]["call_id"])
	}
	if items[1]["name"] != "get_weather" {
		t.Errorf("item[1].name = %v, want get_weather", items[1]["name"])
	}
	// Item 2: function_call_output (from tool role)
	if items[2]["type"] != "function_call_output" {
		t.Errorf("item[2].type = %v, want function_call_output", items[2]["type"])
	}
	if items[2]["call_id"] != "call_abc" {
		t.Errorf("item[2].call_id = %v, want call_abc", items[2]["call_id"])
	}
	if items[2]["output"] != "25°C, sunny" {
		t.Errorf("item[2].output = %v, want '25°C, sunny'", items[2]["output"])
	}
}

func TestExtractMessages_AssistantWithContentAndToolCalls(t *testing.T) {
	messages := `[
		{"role":"user","content":"Search for X"},
		{"role":"assistant","content":"Let me search for that.","tool_calls":[
			{"id":"call_1","type":"function","function":{"name":"search","arguments":"{}"}}
		]}
	]`
	_, inputJSON, err := extractMessages(json.RawMessage(messages))
	if err != nil {
		t.Fatalf("extractMessages: %v", err)
	}
	var items []map[string]interface{}
	json.Unmarshal(inputJSON, &items)
	// Should have 3 items: user, assistant text, function_call
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d: %s", len(items), string(inputJSON))
	}
	// Assistant text message first.
	if items[1]["role"] != "assistant" {
		t.Errorf("item[1] should be assistant message")
	}
	// Then function_call.
	if items[2]["type"] != "function_call" {
		t.Errorf("item[2].type = %v, want function_call", items[2]["type"])
	}
}
