// responses_adapter_test.go — contract tests for Chat Completions ↔ Responses.
package yarouter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeContentParts(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantType string
	}{
		{name: "text", input: `[{
			"type":"text","text":"hello"
		}]`, wantType: "input_text"},
		{name: "image", input: `[{
			"type":"image_url","image_url":{"url":"https://example.test/image.png"}
		}]`, wantType: "input_image"},
		{name: "unknown", input: `[{
			"type":"computer_screenshot","data":"x"
		}]`, wantType: "computer_screenshot"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var parts []map[string]json.RawMessage
			if err := json.Unmarshal(normalizeContentParts(json.RawMessage(test.input)), &parts); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := json.Unmarshal(parts[0]["type"], &got); err != nil {
				t.Fatal(err)
			}
			if got != test.wantType {
				t.Fatalf("type=%q want=%q", got, test.wantType)
			}
		})
	}
	plain := json.RawMessage(`"hello"`)
	if got := normalizeContentParts(plain); string(got) != string(plain) {
		t.Fatalf("plain content changed: %s", got)
	}
}

func TestExtractMessagesSplitsInstructionsAndInput(t *testing.T) {
	messages := json.RawMessage(`[
		{"role":"system","content":"first"},
		{"role":"system","content":[{"type":"text","text":"second"}]},
		{"role":"user","content":[{"type":"text","text":"hello"}]}
	]`)
	instructions, input, err := extractMessages(messages)
	if err != nil {
		t.Fatal(err)
	}
	if instructions != "first\nsecond" {
		t.Fatalf("instructions=%q", instructions)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(input, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("input items=%d", len(items))
	}
	if !strings.Contains(string(items[0]["content"]), "input_text") {
		t.Fatalf("content was not normalized: %s", items[0]["content"])
	}
}

func TestExtractMessagesConvertsToolRoundTrip(t *testing.T) {
	messages := json.RawMessage(`[
		{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{\"q\":1}"}}]},
		{"role":"tool","tool_call_id":"call-1","content":"done"}
	]`)
	_, input, err := extractMessages(messages)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(input), `"function_call"`) || !strings.Contains(string(input), `"function_call_output"`) {
		t.Fatalf("tool round-trip missing: %s", input)
	}
}

func TestConvertToolsForResponses(t *testing.T) {
	input := json.RawMessage(`[{
		"type":"function",
		"function":{"name":"lookup","description":"find data","parameters":{"type":"object"},"strict":true}
	}]`)
	output := convertToolsForResponses(input)
	if strings.Contains(string(output), `"function":`) {
		t.Fatalf("tool was not flattened: %s", output)
	}
	if !strings.Contains(string(output), `"name":"lookup"`) || !strings.Contains(string(output), `"strict":true`) {
		t.Fatalf("tool fields missing: %s", output)
	}
}

func TestBuildChatGPTCodexRequestPreservesStructuredOutput(t *testing.T) {
	input := []byte(`{
		"model":"gpt-5.4",
		"messages":[{"role":"system","content":"be exact"},{"role":"user","content":"hello"}],
		"stream":false,
		"stream_options":{"include_usage":true},
		"response_format":{"type":"json_schema","json_schema":{"name":"answer","strict":true,"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}}},
		"tool_choice":"auto",
		"parallel_tool_calls":true
	}`)
	output, includeUsage, err := buildChatGPTCodexRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if !includeUsage {
		t.Fatal("include_usage was not preserved locally")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(output, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["messages"]; ok {
		t.Fatal("messages must be converted")
	}
	if _, ok := body["text"]; !ok {
		t.Fatalf("text.format missing: %s", output)
	}
	if _, ok := body["tool_choice"]; !ok {
		t.Fatal("tool_choice was dropped")
	}
	if _, ok := body["parallel_tool_calls"]; !ok {
		t.Fatal("parallel_tool_calls was dropped")
	}
	var stream, store bool
	_ = json.Unmarshal(body["stream"], &stream)
	_ = json.Unmarshal(body["store"], &store)
	if !stream || store {
		t.Fatalf("stream=%v store=%v", stream, store)
	}
}

func TestBuildChatGPTCodexRequestRejectsUnmappableFields(t *testing.T) {
	for _, field := range []string{"n", "stop", "logprobs", "frequency_penalty", "function_call"} {
		input := `{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"` + field + `":true}`
		if _, _, err := buildChatGPTCodexRequest([]byte(input)); err == nil {
			t.Fatalf("expected %s to fail explicitly", field)
		}
	}
}

func TestBuildPlatformResponsesRequest(t *testing.T) {
	input := []byte(`{
		"model":"gpt-5.4",
		"messages":[{"role":"system","content":"help"},{"role":"user","content":"hello"}],
		"max_tokens":100,
		"stream":true,
		"stream_options":{"include_usage":true},
		"response_format":{"type":"json_object"},
		"tool_choice":"auto",
		"parallel_tool_calls":true
	}`)
	output, includeUsage, err := buildPlatformResponsesRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if !includeUsage {
		t.Fatal("usage option was not consumed")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(output, &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"input", "instructions", "max_output_tokens", "text", "tool_choice", "parallel_tool_calls"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("missing key %q in %s", key, output)
		}
	}
	if _, ok := body["stream_options"]; ok {
		t.Fatal("stream_options leaked upstream")
	}
}

func TestBuildPlatformResponsesRequestRejectsUnsupportedLegacyFields(t *testing.T) {
	input := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hi"}],"n":2}`)
	if _, _, err := buildPlatformResponsesRequest(input); err == nil {
		t.Fatal("expected unsupported field to fail explicitly")
	}
}

func TestBuildNativeResponsesRequests(t *testing.T) {
	input := []byte(`{"model":"gpt-5.4","input":"hello","stream":false,"text":{"format":{"type":"json_object"}}}`)
	chatGPTBody, clientStream, err := buildChatGPTNativeResponsesRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if clientStream {
		t.Fatal("client stream preference should remain false")
	}
	if !strings.Contains(string(chatGPTBody), `"stream":true`) || !strings.Contains(string(chatGPTBody), `"store":false`) {
		t.Fatalf("ChatGPT endpoint requirements missing: %s", chatGPTBody)
	}
	platformBody, _, err := buildPlatformNativeResponsesRequest(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(platformBody), `"text"`) {
		t.Fatalf("native Platform field lost: %s", platformBody)
	}
}

func TestBuildChatGPTNativeResponsesRequestNormalizesStringInputToList(t *testing.T) {
	// Given
	input := []byte(`{"model":"gpt-5.4","input":"hello"}`)

	// When
	output, _, err := buildChatGPTNativeResponsesRequest(input)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	var request struct {
		Input []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(output, &request); err != nil {
		t.Fatalf("decode normalized request: %v", err)
	}
	if len(request.Input) != 1 || request.Input[0].Role != "user" || request.Input[0].Content != "hello" {
		t.Fatalf("input = %+v, want one user message", request.Input)
	}
}

func TestBuildChatGPTNativeResponsesRequestRejectsUnknownField(t *testing.T) {
	input := []byte(`{"model":"gpt-5.4","input":"hello","unknown":true}`)
	if _, _, err := buildChatGPTNativeResponsesRequest(input); err == nil {
		t.Fatal("expected unknown ChatGPT transport field to fail")
	}
}

func TestResponsesToChatCompletionTextAndUsage(t *testing.T) {
	input := []byte(`{
		"id":"resp-1","object":"response","created_at":1700000000,"model":"gpt-5.4",
		"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],
		"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}
	}`)
	output, err := responsesToChatCompletion(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `"content":"hello"`) || !strings.Contains(string(output), `"total_tokens":5`) {
		t.Fatalf("unexpected completion: %s", output)
	}
}

func TestResponsesToChatCompletionToolCall(t *testing.T) {
	input := []byte(`{
		"id":"resp-1","model":"gpt-5.4",
		"output":[{"type":"function_call","call_id":"call-1","name":"lookup","arguments":"{\"q\":1}"}]
	}`)
	output, err := responsesToChatCompletion(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `"finish_reason":"tool_calls"`) || !strings.Contains(string(output), `"name":"lookup"`) {
		t.Fatalf("tool call lost: %s", output)
	}
}

func TestAggregateSSEToCompletion(t *testing.T) {
	stream := "event: response.created\ndata: {}\n\n" +
		"event: response.completed\ndata: {\"response\":{\"id\":\"r1\",\"output\":[]}}\n\n"
	output, err := aggregateSSEToCompletion(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `"id":"r1"`) {
		t.Fatalf("unexpected aggregate: %s", output)
	}
}

func TestAggregateSSEToCompletionMergesTextDeltasWhenOutputEmpty(t *testing.T) {
	stream := "event: response.output_text.delta\ndata: {\"delta\":\"Hel\"}\n\n" +
		"event: response.output_text.delta\ndata: {\"delta\":\"lo\"}\n\n" +
		"event: response.completed\ndata: {\"response\":{\"id\":\"r1\",\"output\":[]}}\n\n"
	output, err := aggregateSSEToCompletion(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `"text":"Hello"`) {
		t.Fatalf("expected accumulated delta text in output, got: %s", output)
	}
}

func TestAggregateSSEToCompletionKeepsExistingOutputOverDeltas(t *testing.T) {
	stream := "event: response.output_text.delta\ndata: {\"delta\":\"ignored\"}\n\n" +
		"event: response.completed\ndata: {\"response\":{\"id\":\"r1\",\"output\":[{\"type\":\"message\"}]}}\n\n"
	output, err := aggregateSSEToCompletion(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "ignored") {
		t.Fatalf("delta text should not override a non-empty output array: %s", output)
	}
}

func TestMergeAccumulatedTextSynthesizesOutputFromDeltas(t *testing.T) {
	merged, err := mergeAccumulatedText([]byte(`{"id":"r1","output":[]}`), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(merged), `"text":"hello"`) || !strings.Contains(string(merged), `"role":"assistant"`) {
		t.Fatalf("expected synthesized assistant message, got: %s", merged)
	}
}

func TestMergeAccumulatedTextNoopWhenTextEmpty(t *testing.T) {
	original := []byte(`{"id":"r1","output":[]}`)
	merged, err := mergeAccumulatedText(original, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(merged) != string(original) {
		t.Fatalf("expected unchanged response when no delta text, got: %s", merged)
	}
}

func TestAggregateSSEFailureIsNotSuccess(t *testing.T) {
	stream := "event: response.failed\ndata: {\"error\":{\"message\":\"failed\"}}\n\n"
	if _, err := aggregateSSEToCompletion(strings.NewReader(stream)); err == nil {
		t.Fatal("expected failed SSE event to return an error")
	}
}

func TestHandleNativeResponsesAggregatesForcedSSE(t *testing.T) {
	stream := "event: response.completed\ndata: {\"response\":{\"id\":\"r1\",\"object\":\"response\",\"output\":[]}}\n\n"
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}
	response.Body = &readCloser{Reader: strings.NewReader(stream)}
	recorder := httptest.NewRecorder()
	if err := handleNativeResponsesAPIResponse(recorder, response, false, true); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"object":"response"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type readCloser struct{ *strings.Reader }

func (r *readCloser) Close() error { return nil }
