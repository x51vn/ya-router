// transform.go — request body parsing and model-field utilities.
package yarouter

import (
	"strings"

	api "github.com/duvu/ya-router/internal/api"
	requestproxy "github.com/duvu/ya-router/internal/proxy"
)

func extractModelFromBody(body []byte) string {
	return requestproxy.ExtractModel(body)
}

func patchBodyModel(body []byte, model string) []byte {
	return requestproxy.PatchModel(body, model)
}

// isModelAllowed reports whether model appears in the allowed list.
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

type ChatCompletionRequest = api.ChatCompletionRequest
type ChatCompletionMessage = api.ChatCompletionMessage
type ChatCompletionResponse = api.ChatCompletionResponse
type ChatCompletionChoice = api.ChatCompletionChoice
type ChatCompletionUsage = api.ChatCompletionUsage
type ModelList = api.ModelList
type Model = api.Model
