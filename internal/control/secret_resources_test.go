package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	secretpkg "github.com/duvu/ya-router/internal/secret"
)

func TestSecretRouteExposesOnlyRedactedMetadata(t *testing.T) {
	store := secretpkg.NewMemoryStore(nil)
	const secretValue = "top-secret-value"
	if _, err := store.Set("admin", "kilo/api_key", secretValue); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterReadOnly("codex/api_key", "env-value", secretpkg.SourceEnvironment); err != nil {
		t.Fatal(err)
	}

	api := NewAPI(APIOptions{})
	RegisterSecretRoutes(api, store)
	identity := Identity{Subject: "test", Role: RoleViewer, Source: "test"}
	request := httptest.NewRequest(http.MethodGet, "/control/v1/secrets", nil)
	response := httptest.NewRecorder()
	api.Handler(FixedAuthenticator(identity)).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if strings.Contains(body, secretValue) || strings.Contains(body, "env-value") {
		t.Fatalf("secret value leaked in response: %s", body)
	}
	var payload struct {
		Data []secretpkg.Metadata `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(payload.Data))
	}
	for _, metadata := range payload.Data {
		if !metadata.Configured {
			t.Fatalf("secret %q should be configured", metadata.ID)
		}
	}
}

func TestSecretRouteNilReaderRegistersNothing(t *testing.T) {
	api := NewAPI(APIOptions{})
	RegisterSecretRoutes(api, nil)
	identity := Identity{Subject: "test", Role: RoleViewer, Source: "test"}
	request := httptest.NewRequest(http.MethodGet, "/control/v1/secrets", nil)
	response := httptest.NewRecorder()
	api.Handler(FixedAuthenticator(identity)).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no secret reader is registered", response.Code)
	}
}

func TestSecretWriteIsIdempotentAndNeverReturnsValue(t *testing.T) {
	const value = "credential-value-must-not-leak"
	store := secretpkg.NewMemoryStore(nil)
	api := NewAPI(APIOptions{})
	RegisterSecretRoutes(api, store)
	handler := api.Handler(FixedAuthenticator(Identity{Subject: "operator", Role: RoleOperator, Source: "test"}))

	request := httptest.NewRequest(http.MethodPut, "/control/v1/secrets", strings.NewReader(`{"slot":"kilo/api_key","value":"`+value+`"}`))
	request.Header.Set(IdempotencyKeyHeader, "credential-once")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("write returned %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), value) {
		t.Fatalf("write response leaked credential: %s", response.Body.String())
	}
	resolved, source, ok := store.Resolve("kilo/api_key")
	if !ok || source != secretpkg.SourceManaged || resolved != value {
		t.Fatalf("store = %q, %s, %t", resolved, source, ok)
	}
}
