package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	operationpkg "github.com/duvu/ya-router/internal/operation"
	providerpkg "github.com/duvu/ya-router/internal/provider"
)

const maxOperationRequestBody = 64 << 10

type OperationManager interface {
	DefaultTTL() time.Duration
	Now() time.Time
	Create(operationpkg.CreateRequest) (operationpkg.Record, bool, error)
	Get(string) (operationpkg.Record, error)
	List() []operationpkg.Record
	Cancel(string) (operationpkg.Record, error)
	Events(uint64) []operationpkg.Event
	Subscribe(int) (<-chan operationpkg.Event, func())
}

type AuthSessionStarter interface {
	StartAuthSession(operationpkg.Record) error
}

type operationCreateRequest struct {
	Kind             string                      `json:"kind"`
	Target           string                      `json:"target,omitempty"`
	Cancelable       *bool                       `json:"cancelable,omitempty"`
	ExpiresInSeconds int                         `json:"expires_in_seconds,omitempty"`
	RecoveryPolicy   operationpkg.RecoveryPolicy `json:"recovery_policy,omitempty"`
}

type authSessionCreateRequest struct {
	ProviderID       providerpkg.ID `json:"provider_id"`
	AccountID        string         `json:"account_id,omitempty"`
	Method           string         `json:"method"`
	ExpiresInSeconds int            `json:"expires_in_seconds,omitempty"`
}

type operationEventPage struct {
	Data      []operationpkg.Event `json:"data"`
	NextAfter uint64               `json:"next_after"`
}

var allowedOperationKinds = map[string]struct{}{
	"config_apply":      {},
	"config_validate":   {},
	"credential_verify": {},
	"model_refresh":     {},
}

var allowedAuthMethods = map[providerpkg.ID]map[string]struct{}{
	providerpkg.Copilot: {
		"device_code":           {},
		"manual_token_recovery": {},
	},
	providerpkg.Codex: {
		"device_code":           {},
		"api_key":               {},
		"manual_token_recovery": {},
	},
	providerpkg.Kilo: {
		"anonymous": {},
		"api_key":   {},
	},
}

// RegisterOperationRoutes adds persistent operation and provider-neutral auth
// session resources. Provider-specific authentication execution is deliberately
// deferred to YA-TUI-07; these resources own durable lifecycle and reconnect.
func RegisterOperationRoutes(api *API, manager OperationManager, starters ...AuthSessionStarter) {
	var starter AuthSessionStarter
	if len(starters) > 0 {
		starter = starters[0]
	}
	api.Handle(http.MethodPost, "/control/v1/operations", RoleOperator, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		createOperation(writer, request, manager)
	}))
	api.Handle(http.MethodGet, "/control/v1/operations/events", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		serveOperationEvents(writer, request, manager, false)
	}))
	api.Handle(http.MethodGet, "/control/v1/operations/events/stream", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		serveOperationEvents(writer, request, manager, true)
	}))
	api.HandlePrefix(http.MethodGet, "/control/v1/operations/", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		getOperation(writer, request, manager, "/control/v1/operations/", false)
	}))
	api.HandlePrefix(http.MethodDelete, "/control/v1/operations/", RoleOperator, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cancelOperation(writer, request, manager, "/control/v1/operations/", false)
	}))

	api.Handle(http.MethodPost, "/control/v1/auth-sessions", RoleOperator, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		createAuthSession(writer, request, manager, starter)
	}))
	api.HandlePrefix(http.MethodGet, "/control/v1/auth-sessions/", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		getOperation(writer, request, manager, "/control/v1/auth-sessions/", true)
	}))
	api.HandlePrefix(http.MethodDelete, "/control/v1/auth-sessions/", RoleOperator, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cancelOperation(writer, request, manager, "/control/v1/auth-sessions/", true)
	}))
}

func createOperation(writer http.ResponseWriter, request *http.Request, manager OperationManager) {
	if manager == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "operation_manager_unavailable", "The operation manager is unavailable.", true, nil)
		return
	}
	var input operationCreateRequest
	if err := decodeStrictJSON(request, &input); err != nil {
		writeError(writer, request, http.StatusBadRequest, "invalid_operation_request", "The operation request is invalid.", false, nil)
		return
	}
	input.Kind = strings.TrimSpace(input.Kind)
	if _, allowed := allowedOperationKinds[input.Kind]; !allowed {
		writeError(writer, request, http.StatusBadRequest, "unsupported_operation_kind", "The operation kind is not supported.", false, nil)
		return
	}
	input.Target = strings.TrimSpace(input.Target)
	if len(input.Target) > 256 || strings.ContainsAny(input.Target, "\r\n\t") {
		writeError(writer, request, http.StatusBadRequest, "invalid_operation_target", "The operation target is invalid.", false, nil)
		return
	}
	identity, _ := IdentityFromContext(request.Context())
	key := strings.TrimSpace(request.Header.Get(IdempotencyKeyHeader))
	if key == "" {
		writeError(writer, request, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required for operation creation.", false, nil)
		return
	}
	cancelable := true
	if input.Cancelable != nil {
		cancelable = *input.Cancelable
	}
	expiresAt, err := operationExpiry(manager, input.ExpiresInSeconds)
	if err != nil {
		writeError(writer, request, http.StatusBadRequest, "invalid_operation_expiry", err.Error(), false, nil)
		return
	}
	canonical, _ := json.Marshal(input)
	record, created, err := manager.Create(operationpkg.CreateRequest{
		Kind:           input.Kind,
		Target:         strings.TrimSpace(input.Target),
		Owner:          identity.Subject,
		Cancelable:     cancelable,
		ExpiresAt:      expiresAt,
		RecoveryPolicy: input.RecoveryPolicy,
		IdempotencyKey: key,
		RequestDigest:  digestPayload(canonical),
	})
	if err != nil {
		writeOperationError(writer, request, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(writer, status, record)
}

func createAuthSession(writer http.ResponseWriter, request *http.Request, manager OperationManager, starter AuthSessionStarter) {
	if manager == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "operation_manager_unavailable", "The operation manager is unavailable.", true, nil)
		return
	}
	var input authSessionCreateRequest
	if err := decodeStrictJSON(request, &input); err != nil {
		writeError(writer, request, http.StatusBadRequest, "invalid_auth_session_request", "The auth-session request is invalid or contains an unsupported secret field.", false, nil)
		return
	}
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.Method = strings.TrimSpace(input.Method)
	methods, providerKnown := allowedAuthMethods[input.ProviderID]
	_, methodAllowed := methods[input.Method]
	if !providerKnown || !methodAllowed {
		writeError(writer, request, http.StatusBadRequest, "unsupported_auth_method", "The authentication method is not supported for the provider.", false, nil)
		return
	}
	identity, _ := IdentityFromContext(request.Context())
	key := strings.TrimSpace(request.Header.Get(IdempotencyKeyHeader))
	if key == "" {
		writeError(writer, request, http.StatusBadRequest, "idempotency_key_required", "Idempotency-Key is required for auth-session creation.", false, nil)
		return
	}
	expiresAt, err := operationExpiry(manager, input.ExpiresInSeconds)
	if err != nil {
		writeError(writer, request, http.StatusBadRequest, "invalid_auth_session_expiry", err.Error(), false, nil)
		return
	}
	target := string(input.ProviderID)
	if input.AccountID != "" {
		target += "/" + input.AccountID
	}
	canonical, _ := json.Marshal(input)
	record, created, err := manager.Create(operationpkg.CreateRequest{
		Kind:           "auth_session",
		Target:         target,
		Owner:          identity.Subject,
		Cancelable:     true,
		ExpiresAt:      expiresAt,
		RecoveryPolicy: operationpkg.RecoveryExpire,
		Metadata: map[string]string{
			"provider_id": string(input.ProviderID),
			"account_id":  input.AccountID,
			"auth_method": input.Method,
		},
		IdempotencyKey: key,
		RequestDigest:  digestPayload(canonical),
	})
	if err != nil {
		writeOperationError(writer, request, err)
		return
	}
	if created && starter != nil {
		if err := starter.StartAuthSession(record); err != nil {
			writeError(writer, request, http.StatusServiceUnavailable, "auth_session_start_failed", "The authentication session could not be started.", true, nil)
			return
		}
	}
	status := http.StatusOK
	if created {
		status = http.StatusAccepted
	}
	writeJSON(writer, status, record)
}

func getOperation(writer http.ResponseWriter, request *http.Request, manager OperationManager, prefix string, requireAuthSession bool) {
	id, ok := resourceID(request.URL.Path, prefix)
	if !ok {
		writeError(writer, request, http.StatusNotFound, "operation_not_found", "The operation was not found.", false, nil)
		return
	}
	record, err := manager.Get(id)
	if err != nil {
		writeOperationError(writer, request, err)
		return
	}
	if requireAuthSession && record.Kind != "auth_session" {
		writeError(writer, request, http.StatusNotFound, "auth_session_not_found", "The auth session was not found.", false, nil)
		return
	}
	if !mayAccessOperation(request.Context(), record) {
		writeError(writer, request, http.StatusForbidden, "operation_forbidden", "The authenticated identity cannot access this operation.", false, nil)
		return
	}
	writeJSON(writer, http.StatusOK, record)
}

func cancelOperation(writer http.ResponseWriter, request *http.Request, manager OperationManager, prefix string, requireAuthSession bool) {
	id, ok := resourceID(request.URL.Path, prefix)
	if !ok {
		writeError(writer, request, http.StatusNotFound, "operation_not_found", "The operation was not found.", false, nil)
		return
	}
	record, err := manager.Get(id)
	if err != nil {
		writeOperationError(writer, request, err)
		return
	}
	if requireAuthSession && record.Kind != "auth_session" {
		writeError(writer, request, http.StatusNotFound, "auth_session_not_found", "The auth session was not found.", false, nil)
		return
	}
	if !mayAccessOperation(request.Context(), record) {
		writeError(writer, request, http.StatusForbidden, "operation_forbidden", "The authenticated identity cannot cancel this operation.", false, nil)
		return
	}
	cancelled, err := manager.Cancel(id)
	if err != nil {
		writeOperationError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, cancelled)
}

func serveOperationEvents(writer http.ResponseWriter, request *http.Request, manager OperationManager, stream bool) {
	after, err := operationEventCursor(request)
	if err != nil {
		writeError(writer, request, http.StatusBadRequest, "invalid_operation_event_cursor", "The operation event cursor must be an unsigned integer.", false, nil)
		return
	}
	if !stream {
		events := visibleOperationEvents(request.Context(), manager, manager.Events(after))
		writeJSON(writer, http.StatusOK, operationEventPage{Data: nonNilOperationEvents(events), NextAfter: lastOperationSequence(after, events)})
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeError(writer, request, http.StatusInternalServerError, "streaming_not_supported", "The HTTP transport does not support event streaming.", false, nil)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")
	writer.WriteHeader(http.StatusOK)
	live, cancel := manager.Subscribe(32)
	defer cancel()
	for _, event := range visibleOperationEvents(request.Context(), manager, manager.Events(after)) {
		if event.Sequence <= after {
			continue
		}
		if err := writeOperationSSE(writer, event); err != nil {
			return
		}
		after = event.Sequence
	}
	flusher.Flush()
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-live:
			if !open {
				return
			}
			if event.Sequence <= after || !operationEventVisible(request.Context(), manager, event) {
				continue
			}
			if err := writeOperationSSE(writer, event); err != nil {
				return
			}
			after = event.Sequence
			flusher.Flush()
		case <-keepalive.C:
			if _, err := fmt.Fprint(writer, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func decodeStrictJSON(request *http.Request, target any) error {
	body := io.Reader(http.NoBody)
	if request.Body != nil {
		body = request.Body
	}
	payload, err := io.ReadAll(io.LimitReader(body, maxOperationRequestBody+1))
	if err != nil {
		return err
	}
	if len(payload) > maxOperationRequestBody {
		return fmt.Errorf("request body is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("multiple JSON values are not allowed")
	}
	return nil
}

func operationExpiry(manager OperationManager, seconds int) (time.Time, error) {
	ttl := manager.DefaultTTL()
	if seconds != 0 {
		if seconds < 30 || seconds > 3600 {
			return time.Time{}, fmt.Errorf("Expiry must be between 30 and 3600 seconds.")
		}
		ttl = time.Duration(seconds) * time.Second
	}
	return manager.Now().Add(ttl).UTC(), nil
}

func digestPayload(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func resourceID(path, prefix string) (string, bool) {
	id := strings.TrimPrefix(path, prefix)
	if id == path || id == "" || strings.Contains(id, "/") || len(id) > 128 {
		return "", false
	}
	return id, true
}

func mayAccessOperation(ctx context.Context, record operationpkg.Record) bool {
	identity, ok := IdentityFromContext(ctx)
	return ok && (identity.Role == RoleAdmin || identity.Subject == record.Owner)
}

func visibleOperationEvents(ctx context.Context, manager OperationManager, events []operationpkg.Event) []operationpkg.Event {
	identity, _ := IdentityFromContext(ctx)
	if identity.Role == RoleAdmin {
		return append([]operationpkg.Event(nil), events...)
	}
	result := make([]operationpkg.Event, 0, len(events))
	for _, event := range events {
		if operationEventVisible(ctx, manager, event) {
			result = append(result, event)
		}
	}
	return result
}

func operationEventVisible(ctx context.Context, manager OperationManager, event operationpkg.Event) bool {
	record, err := manager.Get(event.OperationID)
	return err == nil && mayAccessOperation(ctx, record)
}

func operationEventCursor(request *http.Request) (uint64, error) {
	value := strings.TrimSpace(request.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(request.URL.Query().Get("after"))
	}
	if value == "" {
		return 0, nil
	}
	return strconv.ParseUint(value, 10, 64)
}

func lastOperationSequence(after uint64, events []operationpkg.Event) uint64 {
	if len(events) == 0 {
		return after
	}
	return events[len(events)-1].Sequence
}

func nonNilOperationEvents(events []operationpkg.Event) []operationpkg.Event {
	if events == nil {
		return []operationpkg.Event{}
	}
	return events
}

func writeOperationSSE(writer http.ResponseWriter, event operationpkg.Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, payload)
	return err
}

func writeOperationError(writer http.ResponseWriter, request *http.Request, err error) {
	var notFound *operationpkg.NotFoundError
	var idempotencyConflict *operationpkg.IdempotencyConflictError
	var transition *operationpkg.InvalidTransitionError
	var capacity *operationpkg.CapacityError
	switch {
	case errors.As(err, &notFound):
		writeError(writer, request, http.StatusNotFound, "operation_not_found", "The operation was not found.", false, nil)
	case errors.As(err, &idempotencyConflict):
		writeError(writer, request, http.StatusConflict, "operation_idempotency_conflict", "The idempotency key was already used with a different request.", false, nil)
	case errors.As(err, &transition):
		writeError(writer, request, http.StatusConflict, "operation_state_conflict", "The operation cannot be changed from its current state.", false, nil)
	case errors.As(err, &capacity):
		writeError(writer, request, http.StatusServiceUnavailable, "operation_capacity_exhausted", "The operation store is at capacity.", true, nil)
	case strings.Contains(err.Error(), "not cancelable"):
		writeError(writer, request, http.StatusConflict, "operation_not_cancelable", "The operation is not cancelable.", false, nil)
	default:
		writeError(writer, request, http.StatusServiceUnavailable, "operation_persistence_failed", "The operation state could not be persisted.", true, nil)
	}
}
