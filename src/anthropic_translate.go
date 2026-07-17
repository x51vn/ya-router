package yarouter

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

var anthropicAllowedFields = map[string]struct{}{
	"max_tokens": {}, "messages": {}, "metadata": {}, "model": {}, "output_config": {},
	"stream": {}, "system": {}, "temperature": {}, "thinking": {}, "tool_choice": {},
	"tools": {}, "top_p": {},
}

func translateAnthropicRequest(body []byte, aliases map[string]string) (anthropicTranslatedRequest, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return anthropicTranslatedRequest{}, fmt.Errorf("parse Anthropic request: %w", err)
	}
	for field := range raw {
		if _, ok := anthropicAllowedFields[field]; !ok {
			return anthropicTranslatedRequest{}, fmt.Errorf("unsupported Anthropic field %q", field)
		}
	}
	var request anthropicMessageRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return anthropicTranslatedRequest{}, fmt.Errorf("decode Anthropic request: %w", err)
	}
	if request.Model == "" {
		return anthropicTranslatedRequest{}, fmt.Errorf("model is required")
	}
	if request.MaxTokens <= 0 {
		return anthropicTranslatedRequest{}, fmt.Errorf("max_tokens must be positive")
	}
	model := request.Model
	if target, ok := aliases[model]; ok {
		model = target
	}
	instructions, err := translateAnthropicSystem(request.System)
	if err != nil {
		return anthropicTranslatedRequest{}, err
	}
	input, err := translateAnthropicMessages(request.Messages)
	if err != nil {
		return anthropicTranslatedRequest{}, err
	}
	out := map[string]json.RawMessage{}
	out["model"], _ = json.Marshal(model)
	out["instructions"], _ = json.Marshal(instructions)
	out["input"] = input
	out["max_output_tokens"], _ = json.Marshal(request.MaxTokens)
	out["stream"], _ = json.Marshal(request.Stream)
	if request.Temperature != nil {
		out["temperature"], _ = json.Marshal(*request.Temperature)
	}
	if request.TopP != nil {
		out["top_p"], _ = json.Marshal(*request.TopP)
	}
	if len(request.Metadata) > 0 && string(request.Metadata) != "null" {
		out["metadata"] = request.Metadata
	}
	if len(request.Tools) > 0 {
		tools, toolErr := translateAnthropicTools(request.Tools)
		if toolErr != nil {
			return anthropicTranslatedRequest{}, toolErr
		}
		out["tools"] = tools
	}
	if len(request.ToolChoice) > 0 && string(request.ToolChoice) != "null" {
		choice, choiceErr := translateAnthropicToolChoice(request.ToolChoice)
		if choiceErr != nil {
			return anthropicTranslatedRequest{}, choiceErr
		}
		out["tool_choice"] = choice
	}
	if request.OutputConfig.Effort != "" {
		if !supportsAnthropicEffort(model, request.OutputConfig.Effort) {
			return anthropicTranslatedRequest{}, fmt.Errorf("unsupported output_config.effort %q", request.OutputConfig.Effort)
		}
		out["reasoning"], _ = json.Marshal(map[string]string{"effort": request.OutputConfig.Effort})
	}
	if len(request.OutputConfig.Format) > 0 && string(request.OutputConfig.Format) != "null" {
		format, formatErr := translateAnthropicOutputFormat(request.OutputConfig.Format)
		if formatErr != nil {
			return anthropicTranslatedRequest{}, formatErr
		}
		out["text"] = format
	}
	if request.Thinking.Type != "" && request.Thinking.Type != "adaptive" {
		return anthropicTranslatedRequest{}, fmt.Errorf("unsupported thinking.type %q", request.Thinking.Type)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return anthropicTranslatedRequest{}, fmt.Errorf("encode Responses request: %w", err)
	}
	return anthropicTranslatedRequest{Model: model, Body: encoded, Stream: request.Stream}, nil
}

func translateAnthropicSystem(value json.RawMessage) (string, error) {
	if len(value) == 0 || string(value) == "null" {
		return "", nil
	}
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text, nil
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(value, &blocks); err != nil {
		return "", fmt.Errorf("system must be text or text blocks")
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			return "", fmt.Errorf("unsupported system block type %q", block.Type)
		}
		parts = append(parts, block.Text)
	}
	return strings.Join(parts, "\n"), nil
}

func translateAnthropicMessages(messages []anthropicMessage) (json.RawMessage, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages must not be empty")
	}
	items := make([]json.RawMessage, 0, len(messages))
	for _, message := range messages {
		if message.Role != "user" && message.Role != "assistant" {
			return nil, fmt.Errorf("unsupported message role %q", message.Role)
		}
		blocks, err := decodeAnthropicContent(message.Content)
		if err != nil {
			return nil, err
		}
		for _, block := range blocks {
			item, itemErr := translateAnthropicBlock(message.Role, block)
			if itemErr != nil {
				return nil, itemErr
			}
			items = append(items, item)
		}
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("encode Responses input: %w", err)
	}
	return encoded, nil
}

func decodeAnthropicContent(value json.RawMessage) ([]anthropicContentBlock, error) {
	var text string
	if json.Unmarshal(value, &text) == nil {
		return []anthropicContentBlock{{Type: "text", Text: text}}, nil
	}
	var blocks []anthropicContentBlock
	if err := json.Unmarshal(value, &blocks); err != nil {
		return nil, fmt.Errorf("message content must be text or content blocks")
	}
	return blocks, nil
}

func translateAnthropicBlock(role string, block anthropicContentBlock) (json.RawMessage, error) {
	switch block.Type {
	case "text":
		content, _ := json.Marshal([]map[string]string{{"type": "input_text", "text": block.Text}})
		roleValue, _ := json.Marshal(role)
		return json.Marshal(map[string]json.RawMessage{"role": roleValue, "content": content})
	case "image":
		if block.Source == nil || block.Source.Type != "base64" || block.Source.MediaType == "" || block.Source.Data == "" {
			return nil, fmt.Errorf("image blocks require a base64 source")
		}
		if _, err := base64.StdEncoding.DecodeString(block.Source.Data); err != nil {
			return nil, fmt.Errorf("image source is not valid base64")
		}
		content, _ := json.Marshal([]map[string]string{{"type": "input_image", "image_url": "data:" + block.Source.MediaType + ";base64," + block.Source.Data}})
		roleValue, _ := json.Marshal(role)
		return json.Marshal(map[string]json.RawMessage{"role": roleValue, "content": content})
	case "tool_use":
		if role != "assistant" || block.ID == "" || block.Name == "" || len(block.Input) == 0 {
			return nil, fmt.Errorf("tool_use requires assistant role, id, name, and input")
		}
		if !json.Valid(block.Input) {
			return nil, fmt.Errorf("tool_use input must be JSON")
		}
		return json.Marshal(map[string]string{"type": "function_call", "call_id": block.ID, "name": block.Name, "arguments": string(block.Input)})
	case "tool_result":
		if role != "user" || block.ToolUseID == "" {
			return nil, fmt.Errorf("tool_result requires user role and tool_use_id")
		}
		if block.IsError {
			return nil, fmt.Errorf("tool_result.is_error is not supported by the Responses adapter")
		}
		output, err := anthropicTextContent(block.Content)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"type": "function_call_output", "call_id": block.ToolUseID, "output": output})
	default:
		return nil, fmt.Errorf("unsupported Anthropic content block type %q", block.Type)
	}
}

func anthropicTextContent(value json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text, nil
	}
	blocks, err := decodeAnthropicContent(value)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			return "", fmt.Errorf("tool_result content only supports text")
		}
		parts = append(parts, block.Text)
	}
	return strings.Join(parts, "\n"), nil
}
