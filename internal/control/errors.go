package control

import (
	"encoding/json"
	"log"
	"net/http"
)

// ErrorBody is the stable management-plane error contract.
type ErrorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	RequestID string         `json:"request_id"`
	Details   map[string]any `json:"details,omitempty"`
}

type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

func writeError(writer http.ResponseWriter, request *http.Request, status int, code, message string, retryable bool, details map[string]any) {
	writeJSON(writer, status, ErrorEnvelope{Error: ErrorBody{
		Code:      code,
		Message:   message,
		Retryable: retryable,
		RequestID: RequestIDFromContext(request.Context()),
		Details:   details,
	}})
}

func writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	if err := json.NewEncoder(writer).Encode(payload); err != nil {
		log.Printf("control response encoding failed: %v", err)
	}
}
