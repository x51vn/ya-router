package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	operationpkg "github.com/duvu/ya-router/internal/operation"
)

func openControlOperationManager(t *testing.T, path string, now *time.Time) *operationpkg.Manager {
	t.Helper()
	manager, err := operationpkg.OpenManager(operationpkg.Options{
		Path:          path,
		MaxOperations: 32,
		MaxEvents:     128,
		Retention:     time.Hour,
		DefaultTTL:    time.Minute,
		Now:           func() time.Time { return *now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func operationHandler(manager OperationManager, identity Identity) http.Handler {
	api := NewAPI(APIOptions{})
	RegisterOperationRoutes(api, manager)
	return api.Handler(FixedAuthenticator(identity))
}

type recordingAuthSessionStarter struct {
	records []operationpkg.Record
}

func (starter *recordingAuthSessionStarter) StartAuthSession(record operationpkg.Record) error {
	starter.records = append(starter.records, record)
	return nil
}

func TestAuthSessionCreationStartsDaemonWork(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	manager := openControlOperationManager(t, filepath.Join(t.TempDir(), "operations.json"), &now)
	starter := &recordingAuthSessionStarter{}
	api := NewAPI(APIOptions{})
	RegisterOperationRoutes(api, manager, starter)
	handler := api.Handler(FixedAuthenticator(Identity{Subject: "operator", Role: RoleOperator, Source: "test"}))

	request := httptest.NewRequest(http.MethodPost, "/control/v1/auth-sessions", strings.NewReader(`{"provider_id":"kilo","method":"anonymous"}`))
	request.Header.Set(IdempotencyKeyHeader, "start-auth-work")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if len(starter.records) != 1 || starter.records[0].Kind != "auth_session" {
		t.Fatalf("started records = %+v", starter.records)
	}
}

func TestAuthSessionCreationIsPersistentAndIdempotent(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	path := filepath.Join(t.TempDir(), "operations.json")
	manager := openControlOperationManager(t, path, &now)
	operator := Identity{Subject: "operator:one", Role: RoleOperator, Source: "test"}
	body := `{"provider_id":"copilot","account_id":"primary","method":"device_code"}`

	create := func(handler http.Handler, payload string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/control/v1/auth-sessions", strings.NewReader(payload))
		request.Header.Set(IdempotencyKeyHeader, "auth-once")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	first := create(operationHandler(manager, operator), body)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first creation returned %d: %s", first.Code, first.Body.String())
	}
	var firstRecord operationpkg.Record
	if err := json.Unmarshal(first.Body.Bytes(), &firstRecord); err != nil {
		t.Fatal(err)
	}
	if firstRecord.Kind != "auth_session" || firstRecord.Owner != operator.Subject || firstRecord.Metadata["auth_method"] != "device_code" {
		t.Fatalf("unexpected auth session: %+v", firstRecord)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	restarted := openControlOperationManager(t, path, &now)
	defer restarted.Close(context.Background())
	second := create(operationHandler(restarted, operator), body)
	if second.Code != http.StatusOK {
		t.Fatalf("idempotent replay returned %d: %s", second.Code, second.Body.String())
	}
	var secondRecord operationpkg.Record
	if err := json.Unmarshal(second.Body.Bytes(), &secondRecord); err != nil {
		t.Fatal(err)
	}
	if secondRecord.ID != firstRecord.ID || secondRecord.State != operationpkg.StateExpired {
		t.Fatalf("persistent idempotency/recovery mismatch: first=%+v second=%+v", firstRecord, secondRecord)
	}

	conflict := create(operationHandler(restarted, operator), `{"provider_id":"codex","method":"device_code"}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed idempotent payload returned %d: %s", conflict.Code, conflict.Body.String())
	}
}

func TestAuthSessionRejectsSecretMaterial(t *testing.T) {
	now := time.Unix(2000, 0).UTC()
	manager := openControlOperationManager(t, filepath.Join(t.TempDir(), "operations.json"), &now)
	defer manager.Close(context.Background())
	request := httptest.NewRequest(http.MethodPost, "/control/v1/auth-sessions", strings.NewReader(`{"provider_id":"codex","method":"api_key","api_key":"must-not-store"}`))
	request.Header.Set(IdempotencyKeyHeader, "secret-rejected")
	response := httptest.NewRecorder()
	operationHandler(manager, Identity{Subject: "operator:one", Role: RoleOperator, Source: "test"}).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "must-not-store") {
		t.Fatalf("secret-bearing request was not safely rejected: %d %s", response.Code, response.Body.String())
	}
	if len(manager.List()) != 0 {
		t.Fatalf("rejected secret request created an operation: %+v", manager.List())
	}
}

func TestOperationOwnershipCancelAndExactEventRoutes(t *testing.T) {
	now := time.Unix(3000, 0).UTC()
	manager := openControlOperationManager(t, filepath.Join(t.TempDir(), "operations.json"), &now)
	defer manager.Close(context.Background())
	owner := Identity{Subject: "operator:owner", Role: RoleOperator, Source: "test"}
	createRequest := httptest.NewRequest(http.MethodPost, "/control/v1/operations", strings.NewReader(`{"kind":"model_refresh","target":"copilot"}`))
	createRequest.Header.Set(IdempotencyKeyHeader, "refresh-once")
	createdResponse := httptest.NewRecorder()
	operationHandler(manager, owner).ServeHTTP(createdResponse, createRequest)
	if createdResponse.Code != http.StatusAccepted {
		t.Fatalf("create returned %d: %s", createdResponse.Code, createdResponse.Body.String())
	}
	var created operationpkg.Record
	_ = json.Unmarshal(createdResponse.Body.Bytes(), &created)

	otherRequest := httptest.NewRequest(http.MethodGet, "/control/v1/operations/"+created.ID, nil)
	otherResponse := httptest.NewRecorder()
	operationHandler(manager, Identity{Subject: "viewer:other", Role: RoleViewer, Source: "test"}).ServeHTTP(otherResponse, otherRequest)
	if otherResponse.Code != http.StatusForbidden {
		t.Fatalf("cross-owner read returned %d: %s", otherResponse.Code, otherResponse.Body.String())
	}

	cancelRequest := httptest.NewRequest(http.MethodDelete, "/control/v1/operations/"+created.ID, nil)
	cancelResponse := httptest.NewRecorder()
	operationHandler(manager, owner).ServeHTTP(cancelResponse, cancelRequest)
	if cancelResponse.Code != http.StatusOK {
		t.Fatalf("cancel returned %d: %s", cancelResponse.Code, cancelResponse.Body.String())
	}
	var cancelled operationpkg.Record
	_ = json.Unmarshal(cancelResponse.Body.Bytes(), &cancelled)
	if cancelled.State != operationpkg.StateCancelled {
		t.Fatalf("operation was not cancelled: %+v", cancelled)
	}

	poll := httptest.NewRequest(http.MethodGet, "/control/v1/operations/events?after="+jsonNumber(created.Sequence), nil)
	pollResponse := httptest.NewRecorder()
	operationHandler(manager, owner).ServeHTTP(pollResponse, poll)
	if pollResponse.Code != http.StatusOK || !strings.Contains(pollResponse.Body.String(), `"type":"cancelled"`) {
		t.Fatalf("exact event route lost to detail prefix: %d %s", pollResponse.Code, pollResponse.Body.String())
	}

	ctx, stop := context.WithCancel(context.Background())
	stop()
	stream := httptest.NewRequest(http.MethodGet, "/control/v1/operations/events/stream", nil).WithContext(ctx)
	stream.Header.Set("Last-Event-ID", jsonNumber(created.Sequence))
	streamResponse := httptest.NewRecorder()
	operationHandler(manager, owner).ServeHTTP(streamResponse, stream)
	if streamResponse.Code != http.StatusOK || !strings.Contains(streamResponse.Body.String(), "event: cancelled") {
		t.Fatalf("operation SSE did not resume: %d %s", streamResponse.Code, streamResponse.Body.String())
	}
}

func TestRouterExactRouteWinsOverPrefixAndReportsMethodMismatch(t *testing.T) {
	api := NewAPI(APIOptions{})
	api.HandlePrefix(http.MethodGet, "/control/v1/items/", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("prefix"))
	}))
	api.Handle(http.MethodGet, "/control/v1/items/events", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("exact"))
	}))
	handler := api.Handler(FixedAuthenticator(Identity{Subject: "viewer:test", Role: RoleViewer, Source: "test"}))

	exact := httptest.NewRecorder()
	handler.ServeHTTP(exact, httptest.NewRequest(http.MethodGet, "/control/v1/items/events", nil))
	if exact.Body.String() != "exact" {
		t.Fatalf("exact route did not win: %s", exact.Body.String())
	}
	wrongMethod := httptest.NewRecorder()
	handler.ServeHTTP(wrongMethod, httptest.NewRequest(http.MethodPost, "/control/v1/items/value", nil))
	if wrongMethod.Code != http.StatusMethodNotAllowed {
		t.Fatalf("prefix method mismatch returned %d", wrongMethod.Code)
	}
}

func jsonNumber(value uint64) string {
	payload, _ := json.Marshal(value)
	return string(payload)
}
