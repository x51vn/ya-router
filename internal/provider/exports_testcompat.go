package provider

// exports_for_test.go provides exported wrappers for unexported functions
// so that the legacy src/*_test.go test suite can access them via type aliases.

import (
	"encoding/json"
	"net/http"

	"github.com/x51vn/github-copilot-svcs/internal/types"
)

func IsAccountLimitSignal(resp *http.Response) (bool, string) {
	return isAccountLimitSignal(resp)
}

func FilterAllowedModels(ml *types.ModelList, allowed []string) *types.ModelList {
	return filterAllowedModels(ml, allowed)
}

func IsModelAllowed(model string, allowed []string) bool {
	return isModelAllowed(model, allowed)
}

func IntersectNormalizedModelNames(left, right []string) []string {
	return intersectNormalizedModelNames(left, right)
}

func CloneModelList(ml *types.ModelList) *types.ModelList {
	return cloneModelList(ml)
}

func NormalizeEmbeddingsRequestBody(body []byte) ([]byte, string) {
	return normalizeEmbeddingsRequestBody(body)
}

func EnsureEmbeddingsResponseCompat(body []byte, model string) []byte {
	return ensureEmbeddingsResponseCompat(body, model)
}

func BuildChatGPTCodexRequest(chatBody []byte) ([]byte, bool, error) {
	return buildChatGPTCodexRequest(chatBody)
}

func BuildChatGPTCodexRequestFromResponses(respBody []byte) ([]byte, bool, error) {
	return buildChatGPTCodexRequestFromResponses(respBody)
}

func NormalizeContentParts(content json.RawMessage) json.RawMessage {
	return normalizeContentParts(content)
}

func ResponsesToChatCompletion(respBody []byte) ([]byte, error) {
	return responsesToChatCompletion(respBody)
}

func ChatCompletionToResponses(chatBody []byte) ([]byte, error) {
	return chatCompletionToResponses(chatBody)
}

func ChatCompletionStreamToResponses(body []byte) []byte {
	return chatCompletionStreamToResponses(body)
}

func ExtractMessages(v json.RawMessage) (string, json.RawMessage, error) {
	return extractMessages(v)
}

func StreamOptionsIncludeUsage(raw map[string]json.RawMessage) bool {
	return streamOptionsIncludeUsage(raw)
}

func StreamResponsesAsChat(w http.ResponseWriter, resp *http.Response, includeUsage bool) error {
	return streamResponsesAsChat(w, resp, includeUsage)
}

func IsStreamingRequestBody(body []byte) bool {
	return isStreamingRequest(body)
}

func ConvertToolsForResponses(raw json.RawMessage) json.RawMessage {
	return convertToolsForResponses(raw)
}

func ParseCopilotPlanFreeModels(pageHTML string) ([]string, error) {
	return parseCopilotPlanFreeModels(pageHTML)
}

func ParseZeroPremiumModels(pageHTML string) ([]string, error) {
	return parseZeroPremiumModels(pageHTML)
}

func ResolveEffectiveCopilotFreeModels(eligible []string, upstream *types.ModelList) []types.Model {
	return resolveEffectiveCopilotFreeModels(eligible, upstream)
}

func CellHasIncludedMarker(raw string) bool {
	return cellHasIncludedMarker(raw)
}

func BuildPlatformResponsesRequest(chatBody []byte) ([]byte, bool, error) {
	return buildPlatformResponsesRequest(chatBody)
}
