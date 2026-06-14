package provider

import (
	"bytes"
	"encoding/json"
)

func normalizeEmbeddingsRequestBody(body []byte) (normalized []byte, model string) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return body, ""
	}

	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return body, ""
	}

	if m, ok := payload["model"].(string); ok {
		model = m
	}

	if input, ok := payload["input"]; ok {
		switch v := input.(type) {
		case string:
			payload["input"] = []any{v}
		case []any:
		default:
			_ = v
		}
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return body, model
	}

	return out, model
}

func ensureEmbeddingsResponseCompat(body []byte, model string) []byte {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return body
	}

	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return body
	}

	if _, ok := payload["object"]; !ok {
		payload["object"] = "list"
	}

	if _, ok := payload["model"]; !ok && model != "" {
		payload["model"] = model
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return body
	}

	return out
}
