package yarouter

import (
	"encoding/json"
	"fmt"
)

type anthropicMessageResponse struct {
	ID         string                     `json:"id"`
	Type       string                     `json:"type"`
	Role       string                     `json:"role"`
	Content    []anthropicResponseContent `json:"content"`
	Model      string                     `json:"model"`
	StopReason string                     `json:"stop_reason"`
	StopSeq    *string                    `json:"stop_sequence"`
	Usage      anthropicUsage             `json:"usage"`
}

type anthropicResponseContent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func responsesToAnthropicMessage(body []byte, publicModel string) ([]byte, error) {
	var response responsesAPIResult
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("parse Responses result: %w", err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("Responses error: %s", response.Error.Type)
	}
	content := make([]anthropicResponseContent, 0, len(response.Output))
	stopReason := "end_turn"
	for _, item := range response.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					content = append(content, anthropicResponseContent{Type: "text", Text: part.Text})
				}
			}
		case "function_call":
			if item.Name == "" || item.Arguments == "" || !json.Valid([]byte(item.Arguments)) {
				return nil, fmt.Errorf("Responses function_call is incomplete")
			}
			id := item.CallID
			if id == "" {
				id = item.ID
			}
			if id == "" {
				return nil, fmt.Errorf("Responses function_call has no stable ID")
			}
			content = append(content, anthropicResponseContent{Type: "tool_use", ID: id, Name: item.Name, Input: json.RawMessage(item.Arguments)})
			stopReason = "tool_use"
		}
	}
	if len(content) == 0 {
		content = append(content, anthropicResponseContent{Type: "text", Text: ""})
	}
	message := anthropicMessageResponse{
		ID:         response.ID,
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      publicModel,
		StopReason: stopReason,
		Usage:      anthropicUsage{},
	}
	if message.ID == "" {
		return nil, fmt.Errorf("Responses result has no ID")
	}
	if response.Usage != nil {
		message.Usage = anthropicUsage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens}
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode Anthropic message: %w", err)
	}
	return encoded, nil
}
