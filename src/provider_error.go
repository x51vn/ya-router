package yarouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type ProviderErrorKind string

const (
	ProviderErrorInvalidRequest ProviderErrorKind = "invalid_request"
	ProviderErrorAuthRequired   ProviderErrorKind = "auth_required"
	ProviderErrorEntitlement    ProviderErrorKind = "entitlement_denied"
	ProviderErrorRateLimit      ProviderErrorKind = "rate_limited"
	ProviderErrorUnsupported    ProviderErrorKind = "unsupported_capability"
	ProviderErrorUnavailable    ProviderErrorKind = "provider_unavailable"
	ProviderErrorTransport      ProviderErrorKind = "transport_error"
)

type ProviderError struct {
	Kind       ProviderErrorKind
	StatusCode int
	Retryable  bool
	Provider   ProviderID
	Err        error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "provider error"
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Kind)
}

func (e *ProviderError) Unwrap() error { return e.Err }

func newProviderError(provider ProviderID, kind ProviderErrorKind, status int, retryable bool, format string, args ...interface{}) error {
	return &ProviderError{
		Kind:       kind,
		StatusCode: status,
		Retryable:  retryable,
		Provider:   provider,
		Err:        fmt.Errorf(format, args...),
	}
}

func providerErrorStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) && providerErr.StatusCode > 0 {
		return providerErr.StatusCode
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	if errors.Is(err, context.Canceled) {
		return http.StatusRequestTimeout
	}
	return http.StatusBadGateway
}

func writeOpenAIError(w http.ResponseWriter, status int, err error) {
	if status <= 0 {
		status = http.StatusInternalServerError
	}
	message := "request failed"
	errorType := "proxy_error"
	if err != nil {
		message = err.Error()
	}
	var providerErr *ProviderError
	if errors.As(err, &providerErr) {
		errorType = string(providerErr.Kind)
	}
	payload := map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    errorType,
		},
	}
	body, _ := json.Marshal(payload)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func responseStatus(w http.ResponseWriter) int {
	type statusWriter interface{ StatusCode() int }
	if sw, ok := w.(statusWriter); ok {
		if status := sw.StatusCode(); status > 0 {
			return status
		}
	}
	return http.StatusOK
}
