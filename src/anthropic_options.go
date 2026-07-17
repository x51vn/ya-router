package yarouter

import (
	"encoding/json"
	"fmt"
	"strings"
)

func translateAnthropicTools(tools []anthropicTool) (json.RawMessage, error) {
	converted := make([]map[string]json.RawMessage, 0, len(tools))
	for _, tool := range tools {
		if tool.Name == "" || len(tool.InputSchema) == 0 || len(tool.InputSchema) > anthropicToolSchemaLimit || !json.Valid(tool.InputSchema) {
			return nil, fmt.Errorf("tools require a name and valid input_schema")
		}
		name, _ := json.Marshal(tool.Name)
		item := map[string]json.RawMessage{
			"type":       json.RawMessage(`"function"`),
			"name":       name,
			"parameters": tool.InputSchema,
		}
		if tool.Description != "" {
			item["description"], _ = json.Marshal(tool.Description)
		}
		converted = append(converted, item)
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		return nil, fmt.Errorf("encode tools: %w", err)
	}
	return encoded, nil
}

func translateAnthropicToolChoice(value json.RawMessage) (json.RawMessage, error) {
	var choice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(value, &choice); err != nil {
		return nil, fmt.Errorf("parse tool_choice: %w", err)
	}
	switch choice.Type {
	case "auto":
		return json.RawMessage(`"auto"`), nil
	case "any":
		return json.RawMessage(`"required"`), nil
	case "none":
		return json.RawMessage(`"none"`), nil
	case "tool":
		if choice.Name == "" {
			return nil, fmt.Errorf("tool_choice.name is required for type tool")
		}
		return json.Marshal(map[string]string{"type": "function", "name": choice.Name})
	default:
		return nil, fmt.Errorf("unsupported tool_choice.type %q", choice.Type)
	}
}

func supportsAnthropicEffort(model, value string) bool {
	switch value {
	case "low", "medium", "high":
		return true
	case "xhigh", "max":
		return strings.HasPrefix(strings.ToLower(model), "codex/gpt-5")
	default:
		return false
	}
}

func translateAnthropicOutputFormat(value json.RawMessage) (json.RawMessage, error) {
	var format struct {
		Type   string          `json:"type"`
		Schema json.RawMessage `json:"schema"`
	}
	if err := json.Unmarshal(value, &format); err != nil {
		return nil, fmt.Errorf("parse output_config.format: %w", err)
	}
	switch format.Type {
	case "text":
		return json.RawMessage(`{"format":{"type":"text"}}`), nil
	case "json_schema":
		if len(format.Schema) == 0 || !json.Valid(format.Schema) {
			return nil, fmt.Errorf("output_config.format.schema must be valid JSON")
		}
		name, _ := json.Marshal("anthropic_output")
		return json.Marshal(map[string]json.RawMessage{
			"format": json.RawMessage(`{"type":"json_schema","name":` + string(name) + `,"schema":` + string(format.Schema) + `}`),
		})
	default:
		return nil, fmt.Errorf("unsupported output_config.format.type %q", format.Type)
	}
}
