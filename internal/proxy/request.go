// Package proxy contains provider-neutral data-plane request helpers. Network
// execution remains with provider implementations.
package proxy

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/duvu/ya-router/internal/provider"
)

// CapabilityFromPath maps a supported data-plane path to a capability.
func CapabilityFromPath(path string) (provider.Capability, error) {
	switch {
	case strings.Contains(path, "/chat/completions"):
		return provider.CapabilityChat, nil
	case strings.Contains(path, "/responses"):
		return provider.CapabilityResponses, nil
	case strings.Contains(path, "/embeddings"):
		return provider.CapabilityEmbeddings, nil
	default:
		return "", fmt.Errorf("unsupported path: %s", path)
	}
}

// ExtractModel returns the top-level model field, or an empty string when the
// request is invalid or omits it.
func ExtractModel(body []byte) string {
	var request struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return ""
	}
	return request.Model
}

// PatchModel returns a request body with its top-level model replaced. Invalid
// input is returned unchanged for compatibility with the existing data plane.
func PatchModel(body []byte, model string) []byte {
	var request map[string]interface{}
	if err := json.Unmarshal(body, &request); err != nil {
		log.Printf("PatchModel: cannot unmarshal body: %v", err)
		return body
	}
	request["model"] = model
	patched, err := json.Marshal(request)
	if err != nil {
		log.Printf("PatchModel: cannot re-marshal body: %v", err)
		return body
	}
	return patched
}
