package yarouter

import "encoding/json"

const (
	anthropicRequestLimit    = 5 * 1024 * 1024
	anthropicToolSchemaLimit = 256 * 1024
)

type anthropicTranslatedRequest struct {
	Model  string
	Body   []byte
	Stream bool
}

type anthropicMessageRequest struct {
	Model        string                `json:"model"`
	System       json.RawMessage       `json:"system,omitempty"`
	Messages     []anthropicMessage    `json:"messages"`
	MaxTokens    int                   `json:"max_tokens"`
	Tools        []anthropicTool       `json:"tools,omitempty"`
	ToolChoice   json.RawMessage       `json:"tool_choice,omitempty"`
	OutputConfig anthropicOutputConfig `json:"output_config,omitempty"`
	Thinking     anthropicThinking     `json:"thinking,omitempty"`
	Metadata     json.RawMessage       `json:"metadata,omitempty"`
	Stream       bool                  `json:"stream,omitempty"`
	Temperature  *float64              `json:"temperature,omitempty"`
	TopP         *float64              `json:"top_p,omitempty"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicOutputConfig struct {
	Effort string          `json:"effort,omitempty"`
	Format json.RawMessage `json:"format,omitempty"`
}

type anthropicThinking struct {
	Type string `json:"type,omitempty"`
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Source    *anthropicImage `json:"source,omitempty"`
}

type anthropicImage struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}
