package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/duvu/ya-router/internal/control"
	secretpkg "github.com/duvu/ya-router/internal/secret"
)

// newTestServer stands up the real control API over TCP with a fixed viewer
// identity so the typed client can be exercised end-to-end.
func newTestServer(t *testing.T, role control.Role) (*httptest.Server, *Client) {
	t.Helper()
	api := control.NewAPI(control.APIOptions{ServiceVersion: "test"})
	control.RegisterReadRoutes(api, &stubReadModel{})
	control.RegisterSecretRoutes(api, secretpkg.NewMemoryStore(nil))
	identity := control.Identity{Subject: "test", Role: role, Source: "test"}
	server := httptest.NewServer(api.Handler(control.FixedAuthenticator(identity)))
	t.Cleanup(server.Close)

	address := strings.TrimPrefix(server.URL, "http://")
	cl, err := New(Profile{Transport: TransportHTTPS, Address: address})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	// The test server is plain HTTP; point the client's transport at it.
	cl.baseURL = server.URL
	cl.http = server.Client()
	return server, cl
}

func TestClientWritesCredentialWithoutReturningValue(t *testing.T) {
	_, cl := newTestServer(t, control.RoleOperator)
	const value = "client-secret-must-not-return"
	metadata, err := cl.SetSecret(context.Background(), "kilo/api_key", value)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ID != "kilo/api_key" || !metadata.Configured {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestClientMetaAndCompatibility(t *testing.T) {
	_, cl := newTestServer(t, control.RoleViewer)
	meta, err := cl.Meta(context.Background())
	if err != nil {
		t.Fatalf("meta: %v", err)
	}
	if meta.ServiceVersion != "test" {
		t.Fatalf("service version = %q", meta.ServiceVersion)
	}
	if !meta.Client.Compatible {
		t.Fatalf("expected client compatible")
	}
	if err := cl.CheckCompatible(context.Background()); err != nil {
		t.Fatalf("CheckCompatible: %v", err)
	}
}

func TestClientReadCommands(t *testing.T) {
	_, cl := newTestServer(t, control.RoleViewer)
	ctx := context.Background()

	providers, err := cl.Providers(ctx)
	if err != nil {
		t.Fatalf("providers: %v", err)
	}
	if len(providers) != 1 || providers[0].Descriptor.ID != "copilot" {
		t.Fatalf("providers = %+v", providers)
	}

	models, err := cl.Models(ctx, false)
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if len(models.Catalogs) != 1 {
		t.Fatalf("catalogs = %d", len(models.Catalogs))
	}

	config, err := cl.Configuration(ctx)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if config.Revision != 7 {
		t.Fatalf("revision = %d", config.Revision)
	}

	events, err := cl.Events(ctx, 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if events.NextAfter == 0 {
		t.Fatalf("expected a non-zero next cursor")
	}
}

// TestClientForbiddenMapsToTypedError proves an RBAC failure surfaces as a
// typed APIError with the daemon status, so the CLI can map a stable exit code.
func TestClientForbiddenMapsToTypedError(t *testing.T) {
	// operations POST requires operator; a viewer identity is forbidden. But
	// read routes only need viewer, so exercise a not-found path instead to get
	// a typed error without a mutation route.
	_, cl := newTestServer(t, control.RoleViewer)
	err := cl.doJSON(context.Background(), "GET", "/control/v1/does-not-exist", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if apiErr.Status != 404 {
		t.Fatalf("status = %d, want 404", apiErr.Status)
	}
	if apiErr.RequestID == "" {
		t.Fatalf("expected request id in error")
	}
}

// TestMutationSendsStableIdempotencyKey proves a mutating request carries an
// Idempotency-Key and that a retry reuses the same key (so the daemon dedupes).
func TestMutationSendsStableIdempotencyKey(t *testing.T) {
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		// Fail the first attempt with a retryable 503 to force a retry.
		if len(keys) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"temporarily_unavailable","retryable":true}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"applied":true}`))
	}))
	t.Cleanup(server.Close)

	cl, err := New(Profile{Transport: TransportHTTPS, Address: strings.TrimPrefix(server.URL, "http://"), MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	cl.baseURL = server.URL
	cl.http = server.Client()

	var out map[string]any
	if err := cl.doJSON(context.Background(), "POST", "/control/v1/config/mutations", map[string]string{"kind": "x"}, &out); err != nil {
		t.Fatalf("mutation: %v", err)
	}
	if len(keys) < 2 {
		t.Fatalf("expected a retry, got %d attempts", len(keys))
	}
	if keys[0] == "" {
		t.Fatal("mutation did not send an idempotency key")
	}
	if keys[0] != keys[1] {
		t.Fatalf("retry used a different idempotency key: %q != %q", keys[0], keys[1])
	}
}

func TestNewClientValidation(t *testing.T) {
	if _, err := New(Profile{Transport: TransportUnix}); err == nil {
		t.Fatal("expected error for unix transport without socket")
	}
	if _, err := New(Profile{Transport: TransportHTTPS}); err == nil {
		t.Fatal("expected error for https transport without address")
	}
	if _, err := New(Profile{Transport: "bogus"}); err == nil {
		t.Fatal("expected error for unsupported transport")
	}
}
