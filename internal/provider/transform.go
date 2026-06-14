package provider

import (
	"encoding/json"
	"log"
	"strings"
)

func ExtractModelFromBody(body []byte) string {
	return extractModelFromBody(body)
}

func PatchBodyModel(body []byte, model string) []byte {
	return patchBodyModel(body, model)
}

func extractModelFromBody(body []byte) string {
	var req struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	return req.Model
}

func patchBodyModel(body []byte, model string) []byte {
	var m map[string]interface{}
	if err := json.Unmarshal(body, &m); err != nil {
		log.Printf("patchBodyModel: cannot unmarshal body: %v", err)
		return body
	}
	m["model"] = model
	patched, err := json.Marshal(m)
	if err != nil {
		log.Printf("patchBodyModel: cannot re-marshal body: %v", err)
		return body
	}
	return patched
}

func isModelAllowed(model string, allowedModels []string) bool {
	if len(allowedModels) == 0 {
		return true
	}
	for _, allowed := range allowedModels {
		if strings.EqualFold(model, allowed) {
			return true
		}
	}
	return false
}
