package control

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func newTestAPI(audit AuditSink, dataKey string) *API {
	return NewAPI(APIOptions{
		ServiceVersion:  "test",
		DeploymentMode:  "local",
		Features:        []string{"request_id", "rbac", "idempotency"},
		DataPlaneAPIKey: dataKey,
		Audit:           audit,
		State: func() StateMeta {
			return StateMeta{Revision: 7, RestartRequired: true}
		},
	})
}

func localAdmin() Authenticator {
	return FixedAuthenticator(Identity{Subject: "local:test", Role: RoleAdmin, Source: "unix"})
}

func TestMetaNegotiatesCurrentAndPreviousClientVersions(t *testing.T) {
	api := newTestAPI(nil, "")
	for _, version := range []string{CurrentClientVersion, PreviousSupportedClient} {
		request := httptest.NewRequest(http.MethodGet, "/control/v1/meta", nil)
		request.Header.Set(ClientVersionHeader, version)
		response := httptest.NewRecorder()
		api.Handler(localAdmin()).ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("version %s returned %d: %s", version, response.Code, response.Body.String())
		}
		var meta MetaResponse
		if err := json.Unmarshal(response.Body.Bytes(), &meta); err != nil {
			t.Fatal(err)
		}
		if !meta.Client.Compatible || meta.Client.Requested != version {
			t.Fatalf("unexpected compatibility for %s: %+v", version, meta.Client)
		}
		if meta.ConfigRevision != 7 || !meta.RestartRequired {
			t.Fatalf("state metadata missing: %+v", meta)
		}
	}
}

func TestMetaReportsUnsupportedClientWithoutBlockingDiscovery(t *testing.T) {
	api := newTestAPI(nil, "")
	request := httptest.NewRequest(http.MethodGet, "/control/v1/meta", nil)
	request.Header.Set(ClientVersionHeader, "2.0.0")
	response := httptest.NewRecorder()
	api.Handler(localAdmin()).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("meta discovery should remain available, got %d", response.Code)
	}
	var meta MetaResponse
	if err := json.Unmarshal(response.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Client.Compatible {
		t.Fatalf("unsupported client was accepted: %+v", meta.Client)
	}
}

func TestUnsupportedClientFailsBeforeNonMetaRoute(t *testing.T) {
	api := newTestAPI(nil, "")
	api.Handle(http.MethodGet, "/control/v1/test", RoleViewer, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	}))
	request := httptest.NewRequest(http.MethodGet, "/control/v1/test", nil)
	request.Header.Set(ClientVersionHeader, "2.0.0")
	response := httptest.NewRecorder()
	api.Handler(localAdmin()).ServeHTTP(response, request)
	if response.Code != http.StatusUpgradeRequired {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response, "unsupported_client_version")
}

func TestDataPlaneCredentialIsRejectedByControlPlane(t *testing.T) {
	api := newTestAPI(nil, "data-secret")
	request := httptest.NewRequest(http.MethodGet, "/control/v1/meta", nil)
	request.Header.Set("Authorization", "Bearer data-secret")
	response := httptest.NewRecorder()
	api.Handler(localAdmin()).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
	assertErrorCode(t, response, "data_plane_credential_rejected")
}

func TestUnauthorizedAndForbiddenAreDistinctAndRedactedInAudit(t *testing.T) {
	audit := NewMemoryAuditSink(10)
	api := newTestAPI(audit, "data-secret")
	api.Handle(http.MethodPost, "/control/v1/admin-only", RoleAdmin, false, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))

	unauthorized := httptest.NewRequest(http.MethodGet, "/control/v1/meta", nil)
	unauthorized.Header.Set("Authorization", "Bearer top-secret-invalid")
	unauthorizedResponse := httptest.NewRecorder()
	api.Handler(NewTokenAuthenticator(map[Role]string{RoleViewer: "viewer-secret"})).ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", unauthorizedResponse.Code)
	}

	forbidden := httptest.NewRequest(http.MethodPost, "/control/v1/admin-only", strings.NewReader(`{"api_key":"must-not-audit"}`))
	forbidden.Header.Set("Authorization", "Bearer viewer-secret")
	forbiddenResponse := httptest.NewRecorder()
	api.Handler(NewTokenAuthenticator(map[Role]string{RoleViewer: "viewer-secret"})).ServeHTTP(forbiddenResponse, forbidden)
	if forbiddenResponse.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", forbiddenResponse.Code, forbiddenResponse.Body.String())
	}

	events := audit.Snapshot()
	if len(events) != 2 || events[0].Status != http.StatusUnauthorized || events[1].Status != http.StatusForbidden {
		t.Fatalf("unexpected audit events: %+v", events)
	}
	payload, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"top-secret-invalid", "viewer-secret", "must-not-audit", "data-secret"} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("audit payload leaked %q: %s", secret, payload)
		}
	}
	if events[1].Actor != "control-token:viewer" || events[1].Role != RoleViewer {
		t.Fatalf("authenticated audit identity missing: %+v", events[1])
	}
}

func TestRequestIDIsStableInHeaderAndTypedError(t *testing.T) {
	api := newTestAPI(nil, "")
	request := httptest.NewRequest(http.MethodGet, "/control/v1/missing", nil)
	request.Header.Set(RequestIDHeader, "request-12345678")
	response := httptest.NewRecorder()
	api.Handler(localAdmin()).ServeHTTP(response, request)
	if response.Header().Get(RequestIDHeader) != "request-12345678" {
		t.Fatalf("request ID header mismatch: %q", response.Header().Get(RequestIDHeader))
	}
	var envelope ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.RequestID != "request-12345678" {
		t.Fatalf("typed error request ID mismatch: %+v", envelope)
	}
}

func TestIdempotencyReplaysAndRejectsPayloadConflict(t *testing.T) {
	api := newTestAPI(nil, "")
	var calls atomic.Int32
	api.Handle(http.MethodPost, "/control/v1/mutate", RoleOperator, true, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writeJSON(writer, http.StatusCreated, map[string]int{"call": int(calls.Load())})
	}))
	handler := api.Handler(FixedAuthenticator(Identity{Subject: "operator:test", Role: RoleOperator, Source: "test"}))

	perform := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/control/v1/mutate", strings.NewReader(body))
		request.Header.Set(IdempotencyKeyHeader, "same-operation")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	first := perform(`{"enabled":true}`)
	second := perform(`{"enabled":true}`)
	if first.Code != http.StatusCreated || second.Code != http.StatusCreated || first.Body.String() != second.Body.String() {
		t.Fatalf("idempotent replay mismatch: first=%d/%s second=%d/%s", first.Code, first.Body, second.Code, second.Body)
	}
	if calls.Load() != 1 {
		t.Fatalf("mutation executed %d times", calls.Load())
	}
	conflict := perform(`{"enabled":false}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("expected conflict, got %d: %s", conflict.Code, conflict.Body.String())
	}
	assertErrorCode(t, conflict, "idempotency_key_conflict")
}

func TestCertificateAuthenticatorMapsVerifiedIdentity(t *testing.T) {
	certificate := &x509.Certificate{Subject: pkix.Name{CommonName: "operator.example"}}
	request := httptest.NewRequest(http.MethodGet, "/control/v1/meta", nil)
	request.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{certificate},
		VerifiedChains:   [][]*x509.Certificate{{certificate}},
	}
	identity, ok := (CertificateAuthenticator{SubjectRoles: map[string]Role{"operator.example": RoleOperator}}).Authenticate(request)
	if !ok || identity.Role != RoleOperator || identity.Source != "mtls" {
		t.Fatalf("unexpected mTLS identity: %+v %t", identity, ok)
	}
}

func TestNonLoopbackPlaintextControlBindingIsRejected(t *testing.T) {
	config := ListenerConfig{
		RemoteAddress:            "0.0.0.0:7443",
		RemoteIdentityConfigured: true,
		RequireMTLS:              true,
		ClientCAFile:             "ca.pem",
	}
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "TLS certificate and key") {
		t.Fatalf("expected TLS validation error, got %v", err)
	}
	config.TLSCertFile = "server.pem"
	config.TLSKeyFile = "server-key.pem"
	config.RequireMTLS = false
	if err := config.Validate(); err == nil || !strings.Contains(err.Error(), "requires mTLS") {
		t.Fatalf("expected mTLS validation error, got %v", err)
	}
}

func TestUnixSocketServiceLifecycle(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "control.sock")
	api := newTestAPI(nil, "")
	service, err := NewService(ListenerConfig{UnixSocket: socket}, api.Handler(localAdmin()), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}
	response, err := client.Get("http://unix/control/v1/meta")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("got %d: %s", response.StatusCode, body)
	}
	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected socket mode: %o", info.Mode().Perm())
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket was not removed: %v", err)
	}
}

func TestUnixSocketServiceAppliesGroupOverride(t *testing.T) {
	currentUser, err := user.Current()
	if err != nil {
		t.Skip("cannot resolve current user in this environment")
	}
	group, err := user.LookupGroupId(currentUser.Gid)
	if err != nil {
		t.Skip("cannot resolve current group in this environment")
	}

	socket := filepath.Join(t.TempDir(), "control.sock")
	api := newTestAPI(nil, "")
	service, err := NewService(
		ListenerConfig{UnixSocket: socket, UnixGroup: group.Name},
		api.Handler(localAdmin()),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = service.Shutdown(shutdownContext)
	}()

	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("cannot inspect socket group ownership on this platform")
	}
	wantGid, err := strconv.Atoi(currentUser.Gid)
	if err != nil {
		t.Fatal(err)
	}
	if int(stat.Gid) != wantGid {
		t.Fatalf("expected socket gid %d, got %d", wantGid, stat.Gid)
	}
}

func TestUnixSocketServiceRejectsUnknownGroup(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "control.sock")
	api := newTestAPI(nil, "")
	service, err := NewService(
		ListenerConfig{UnixSocket: socket, UnixGroup: "definitely-not-a-real-group"},
		api.Handler(localAdmin()),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err == nil {
		t.Fatal("expected Start to fail for an unresolvable group")
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket should have been cleaned up after failed group resolution: %v", err)
	}
}

func assertErrorCode(t *testing.T, response *httptest.ResponseRecorder, code string) {
	t.Helper()
	var envelope ErrorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v: %s", err, response.Body.String())
	}
	if envelope.Error.Code != code {
		t.Fatalf("expected error %q, got %+v", code, envelope.Error)
	}
}
