// responses_adapter.go — Converts between OpenAI Chat Completions and
// Responses API formats.  The Codex device_code token (ChatGPT Plus) is
// authorised for chatgpt.com/backend-api/codex/responses (ChatGPT mode) or
// api.openai.com/v1/responses (api_key mode).  Two transport-specific
// request builders produce the correct body for each backend:
//
//   - buildChatGPTCodexRequest  — strict allowlist, forces stream/store
//   - buildPlatformResponsesRequest — generic conversion, drop-list based
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type responsesRequest struct {
	Model        string          `json:"model"`
	Input        json.RawMessage `json:"input"`
	Instructions string          `json:"instructions,omitempty"`
	Stream       bool            `json:"stream,omitempty"`
	Store        *bool           `json:"store,omitempty"`
	Temperature  *float64        `json:"temperature,omitempty"`
	TopP         *float64        `json:"top_p,omitempty"`
	Tools        json.RawMessage `json:"tools,omitempty"`
	MaxOutputTok *int            `json:"max_output_tokens,omitempty"`
	User         string          `json:"user,omitempty"`
	Reasoning    json.RawMessage `json:"reasoning,omitempty"`
}

func buildChatCompletionsRequestFromResponses(respBody []byte) ([]byte, bool, error) {
	var req responsesRequest
	if err := json.Unmarshal(respBody, &req); err != nil {
		return nil, false, fmt.Errorf("parse responses request: %w", err)
	}
	if req.Model == "" {
		return nil, false, fmt.Errorf("missing required field \"model\"")
	}
	if len(req.Input) == 0 {
		return nil, false, fmt.Errorf("missing required field \"input\"")
	}

	messages, err := responsesInputToChatMessages(req.Instructions, req.Input)
	if err != nil {
		return nil, false, err
	}

	out := map[string]interface{}{
		"model":    req.Model,
		"messages": messages,
		"stream":   req.Stream,
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if req.MaxOutputTok != nil {
		out["max_tokens"] = *req.MaxOutputTok
	}
	if req.User != "" {
		out["user"] = req.User
	}
	if len(req.Tools) > 0 {
		out["tools"] = convertToolsForChat(req.Tools)
	}
	applyResponsesReasoningToChat(out, req.Reasoning)
	b, err := json.Marshal(out)
	return b, req.Stream, err
}

func normalizeResponsesRequestForCodex(respBody []byte) ([]byte, responsesRequest, error) {
	var req responsesRequest
	if err := json.Unmarshal(respBody, &req); err != nil {
		return nil, responsesRequest{}, fmt.Errorf("parse responses request: %w", err)
	}
	storeFalse := false
	req.Store = &storeFalse
	normalized, err := json.Marshal(req)
	if err != nil {
		return nil, responsesRequest{}, fmt.Errorf("marshal normalized responses request: %w", err)
	}
	return normalized, req, nil
}

func applyResponsesReasoningToChat(out map[string]interface{}, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var reasoning map[string]interface{}
	if err := json.Unmarshal(raw, &reasoning); err != nil {
		return
	}
	if len(reasoning) == 0 {
		return
	}
	sanitized := map[string]interface{}{}
	for _, key := range []string{"effort"} {
		if v, ok := reasoning[key]; ok {
			sanitized[key] = v
		}
	}
	if len(sanitized) > 0 {
		out["reasoning"] = sanitized
	}
}

func sanitizeChatBodyForProvider(body []byte, providerID ProviderID) []byte {
	if providerID == ProviderCodex {
		return body
	}
	return sanitizeResponsesBodyForProvider(body, providerID)
}

func sanitizeResponsesBodyForProvider(body []byte, providerID ProviderID) []byte {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return body
	}
	changed := false
	if providerID == ProviderCodex {
		if storeRaw, ok := raw["store"]; ok {
			var store bool
			if json.Unmarshal(storeRaw, &store) == nil && store {
				raw["store"] = json.RawMessage("false")
				changed = true
			}
		}
	}
	fields := []string{"reasoningSummary"}
	if providerID != ProviderCodex {
		fields = append([]string{"reasoning"}, fields...)
	}
	for _, field := range fields {
		if _, ok := raw[field]; ok {
			delete(raw, field)
			changed = true
		}
	}
	if !changed {
		return body
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return body
	}
	return b
}

func responsesInputToChatMessages(instructions string, input json.RawMessage) ([]map[string]interface{}, error) {
	msgs := make([]map[string]interface{}, 0, 4)
	if instructions != "" {
		msgs = append(msgs, map[string]interface{}{"role": "system", "content": instructions})
	}

	var asString string
	if json.Unmarshal(input, &asString) == nil {
		msgs = append(msgs, map[string]interface{}{"role": "user", "content": asString})
		return msgs, nil
	}

	var items []map[string]json.RawMessage
	if err := json.Unmarshal(input, &items); err != nil {
		return nil, fmt.Errorf("unsupported responses input format")
	}
	for _, item := range items {
		var itemType string
		_ = json.Unmarshal(item["type"], &itemType)
		switch itemType {
		case "message", "":
			var role string
			if json.Unmarshal(item["role"], &role) != nil || role == "" {
				role = "user"
			}
			content := normalizeResponsesInputContent(item["content"])
			msgs = append(msgs, map[string]interface{}{"role": role, "content": content})
		case "function_call_output":
			var callID, output string
			_ = json.Unmarshal(item["call_id"], &callID)
			_ = json.Unmarshal(item["output"], &output)
			msgs = append(msgs, map[string]interface{}{"role": "tool", "tool_call_id": callID, "content": output})
		case "function_call":
			var callID, name, arguments string
			_ = json.Unmarshal(item["call_id"], &callID)
			_ = json.Unmarshal(item["name"], &name)
			_ = json.Unmarshal(item["arguments"], &arguments)
			msgs = append(msgs, map[string]interface{}{
				"role":    "assistant",
				"content": "",
				"tool_calls": []map[string]interface{}{{
					"id":   callID,
					"type": "function",
					"function": map[string]string{
						"name":      name,
						"arguments": arguments,
					},
				}},
			})
		default:
			return nil, fmt.Errorf("responses input item type %q is not supported", itemType)
		}
	}
	return msgs, nil
}

func normalizeResponsesInputContent(raw json.RawMessage) interface{} {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []map[string]interface{}
	if json.Unmarshal(raw, &parts) == nil {
		for i := range parts {
			if t, ok := parts[i]["type"].(string); ok {
				switch t {
				case "input_text":
					parts[i]["type"] = "text"
				case "input_image":
					parts[i]["type"] = "image_url"
				}
			}
		}
		return parts
	}
	return raw
}

func convertToolsForChat(raw json.RawMessage) interface{} {
	var tools []map[string]interface{}
	if json.Unmarshal(raw, &tools) != nil {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		if t["type"] != "function" {
			continue
		}
		function := map[string]interface{}{}
		for _, key := range []string{"name", "description", "parameters", "strict"} {
			if v, ok := t[key]; ok {
				function[key] = v
			}
		}
		out = append(out, map[string]interface{}{"type": "function", "function": function})
	}
	return out
}

func writeResponsesJSONFromChat(w http.ResponseWriter, statusCode int, header http.Header, body []byte) error {
	copyHeaders(w, header, "Content-Length")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Content-Type", "application/json")
	if statusCode >= 400 {
		w.WriteHeader(statusCode)
		_, err := w.Write(body)
		return err
	}
	respBody, err := chatCompletionToResponses(body)
	if err != nil {
		return err
	}
	w.WriteHeader(statusCode)
	_, err = w.Write(respBody)
	return err
}

func writeResponsesSSEFromChat(w http.ResponseWriter, statusCode int, header http.Header, body []byte) error {
	copyHeaders(w, header, "Content-Length")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.Header().Set("Content-Type", "text/event-stream")
	if statusCode >= 400 {
		w.WriteHeader(statusCode)
		_, err := w.Write(body)
		return err
	}
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(chatCompletionStreamToResponses(body))
	return err
}

func chatCompletionToResponses(chatBody []byte) ([]byte, error) {
	var chatResp struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls,omitempty"`
			} `json:"message"`
		} `json:"choices"`
		Usage map[string]int `json:"usage,omitempty"`
	}
	if err := json.Unmarshal(chatBody, &chatResp); err != nil {
		return nil, fmt.Errorf("parse chat completion response: %w", err)
	}
	output := make([]map[string]interface{}, 0, 2)
	if len(chatResp.Choices) > 0 {
		msg := chatResp.Choices[0].Message
		if msg.Content != "" {
			output = append(output, map[string]interface{}{
				"type": "message",
				"id":   fmt.Sprintf("msg_%d", chatResp.Created),
				"role": "assistant",
				"content": []map[string]string{{
					"type": "output_text",
					"text": msg.Content,
				}},
			})
		}
		for _, tc := range msg.ToolCalls {
			output = append(output, map[string]interface{}{
				"type":      "function_call",
				"id":        tc.ID,
				"call_id":   tc.ID,
				"name":      tc.Function.Name,
				"arguments": tc.Function.Arguments,
			})
		}
	}
	resp := map[string]interface{}{
		"id":         chatResp.ID,
		"object":     "response",
		"created_at": chatResp.Created,
		"model":      chatResp.Model,
		"output":     output,
	}
	if chatResp.Usage != nil {
		resp["usage"] = map[string]int{
			"input_tokens":  chatResp.Usage["prompt_tokens"],
			"output_tokens": chatResp.Usage["completion_tokens"],
			"total_tokens":  chatResp.Usage["total_tokens"],
		}
	}
	return json.Marshal(resp)
}

func chatCompletionStreamToResponses(body []byte) []byte {
	text := string(body)
	if strings.TrimSpace(text) == "" {
		return body
	}
	lines := strings.Split(text, "\n")
	var out strings.Builder
	responseID := fmt.Sprintf("resp_%d", time.Now().UnixMilli())
	model := ""
	created := false
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			out.WriteString("event: response.completed\n")
			out.WriteString(`data: {"response":{"id":"` + responseID + `"}}` + "\n\n")
			out.WriteString("data: [DONE]\n\n")
			continue
		}
		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Role      string `json:"role,omitempty"`
					Content   string `json:"content,omitempty"`
					ToolCalls []struct {
						ID       string `json:"id,omitempty"`
						Index    int    `json:"index,omitempty"`
						Function struct {
							Name      string `json:"name,omitempty"`
							Arguments string `json:"arguments,omitempty"`
						} `json:"function,omitempty"`
					} `json:"tool_calls,omitempty"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		if chunk.ID != "" {
			responseID = chunk.ID
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if !created {
			created = true
			out.WriteString("event: response.created\n")
			out.WriteString(`data: {"response":{"id":"` + responseID + `","model":"` + model + `"}}` + "\n\n")
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			out.WriteString("event: response.output_text.delta\n")
			out.WriteString(`data: {"delta":` + strconvQuote(delta.Content) + `}` + "\n\n")
		}
		for _, tc := range delta.ToolCalls {
			if tc.Function.Name != "" {
				out.WriteString("event: response.output_item.added\n")
				out.WriteString(`data: {"item":{"id":` + strconvQuote(tc.ID) + `,"type":"function_call","call_id":` + strconvQuote(tc.ID) + `,"name":` + strconvQuote(tc.Function.Name) + `}}` + "\n\n")
			}
			if tc.Function.Arguments != "" {
				out.WriteString("event: response.function_call_arguments.delta\n")
				out.WriteString(`data: {"item_id":` + strconvQuote(tc.ID) + `,"call_id":` + strconvQuote(tc.ID) + `,"delta":` + strconvQuote(tc.Function.Arguments) + `}` + "\n\n")
			}
		}
	}
	return []byte(out.String())
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ---------------------------------------------------------------------------
// Request conversion helpers
// ---------------------------------------------------------------------------

// streamOptionsIncludeUsage returns true if stream_options.include_usage is
// set in the raw field map.  The field is a Chat Completions concept and must
// never be forwarded to upstream Responses API endpoints.
func streamOptionsIncludeUsage(raw map[string]json.RawMessage) bool {
	v, ok := raw["stream_options"]
	if !ok {
		return false
	}
	var so struct {
		IncludeUsage bool `json:"include_usage"`
	}
	return json.Unmarshal(v, &so) == nil && so.IncludeUsage
}

// normalizeContentParts rewrites Chat Completions content-part type names to
// the Responses API equivalents so the upstream never receives an unsupported
// "text" or "image_url" type value.
//
// Chat Completions → Responses API mapping:
//
//	"text"      → "input_text"
//	"image_url" → "input_image"
//
// Plain string content is returned unchanged.  Unknown part types are passed
// through without modification to preserve forward-compatibility.
func normalizeContentParts(content json.RawMessage) json.RawMessage {
	// Plain string content: nothing to rewrite.
	var s string
	if json.Unmarshal(content, &s) == nil {
		return content
	}
	// Array of content parts: rewrite Chat Completions type names.
	var parts []map[string]json.RawMessage
	if json.Unmarshal(content, &parts) != nil {
		return content
	}
	for i, part := range parts {
		typeRaw, ok := part["type"]
		if !ok {
			continue
		}
		var t string
		if json.Unmarshal(typeRaw, &t) != nil {
			continue
		}
		switch t {
		case "text":
			parts[i]["type"], _ = json.Marshal("input_text")
		case "image_url":
			parts[i]["type"], _ = json.Marshal("input_image")
		}
	}
	out, _ := json.Marshal(parts)
	return out
}

// convertToolsForResponses rewrites the Chat Completions "tools" array into the
// Responses API format.  In Chat Completions each tool wraps its definition
// inside a "function" object; the Responses API flattens it:
//
//	Chat Completions: {"type":"function","function":{"name":"f",…}}
//	Responses API:    {"type":"function","name":"f",…}
func convertToolsForResponses(raw json.RawMessage) json.RawMessage {
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description,omitempty"`
			Parameters  json.RawMessage `json:"parameters,omitempty"`
			Strict      *bool           `json:"strict,omitempty"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &tools) != nil {
		return raw
	}
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		rt := map[string]interface{}{
			"type": "function",
			"name": t.Function.Name,
		}
		if t.Function.Description != "" {
			rt["description"] = t.Function.Description
		}
		if t.Function.Parameters != nil {
			rt["parameters"] = t.Function.Parameters
		}
		if t.Function.Strict != nil {
			rt["strict"] = *t.Function.Strict
		}
		out = append(out, rt)
	}
	b, _ := json.Marshal(out)
	return b
}

// extractMessages splits the Chat Completions "messages" array into:
//   - instructions: system-role content joined with newlines
//   - inputJSON:    remaining messages as a JSON array, with content-part
//     types rewritten for Responses API compatibility (text→input_text, etc.)
func extractMessages(v json.RawMessage) (instructions string, inputJSON json.RawMessage, err error) {
	var msgs []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
	}
	if err = json.Unmarshal(v, &msgs); err != nil {
		return "", v, fmt.Errorf("parse messages: %w", err)
	}
	var instrParts []string
	var inputItems []json.RawMessage
	for _, m := range msgs {
		switch m.Role {
		case "system":
			// content may be a plain string or an array of parts.
			var text string
			if json.Unmarshal(m.Content, &text) == nil {
				instrParts = append(instrParts, text)
			} else {
				var parts []struct {
					Type string `json:"type"`
					Text string `json:"text,omitempty"`
				}
				if json.Unmarshal(m.Content, &parts) == nil {
					for _, p := range parts {
						if p.Text != "" {
							instrParts = append(instrParts, p.Text)
						}
					}
				}
			}

		case "tool":
			// Chat Completions tool result → Responses API function_call_output
			var contentStr string
			if json.Unmarshal(m.Content, &contentStr) != nil {
				// If content is not a plain string, marshal it as-is.
				contentStr = string(m.Content)
			}
			item := map[string]string{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  contentStr,
			}
			b, _ := json.Marshal(item)
			inputItems = append(inputItems, b)

		case "assistant":
			// Check if this assistant message has tool_calls.
			if len(m.ToolCalls) > 0 {
				// First emit the assistant message text (if any).
				if len(m.Content) > 0 && string(m.Content) != `""` && string(m.Content) != "null" {
					msg := map[string]json.RawMessage{
						"role":    json.RawMessage(`"assistant"`),
						"content": normalizeContentParts(m.Content),
					}
					b, _ := json.Marshal(msg)
					inputItems = append(inputItems, b)
				}
				// Convert each tool_call to a function_call input item.
				var toolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				}
				if json.Unmarshal(m.ToolCalls, &toolCalls) == nil {
					for _, tc := range toolCalls {
						item := map[string]string{
							"type":      "function_call",
							"call_id":   tc.ID,
							"name":      tc.Function.Name,
							"arguments": tc.Function.Arguments,
						}
						b, _ := json.Marshal(item)
						inputItems = append(inputItems, b)
					}
				}
			} else {
				// Normal assistant message without tool calls.
				msg := map[string]json.RawMessage{
					"role":    json.RawMessage(`"assistant"`),
					"content": normalizeContentParts(m.Content),
				}
				b, _ := json.Marshal(msg)
				inputItems = append(inputItems, b)
			}

		default:
			// user, developer, etc. — pass through with normalized content.
			roleJSON, _ := json.Marshal(m.Role)
			msg := map[string]json.RawMessage{
				"role":    roleJSON,
				"content": normalizeContentParts(m.Content),
			}
			b, _ := json.Marshal(msg)
			inputItems = append(inputItems, b)
		}
	}
	if inputItems == nil {
		inputItems = []json.RawMessage{}
	}
	// Build a JSON array from the items.
	result := []byte("[")
	for i, item := range inputItems {
		if i > 0 {
			result = append(result, ',')
		}
		result = append(result, item...)
	}
	result = append(result, ']')
	return strings.Join(instrParts, "\n"), result, nil
}

// ---------------------------------------------------------------------------
// Request conversion:  Chat Completions → transport-specific Responses body
// ---------------------------------------------------------------------------

// chatGPTCodexAllowedKeys is the strict allowlist of fields accepted by
// chatgpt.com/backend-api/codex/responses.  Anything not in this set is
// silently dropped before the request leaves the proxy.
var chatGPTCodexAllowedKeys = map[string]bool{
	"model":        true,
	"input":        true,
	"instructions": true,
	"tools":        true,
	"stream":       true,
	"store":        true,
	"temperature":  true,
	"top_p":        true,
	"user":         true,
}

// buildChatGPTCodexRequest converts an OpenAI Chat Completions body into the
// request format required by chatgpt.com/backend-api/codex/responses.
//
// Differences from the Platform Responses API:
//   - strict allowlist: only chatGPTCodexAllowedKeys are forwarded
//   - stream is always forced to true  (endpoint requirement)
//   - store  is always forced to false (endpoint requirement)
//   - stream_options is consumed locally; include_usage is returned
//   - max_tokens/max_output_tokens are dropped (unsupported)
//
// Returns the serialised request body and whether the client requested usage
// in streaming chunks (stream_options.include_usage).
func buildChatGPTCodexRequest(chatBody []byte) ([]byte, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(chatBody, &raw); err != nil {
		return nil, false, fmt.Errorf("unmarshal chat body: %w", err)
	}
	includeUsage := streamOptionsIncludeUsage(raw)

	out := make(map[string]json.RawMessage, len(chatGPTCodexAllowedKeys))

	// Extract messages → instructions + input.
	if v, ok := raw["messages"]; ok {
		instr, inputJSON, err := extractMessages(v)
		if err != nil {
			out["input"] = v // fallback
		} else {
			instrJSON, _ := json.Marshal(instr)
			out["instructions"] = instrJSON
			out["input"] = inputJSON
		}
	}
	if _, ok := out["instructions"]; !ok {
		out["instructions"], _ = json.Marshal("")
	}

	// Safe pass-through fields from the allowlist (excluding messages already handled).
	for _, k := range []string{"model", "temperature", "top_p", "user"} {
		if v, ok := raw[k]; ok {
			out[k] = v
		}
	}

	// Convert Chat Completions tools format to Responses API format.
	if v, ok := raw["tools"]; ok {
		out["tools"] = convertToolsForResponses(v)
	}

	// Endpoint requirements.
	out["stream"], _ = json.Marshal(true)
	out["store"], _ = json.Marshal(false)

	b, err := json.Marshal(out)
	return b, includeUsage, err
}

func buildChatGPTCodexRequestFromResponses(respBody []byte) ([]byte, bool, error) {
	normalized, req, err := normalizeResponsesRequestForCodex(respBody)
	if err != nil {
		return nil, false, err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &raw); err != nil {
		return nil, false, fmt.Errorf("unmarshal normalized responses body: %w", err)
	}

	out := make(map[string]json.RawMessage, len(chatGPTCodexAllowedKeys))
	for _, k := range []string{"model", "input", "instructions", "temperature", "top_p", "user", "tools"} {
		if v, ok := raw[k]; ok {
			out[k] = v
		}
	}
	out["stream"], _ = json.Marshal(req.Stream)
	out["store"], _ = json.Marshal(false)
	if _, ok := out["instructions"]; !ok {
		out["instructions"], _ = json.Marshal("")
	}
	b, err := json.Marshal(out)
	return b, req.Stream, err
}

// buildPlatformResponsesRequest converts an OpenAI Chat Completions body into
// the generic Responses API format for api.openai.com/v1/responses.
//
//   - messages (system)     → instructions
//   - messages (non-system) → input
//   - max_tokens            → max_output_tokens
//   - stream_options        → consumed locally; include_usage returned
//   - n, stop, etc.         → dropped
//   - all other fields      → passed through
func buildPlatformResponsesRequest(chatBody []byte) ([]byte, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(chatBody, &raw); err != nil {
		return nil, false, fmt.Errorf("unmarshal chat body: %w", err)
	}
	includeUsage := streamOptionsIncludeUsage(raw)

	out := make(map[string]json.RawMessage, len(raw)+2)

	for k, v := range raw {
		switch k {
		case "messages":
			instr, inputJSON, err := extractMessages(v)
			if err != nil {
				out["input"] = v // fallback
			} else {
				instrJSON, _ := json.Marshal(instr)
				out["instructions"] = instrJSON
				out["input"] = inputJSON
			}
		case "max_tokens", "max_completion_tokens":
			out["max_output_tokens"] = v
		case "stream_options":
			// Consumed locally — never forwarded upstream.
		case "tools":
			out["tools"] = convertToolsForResponses(v)
		case "n", "stop", "logprobs", "top_logprobs", "logit_bias",
			"frequency_penalty", "presence_penalty", "seed",
			"response_format", "tool_choice",
			"parallel_tool_calls", "function_call", "functions":
			// Drop fields unsupported by Responses API.
		default:
			out[k] = v
		}
	}

	if _, ok := out["instructions"]; !ok {
		out["instructions"], _ = json.Marshal("")
	}

	b, err := json.Marshal(out)
	return b, includeUsage, err
}

// ---------------------------------------------------------------------------
// Non-streaming response conversion:  Responses API → Chat Completions
// ---------------------------------------------------------------------------

// responsesAPIOutput is a single output item from the Responses API.
type responsesAPIOutput struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Role      string `json:"role"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Content   []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// responsesAPIResult is the top-level Responses API JSON body.
type responsesAPIResult struct {
	ID        string               `json:"id"`
	Object    string               `json:"object"`
	CreatedAt float64              `json:"created_at"`
	Model     string               `json:"model"`
	Output    []responsesAPIOutput `json:"output"`
	Usage     *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// responsesToChatCompletion converts a non-streaming Responses API body
// into the Chat Completions format expected by proxy clients.
func responsesToChatCompletion(respBody []byte) ([]byte, error) {
	var rr responsesAPIResult
	if err := json.Unmarshal(respBody, &rr); err != nil {
		return nil, fmt.Errorf("unmarshal responses body: %w", err)
	}

	// If the Responses API itself returned an error object, pass through.
	if rr.Error != nil {
		errResp := map[string]interface{}{
			"error": map[string]interface{}{
				"message": rr.Error.Message,
				"type":    rr.Error.Type,
				"code":    rr.Error.Code,
			},
		}
		return json.Marshal(errResp)
	}

	// Extract assistant text and tool calls from output items.
	var text strings.Builder
	var toolCalls []map[string]interface{}
	for _, item := range rr.Output {
		switch item.Type {
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
		case "function_call":
			toolCalls = append(toolCalls, map[string]interface{}{
				"id":   item.CallID,
				"type": "function",
				"function": map[string]string{
					"name":      item.Name,
					"arguments": item.Arguments,
				},
			})
		}
	}

	finishReason := "stop"
	msg := map[string]interface{}{
		"role":    "assistant",
		"content": text.String(),
	}
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		msg["tool_calls"] = toolCalls
	}

	cc := map[string]interface{}{
		"id":      rr.ID,
		"object":  "chat.completion",
		"created": int64(rr.CreatedAt),
		"model":   rr.Model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"message":       msg,
				"finish_reason": finishReason,
			},
		},
	}
	if rr.Usage != nil {
		cc["usage"] = map[string]int{
			"prompt_tokens":     rr.Usage.InputTokens,
			"completion_tokens": rr.Usage.OutputTokens,
			"total_tokens":      rr.Usage.TotalTokens,
		}
	}
	return json.Marshal(cc)
}

// ---------------------------------------------------------------------------
// Streaming conversion:  Responses API SSE → Chat Completions SSE
// ---------------------------------------------------------------------------

// streamResponsesAsChat reads a Responses API SSE stream and writes
// Chat Completions–compatible SSE chunks to w.
// includeUsage controls whether usage is appended to the final chunk;
// it reflects stream_options.include_usage from the original client request.
func streamResponsesAsChat(w http.ResponseWriter, resp *http.Response, includeUsage bool) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("ResponseWriter does not support Flusher")
	}

	// Set headers for SSE.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.WriteHeader(http.StatusOK)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)

	var (
		chatID          = fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli())
		model           string
		created         = time.Now().Unix()
		sentRole        bool
		eventType       string
		hadToolCalls    bool
		toolCallCount   int
		toolCallIndices = make(map[string]int) // call_id → index
	)

	for scanner.Scan() {
		line := scanner.Text()

		// SSE event type line.
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		// SSE data line.
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "response.created":
			// Extract model from the response.created event.
			var ev struct {
				Response struct {
					ID    string `json:"id"`
					Model string `json:"model"`
				} `json:"response"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil {
				if ev.Response.Model != "" {
					model = ev.Response.Model
				}
				if ev.Response.ID != "" {
					chatID = ev.Response.ID
				}
			}

		case "response.output_text.delta":
			var ev struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				log.Printf("[responses_adapter] cannot parse text delta: %v", err)
				continue
			}

			// First chunk: send role.
			if !sentRole {
				sentRole = true
				chunk := chatChunk(chatID, model, created, map[string]string{"role": "assistant"}, nil)
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				flusher.Flush()
			}

			chunk := chatChunk(chatID, model, created, map[string]string{"content": ev.Delta}, nil)
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()

		case "response.output_item.added":
			var ev struct {
				Item struct {
					ID     string `json:"id"`
					Type   string `json:"type"`
					CallID string `json:"call_id"`
					Name   string `json:"name"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil || ev.Item.Type != "function_call" {
				continue
			}
			hadToolCalls = true
			idx := toolCallCount
			toolCallCount++
			toolCallIndices[ev.Item.CallID] = idx
			if ev.Item.ID != "" {
				toolCallIndices[ev.Item.ID] = idx
			}

			// Build delta with role (on first chunk) + tool_calls.
			delta := map[string]interface{}{
				"tool_calls": []map[string]interface{}{
					{
						"index": idx,
						"id":    ev.Item.CallID,
						"type":  "function",
						"function": map[string]string{
							"name":      ev.Item.Name,
							"arguments": "",
						},
					},
				},
			}
			if !sentRole {
				sentRole = true
				delta["role"] = "assistant"
			}
			chunk := chatChunkDynamic(chatID, model, created, delta, nil)
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()

		case "response.function_call_arguments.delta":
			var ev struct {
				Delta  string `json:"delta"`
				ItemID string `json:"item_id"`
				CallID string `json:"call_id"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			// Responses API uses item_id in delta events; fall back to call_id.
			lookupKey := ev.ItemID
			if lookupKey == "" {
				lookupKey = ev.CallID
			}
			idx, ok := toolCallIndices[lookupKey]
			if !ok {
				continue
			}
			delta := map[string]interface{}{
				"tool_calls": []map[string]interface{}{
					{
						"index": idx,
						"function": map[string]string{
							"arguments": ev.Delta,
						},
					},
				},
			}
			chunk := chatChunkDynamic(chatID, model, created, delta, nil)
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()

		case "response.completed":
			// Extract usage from the completed event; only include it in the
			// final chunk when the client requested it via
			// stream_options.include_usage.
			var ev struct {
				Response struct {
					Usage *struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
						TotalTokens  int `json:"total_tokens"`
					} `json:"usage"`
				} `json:"response"`
			}
			var usage map[string]int
			if includeUsage && json.Unmarshal([]byte(data), &ev) == nil && ev.Response.Usage != nil {
				usage = map[string]int{
					"prompt_tokens":     ev.Response.Usage.InputTokens,
					"completion_tokens": ev.Response.Usage.OutputTokens,
					"total_tokens":      ev.Response.Usage.TotalTokens,
				}
			}
			// Send finish_reason chunk.
			finish := "stop"
			if hadToolCalls {
				finish = "tool_calls"
			}
			chunk := chatChunkFinish(chatID, model, created, &finish, usage)
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()

			// Send [DONE].
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return nil

		case "error":
			// Forward error as an SSE error event.
			log.Printf("[responses_adapter] upstream error event: %s", data)
			// Convert to chat completion error chunk.
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading responses SSE stream: %w", err)
	}
	return nil
}

// chatChunk builds a Chat Completions streaming chunk JSON.
func chatChunk(id, model string, created int64, delta map[string]string, finishReason *string) []byte {
	chunk := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return b
}

// chatChunkDynamic builds a streaming chunk with an arbitrary delta object
// (used for tool_calls and mixed deltas that contain non-string fields).
func chatChunkDynamic(id, model string, created int64, delta map[string]interface{}, finishReason *string) []byte {
	chunk := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"delta":         delta,
				"finish_reason": finishReason,
			},
		},
	}
	b, _ := json.Marshal(chunk)
	return b
}

// chatChunkFinish builds the final streaming chunk with finish_reason and optional usage.
func chatChunkFinish(id, model string, created int64, finishReason *string, usage map[string]int) []byte {
	chunk := map[string]interface{}{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"delta":         map[string]string{},
				"finish_reason": finishReason,
			},
		},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	b, _ := json.Marshal(chunk)
	return b
}

// isStreamingRequest checks if the request body has "stream":true.
func isStreamingRequest(body []byte) bool {
	var req struct {
		Stream interface{} `json:"stream"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	switch v := req.Stream.(type) {
	case bool:
		return v
	default:
		return false
	}
}

// aggregateSSEToCompletion reads a Responses API SSE stream and returns
// the response JSON from the response.completed event, suitable for
// passing to responsesToChatCompletion.
func aggregateSSEToCompletion(r io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)

	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		switch eventType {
		case "response.completed":
			var ev struct {
				Response json.RawMessage `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				return nil, fmt.Errorf("parse response.completed: %w", err)
			}
			return ev.Response, nil
		case "error":
			return nil, fmt.Errorf("upstream SSE error: %s", data)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading responses SSE: %w", err)
	}
	return nil, fmt.Errorf("no response.completed event in SSE stream")
}

// handleResponsesAPIResponse processes a Responses API HTTP response and
// writes the translated Chat Completions output to w.
//
//   - clientWantsStream: true when the original client requested SSE streaming
//   - upstreamSSE: true when the upstream was forced to stream (chatgpt.com
//     mode); the endpoint may not set Content-Type: text/event-stream
//   - includeUsage: true when the client requested usage via
//     stream_options.include_usage (streaming only)
func handleResponsesAPIResponse(w http.ResponseWriter, resp *http.Response, clientWantsStream, upstreamSSE, includeUsage bool) error {
	ct := resp.Header.Get("Content-Type")
	isSSE := strings.Contains(ct, "text/event-stream") || upstreamSSE

	if resp.StatusCode >= 400 {
		// Error: read body and forward as-is, but also return an error
		// so the proxy layer can log the real upstream status.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Printf("[codex] upstream %d response: %s", resp.StatusCode, string(body))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
		return fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}

	if isSSE {
		if !clientWantsStream {
			// Client requested a blocking response but upstream was forced to
			// stream (chatgpt.com mode).  Aggregate the SSE into a single
			// Chat Completions response object.
			respJSON, err := aggregateSSEToCompletion(resp.Body)
			if err != nil {
				return fmt.Errorf("aggregate SSE: %w", err)
			}
			chatBody, err := responsesToChatCompletion(respJSON)
			if err != nil {
				log.Printf("[responses_adapter] SSE aggregate conversion failed: %v — forwarding raw", err)
				chatBody = respJSON
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			w.WriteHeader(http.StatusOK)
			_, writeErr := io.Copy(w, bytes.NewReader(chatBody))
			return writeErr
		}
		return streamResponsesAsChat(w, resp, includeUsage)
	}

	// Non-streaming: read full body, convert, write.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading responses body: %w", err)
	}

	chatBody, err := responsesToChatCompletion(body)
	if err != nil {
		log.Printf("[responses_adapter] conversion failed: %v — forwarding raw", err)
		chatBody = body
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	w.WriteHeader(resp.StatusCode)
	_, writeErr := io.Copy(w, bytes.NewReader(chatBody))
	return writeErr
}
