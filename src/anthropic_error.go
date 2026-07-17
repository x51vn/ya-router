package yarouter

import (
	"encoding/json"
	"errors"
	"net/http"
)

func writeAnthropicError(w http.ResponseWriter, status int, err error) {
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	typeName := "api_error"
	message := "request failed"
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		typeName, message = anthropicErrorDetails(providerErr)
	}
	payload := struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}{Type: "error"}
	payload.Error.Type = typeName
	payload.Error.Message = message
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func anthropicErrorDetails(err *ProviderError) (string, string) {
	switch err.Kind {
	case ProviderErrorInvalidRequest, ProviderErrorUnsupported:
		return "invalid_request_error", "request uses an unsupported capability"
	case ProviderErrorAuthRequired:
		return "authentication_error", "gateway authentication failed"
	case ProviderErrorEntitlement:
		return "permission_error", "upstream entitlement denied"
	case ProviderErrorRateLimit:
		return "rate_limit_error", "upstream rate limit reached"
	case ProviderErrorModelUnavailable, ProviderErrorUnavailable:
		return "api_error", "no compatible upstream is available"
	default:
		return "api_error", "upstream request failed"
	}
}
