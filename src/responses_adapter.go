// responses_adapter.go converts between OpenAI Chat Completions and Responses
// formats while preserving structured output and tool-call semantics.
package yarouter

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

func streamOptionsIncludeUsage(raw map[string]json.RawMessage) bool {
	value, ok := raw["stream_options"]
	if !ok {
		return false
	}
	var options struct {
		IncludeUsage bool `json:"include_usage"`
	}
	return json.Unmarshal(value, &options) == nil && options.IncludeUsage
}

func normalizeContentParts(content json.RawMessage) json.RawMessage {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return content
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(content, &parts) != nil {
		return content
	}
	for index, part := range parts {
		typeValue, ok := part["type"]
		if !ok {
			continue
		}
		var partType string
		if json.Unmarshal(typeValue, &partType) != nil {
			continue
		}
		switch partType {
		case "text":
			parts[index]["type"], _ = json.Marshal("input_text")
		case "image_url":
			parts[index]["type"], _ = json.Marshal("input_image")
		}
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return content
	}
	return encoded
}

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
	converted := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "" && tool.Type != "function" {
			continue
		}
		item := map[string]interface{}{
			"type": "function",
			"name": tool.Function.Name,
		}
		if tool.Function.Description != "" {
			item["description"] = tool.Function.Description
		}
		if len(tool.Function.Parameters) > 0 {
			item["parameters"] = tool.Function.Parameters
		}
		if tool.Function.Strict != nil {
			item["strict"] = *tool.Function.Strict
		}
		converted = append(converted, item)
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		return raw
	}
	return encoded
}

func extractMessages(value json.RawMessage) (instructions string, inputJSON json.RawMessage, err error) {
	var messages []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal(value, &messages); err != nil {
		return "", nil, fmt.Errorf("parse messages: %w", err)
	}

	var instructionParts []string
	var inputItems []json.RawMessage
	for _, message := range messages {
		switch message.Role {
		case "system":
			var text string
			if json.Unmarshal(message.Content, &text) == nil {
				instructionParts = append(instructionParts, text)
				continue
			}
			var parts []struct {
				Text string `json:"text,omitempty"`
			}
			if json.Unmarshal(message.Content, &parts) == nil {
				for _, part := range parts {
					if part.Text != "" {
						instructionParts = append(instructionParts, part.Text)
					}
				}
			}
		case "tool":
			var output string
			if json.Unmarshal(message.Content, &output) != nil {
				output = string(message.Content)
			}
			item, _ := json.Marshal(map[string]string{
				"type":    "function_call_output",
				"call_id": message.ToolCallID,
				"output":  output,
			})
			inputItems = append(inputItems, item)
		case "assistant":
			if len(message.ToolCalls) > 0 && string(message.ToolCalls) != "null" {
				if len(message.Content) > 0 && string(message.Content) != `""` && string(message.Content) != "null" {
					item, _ := json.Marshal(map[string]json.RawMessage{
						"role":    json.RawMessage(`"assistant"`),
						"content": normalizeContentParts(message.Content),
					})
					inputItems = append(inputItems, item)
				}
				var toolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				}
				if json.Unmarshal(message.ToolCalls, &toolCalls) != nil {
					return "", nil, fmt.Errorf("parse assistant tool_calls")
				}
				for _, toolCall := range toolCalls {
					item, _ := json.Marshal(map[string]string{
						"type":      "function_call",
						"call_id":   toolCall.ID,
						"name":      toolCall.Function.Name,
						"arguments": toolCall.Function.Arguments,
					})
					inputItems = append(inputItems, item)
				}
				continue
			}
			fallthrough
		default:
			roleJSON, _ := json.Marshal(message.Role)
			item, _ := json.Marshal(map[string]json.RawMessage{
				"role":    roleJSON,
				"content": normalizeContentParts(message.Content),
			})
			inputItems = append(inputItems, item)
		}
	}
	if inputItems == nil {
		inputItems = []json.RawMessage{}
	}
	encoded, err := json.Marshal(inputItems)
	if err != nil {
		return "", nil, err
	}
	return strings.Join(instructionParts, "\n"), encoded, nil
}

func responseFormatToResponsesText(raw json.RawMessage) (json.RawMessage, error) {
	var envelope struct {
		Type       string          `json:"type"`
		JSONSchema json.RawMessage `json:"json_schema"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("parse response_format: %w", err)
	}
	var format map[string]json.RawMessage
	switch envelope.Type {
	case "json_schema":
		if len(envelope.JSONSchema) == 0 || string(envelope.JSONSchema) == "null" {
			return nil, fmt.Errorf("response_format.json_schema is required")
		}
		if err := json.Unmarshal(envelope.JSONSchema, &format); err != nil {
			return nil, fmt.Errorf("parse response_format.json_schema: %w", err)
		}
		format["type"], _ = json.Marshal("json_schema")
	case "json_object":
		format = map[string]json.RawMessage{"type": json.RawMessage(`"json_object"`)}
	case "text", "":
		format = map[string]json.RawMessage{"type": json.RawMessage(`"text"`)}
	default:
		return nil, fmt.Errorf("unsupported response_format type %q", envelope.Type)
	}
	formatJSON, err := json.Marshal(format)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]json.RawMessage{"format": formatJSON})
}

var chatGPTCodexAllowedKeys = map[string]bool{
	"model":               true,
	"input":               true,
	"instructions":        true,
	"tools":               true,
	"tool_choice":         true,
	"parallel_tool_calls": true,
	"text":                true,
	"reasoning":           true,
	"metadata":            true,
	"stream":              true,
	"store":               true,
	"temperature":         true,
	"top_p":               true,
	"user":                true,
}

func buildChatGPTCodexRequest(chatBody []byte) ([]byte, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(chatBody, &raw); err != nil {
		return nil, false, fmt.Errorf("unmarshal chat body: %w", err)
	}
	includeUsage := streamOptionsIncludeUsage(raw)
	out := make(map[string]json.RawMessage, len(chatGPTCodexAllowedKeys))

	if messages, ok := raw["messages"]; ok {
		instructions, input, err := extractMessages(messages)
		if err != nil {
			return nil, false, err
		}
		out["instructions"], _ = json.Marshal(instructions)
		out["input"] = input
	}
	if _, ok := out["instructions"]; !ok {
		out["instructions"], _ = json.Marshal("")
	}
	for _, key := range []string{
		"model", "temperature", "top_p", "user", "tool_choice",
		"parallel_tool_calls", "reasoning", "metadata",
	} {
		if value, ok := raw[key]; ok {
			out[key] = value
		}
	}
	if tools, ok := raw["tools"]; ok {
		out["tools"] = convertToolsForResponses(tools)
	}
	if responseFormat, ok := raw["response_format"]; ok {
		textFormat, err := responseFormatToResponsesText(responseFormat)
		if err != nil {
			return nil, false, err
		}
		out["text"] = textFormat
	}
	for _, unsupported := range []string{
		"n", "stop", "logprobs", "top_logprobs", "logit_bias",
		"frequency_penalty", "presence_penalty", "seed",
		"function_call", "functions",
	} {
		if _, present := raw[unsupported]; present {
			return nil, false, fmt.Errorf("field %q is not supported by the ChatGPT Codex transport", unsupported)
		}
	}
	out["stream"], _ = json.Marshal(true)
	out["store"], _ = json.Marshal(false)
	encoded, err := json.Marshal(out)
	return encoded, includeUsage, err
}

func buildPlatformResponsesRequest(chatBody []byte) ([]byte, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(chatBody, &raw); err != nil {
		return nil, false, fmt.Errorf("unmarshal chat body: %w", err)
	}
	includeUsage := streamOptionsIncludeUsage(raw)
	out := make(map[string]json.RawMessage, len(raw)+2)
	for key, value := range raw {
		switch key {
		case "messages":
			instructions, input, err := extractMessages(value)
			if err != nil {
				return nil, false, err
			}
			out["instructions"], _ = json.Marshal(instructions)
			out["input"] = input
		case "max_tokens", "max_completion_tokens":
			out["max_output_tokens"] = value
		case "stream_options":
			// Consumed locally.
		case "tools":
			out["tools"] = convertToolsForResponses(value)
		case "response_format":
			textFormat, err := responseFormatToResponsesText(value)
			if err != nil {
				return nil, false, err
			}
			out["text"] = textFormat
		case "n", "stop", "logprobs", "top_logprobs", "logit_bias",
			"frequency_penalty", "presence_penalty", "seed", "function_call", "functions":
			return nil, false, fmt.Errorf("field %q is not supported by the Responses API adapter", key)
		default:
			out[key] = value
		}
	}
	if _, ok := out["instructions"]; !ok {
		out["instructions"], _ = json.Marshal("")
	}
	encoded, err := json.Marshal(out)
	return encoded, includeUsage, err
}

func buildChatGPTNativeResponsesRequest(body []byte) ([]byte, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("unmarshal Responses body: %w", err)
	}
	clientWantsStream := false
	if value, ok := raw["stream"]; ok {
		_ = json.Unmarshal(value, &clientWantsStream)
	}
	out := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		if !chatGPTCodexAllowedKeys[key] {
			return nil, false, fmt.Errorf("field %q is not supported by the ChatGPT Codex transport", key)
		}
		out[key] = value
	}
	out["stream"], _ = json.Marshal(true)
	out["store"], _ = json.Marshal(false)
	encoded, err := json.Marshal(out)
	return encoded, clientWantsStream, err
}

func buildPlatformNativeResponsesRequest(body []byte) ([]byte, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, fmt.Errorf("unmarshal Responses body: %w", err)
	}
	clientWantsStream := false
	if value, ok := raw["stream"]; ok {
		_ = json.Unmarshal(value, &clientWantsStream)
	}
	delete(raw, "stream_options")
	encoded, err := json.Marshal(raw)
	return encoded, clientWantsStream, err
}

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

func responsesToChatCompletion(respBody []byte) ([]byte, error) {
	var response responsesAPIResult
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("unmarshal Responses body: %w", err)
	}
	if response.Error != nil {
		return json.Marshal(map[string]interface{}{
			"error": map[string]interface{}{
				"message": response.Error.Message,
				"type":    response.Error.Type,
				"code":    response.Error.Code,
			},
		})
	}
	var text strings.Builder
	var toolCalls []map[string]interface{}
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				if content.Type == "output_text" {
					text.WriteString(content.Text)
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
	message := map[string]interface{}{"role": "assistant", "content": text.String()}
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		message["tool_calls"] = toolCalls
	}
	completion := map[string]interface{}{
		"id":      response.ID,
		"object":  "chat.completion",
		"created": int64(response.CreatedAt),
		"model":   response.Model,
		"choices": []map[string]interface{}{{
			"index":         0,
			"message":       message,
			"finish_reason": finishReason,
		}},
	}
	if response.Usage != nil {
		completion["usage"] = map[string]int{
			"prompt_tokens":     response.Usage.InputTokens,
			"completion_tokens": response.Usage.OutputTokens,
			"total_tokens":      response.Usage.TotalTokens,
		}
	}
	return json.Marshal(completion)
}

func streamResponsesAsChat(w http.ResponseWriter, response *http.Response, includeUsage bool) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("ResponseWriter does not support Flusher")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	chatID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli())
	model := ""
	created := time.Now().Unix()
	sentRole := false
	eventType := ""
	hadToolCalls := false
	toolCallCount := 0
	toolCallIndices := make(map[string]int)

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
		case "response.created":
			var event struct {
				Response struct {
					ID    string `json:"id"`
					Model string `json:"model"`
				} `json:"response"`
			}
			if json.Unmarshal([]byte(data), &event) == nil {
				if event.Response.Model != "" {
					model = event.Response.Model
				}
				if event.Response.ID != "" {
					chatID = event.Response.ID
				}
			}
		case "response.output_text.delta":
			var event struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &event) != nil {
				continue
			}
			if !sentRole {
				sentRole = true
				fmt.Fprintf(w, "data: %s\n\n", chatChunk(chatID, model, created, map[string]string{"role": "assistant"}, nil))
				flusher.Flush()
			}
			fmt.Fprintf(w, "data: %s\n\n", chatChunk(chatID, model, created, map[string]string{"content": event.Delta}, nil))
			flusher.Flush()
		case "response.output_item.added":
			var event struct {
				Item struct {
					ID     string `json:"id"`
					Type   string `json:"type"`
					CallID string `json:"call_id"`
					Name   string `json:"name"`
				} `json:"item"`
			}
			if json.Unmarshal([]byte(data), &event) != nil || event.Item.Type != "function_call" {
				continue
			}
			hadToolCalls = true
			index := toolCallCount
			toolCallCount++
			if event.Item.CallID != "" {
				toolCallIndices[event.Item.CallID] = index
			}
			if event.Item.ID != "" {
				toolCallIndices[event.Item.ID] = index
			}
			delta := map[string]interface{}{
				"tool_calls": []map[string]interface{}{{
					"index": index,
					"id":    event.Item.CallID,
					"type":  "function",
					"function": map[string]string{
						"name":      event.Item.Name,
						"arguments": "",
					},
				}},
			}
			if !sentRole {
				sentRole = true
				delta["role"] = "assistant"
			}
			fmt.Fprintf(w, "data: %s\n\n", chatChunkDynamic(chatID, model, created, delta, nil))
			flusher.Flush()
		case "response.function_call_arguments.delta":
			var event struct {
				Delta  string `json:"delta"`
				ItemID string `json:"item_id"`
				CallID string `json:"call_id"`
			}
			if json.Unmarshal([]byte(data), &event) != nil {
				continue
			}
			key := event.ItemID
			if key == "" {
				key = event.CallID
			}
			index, ok := toolCallIndices[key]
			if !ok {
				continue
			}
			delta := map[string]interface{}{
				"tool_calls": []map[string]interface{}{{
					"index":    index,
					"function": map[string]string{"arguments": event.Delta},
				}},
			}
			fmt.Fprintf(w, "data: %s\n\n", chatChunkDynamic(chatID, model, created, delta, nil))
			flusher.Flush()
		case "response.completed":
			var event struct {
				Response struct {
					Usage *struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
						TotalTokens  int `json:"total_tokens"`
					} `json:"usage"`
				} `json:"response"`
			}
			var usage map[string]int
			if includeUsage && json.Unmarshal([]byte(data), &event) == nil && event.Response.Usage != nil {
				usage = map[string]int{
					"prompt_tokens":     event.Response.Usage.InputTokens,
					"completion_tokens": event.Response.Usage.OutputTokens,
					"total_tokens":      event.Response.Usage.TotalTokens,
				}
			}
			finish := "stop"
			if hadToolCalls {
				finish = "tool_calls"
			}
			fmt.Fprintf(w, "data: %s\n\n", chatChunkFinish(chatID, model, created, &finish, usage))
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return nil
		case "response.failed", "response.incomplete", "error":
			log.Printf("[responses_adapter] upstream SSE failure event=%s", eventType)
			fmt.Fprintf(w, "data: %s\n\n", data)
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			return fmt.Errorf("upstream SSE failure: %s", eventType)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading Responses SSE stream: %w", err)
	}
	return fmt.Errorf("Responses SSE stream ended without response.completed")
}

func chatChunk(id, model string, created int64, delta map[string]string, finishReason *string) []byte {
	body, _ := json.Marshal(map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]interface{}{{"index": 0, "delta": delta, "finish_reason": finishReason}},
	})
	return body
}

func chatChunkDynamic(id, model string, created int64, delta map[string]interface{}, finishReason *string) []byte {
	body, _ := json.Marshal(map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]interface{}{{"index": 0, "delta": delta, "finish_reason": finishReason}},
	})
	return body
}

func chatChunkFinish(id, model string, created int64, finishReason *string, usage map[string]int) []byte {
	chunk := map[string]interface{}{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": model,
		"choices": []map[string]interface{}{{"index": 0, "delta": map[string]string{}, "finish_reason": finishReason}},
	}
	if usage != nil {
		chunk["usage"] = usage
	}
	body, _ := json.Marshal(chunk)
	return body
}

func isStreamingRequest(body []byte) bool {
	var request struct {
		Stream bool `json:"stream"`
	}
	return json.Unmarshal(body, &request) == nil && request.Stream
}

func aggregateSSEToCompletion(reader io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	eventType := ""
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
			var event struct {
				Response json.RawMessage `json:"response"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				return nil, fmt.Errorf("parse response.completed: %w", err)
			}
			return event.Response, nil
		case "response.failed", "response.incomplete", "error":
			return nil, fmt.Errorf("upstream SSE failure: %s", eventType)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading Responses SSE: %w", err)
	}
	return nil, fmt.Errorf("no response.completed event in SSE stream")
}

func classifyUpstreamStatus(status int) ProviderErrorKind {
	switch status {
	case http.StatusUnauthorized:
		return ProviderErrorAuthRequired
	case http.StatusForbidden:
		return ProviderErrorEntitlement
	case http.StatusTooManyRequests:
		return ProviderErrorRateLimit
	default:
		if status >= 500 {
			return ProviderErrorUnavailable
		}
		return ProviderErrorInvalidRequest
	}
}

func handleResponsesAPIResponse(w http.ResponseWriter, response *http.Response, clientWantsStream, upstreamSSE, includeUsage bool) error {
	defer response.Body.Close()
	contentType := response.Header.Get("Content-Type")
	isSSE := strings.Contains(contentType, "text/event-stream") || upstreamSSE
	if response.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
		return newProviderError(ProviderCodex, classifyUpstreamStatus(response.StatusCode), response.StatusCode, response.StatusCode >= 500, "upstream HTTP %d", response.StatusCode)
	}
	if isSSE {
		if !clientWantsStream {
			responseJSON, err := aggregateSSEToCompletion(response.Body)
			if err != nil {
				return err
			}
			chatBody, err := responsesToChatCompletion(responseJSON)
			if err != nil {
				return err
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, err = io.Copy(w, bytes.NewReader(chatBody))
			return err
		}
		return streamResponsesAsChat(w, response, includeUsage)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	chatBody, err := responsesToChatCompletion(body)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, err = w.Write(chatBody)
	return err
}

func handleNativeResponsesAPIResponse(w http.ResponseWriter, response *http.Response, clientWantsStream, upstreamSSE bool) error {
	defer response.Body.Close()
	contentType := response.Header.Get("Content-Type")
	isSSE := strings.Contains(contentType, "text/event-stream") || upstreamSSE
	if response.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
		return newProviderError(ProviderCodex, classifyUpstreamStatus(response.StatusCode), response.StatusCode, response.StatusCode >= 500, "upstream HTTP %d", response.StatusCode)
	}
	if isSSE && !clientWantsStream {
		responseJSON, err := aggregateSSEToCompletion(response.Body)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(responseJSON)
		return err
	}
	if isSSE {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return fmt.Errorf("ResponseWriter does not support Flusher")
		}
		buffer := make([]byte, 32*1024)
		for {
			n, err := response.Body.Read(buffer)
			if n > 0 {
				if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
					return writeErr
				}
				flusher.Flush()
			}
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
	copyHeaders(w, response.Header, "Content-Length")
	w.WriteHeader(response.StatusCode)
	_, err := io.Copy(w, response.Body)
	return err
}
