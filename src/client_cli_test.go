package yarouter

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	clientpkg "github.com/duvu/ya-router/internal/client"
	controlpkg "github.com/duvu/ya-router/internal/control"
	providerpkg "github.com/duvu/ya-router/internal/provider"
)

// startUnixControlServer serves the real control API over a Unix socket so the
// client transport path (the production default) is exercised end-to-end.
func startUnixControlServer(t *testing.T) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	api := controlpkg.NewAPI(controlpkg.APIOptions{ServiceVersion: "test"})
	controlpkg.RegisterReadRoutes(api, clientTestReadModel{})
	identity := controlpkg.Identity{Subject: "local:test", Role: controlpkg.RoleAdmin, Source: "test"}
	server := &http.Server{Handler: api.Handler(controlpkg.FixedAuthenticator(identity))}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	})
	return socket
}

func TestClientProfileOverUnixSocket(t *testing.T) {
	socket := startUnixControlServer(t)
	profile, err := resolveClientProfile(clientCommonFlags{socket: socket})
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if profile.Transport != clientpkg.TransportUnix || profile.Socket != socket {
		t.Fatalf("profile = %+v", profile)
	}
	cl, err := clientpkg.New(profile)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Every read command must return a decodable result without a TTY.
	for _, command := range []string{"meta", "providers", "accounts", "models", "config", "operations", "events"} {
		result, err := dispatchClientRead(ctx, cl, command, clientCommonFlags{})
		if err != nil {
			t.Fatalf("command %q: %v", command, err)
		}
		// JSON output must round-trip (contract test for --json).
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("command %q json: %v", command, err)
		}
		if len(encoded) == 0 {
			t.Fatalf("command %q produced empty json", command)
		}
	}
}

func TestClientProfileResolutionPrecedence(t *testing.T) {
	// Explicit address beats socket.
	profile, err := resolveClientProfile(clientCommonFlags{socket: "/tmp/s.sock", address: "127.0.0.1:9000"})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Transport != clientpkg.TransportHTTPS {
		t.Fatalf("expected https transport, got %s", profile.Transport)
	}
}

func TestClientSecretSetRequiresStdin(t *testing.T) {
	if got := runClientSecretCommand("secret-set", []string{"--slot", "kilo/api_key"}); got != clientExitUsage {
		t.Fatalf("exit code = %d, want %d", got, clientExitUsage)
	}
}

// clientTestReadModel is a minimal control.ReadModel for the CLI transport test.
type clientTestReadModel struct{}

func (clientTestReadModel) Providers(context.Context) ([]controlpkg.ProviderResource, error) {
	return []controlpkg.ProviderResource{{Enabled: true}}, nil
}
func (clientTestReadModel) Accounts(context.Context) ([]controlpkg.AccountResource, error) {
	return []controlpkg.AccountResource{}, nil
}
func (clientTestReadModel) Models(context.Context, bool) (controlpkg.ModelCatalogResponse, error) {
	return controlpkg.ModelCatalogResponse{Catalogs: []controlpkg.ProviderCatalog{}}, nil
}
func (clientTestReadModel) Configuration(context.Context) (controlpkg.ConfigResource, error) {
	return controlpkg.ConfigResource{Revision: 1}, nil
}
func (clientTestReadModel) Operations(context.Context) ([]controlpkg.OperationResource, error) {
	return []controlpkg.OperationResource{}, nil
}
func (clientTestReadModel) Events(after uint64) []providerpkg.LifecycleEvent {
	events := []providerpkg.LifecycleEvent{{Sequence: 1, Type: "provider_published"}}
	var result []providerpkg.LifecycleEvent
	for _, event := range events {
		if event.Sequence > after {
			result = append(result, event)
		}
	}
	return result
}
func (clientTestReadModel) SubscribeEvents(int) (<-chan providerpkg.LifecycleEvent, func()) {
	stream := make(chan providerpkg.LifecycleEvent)
	close(stream)
	return stream, func() {}
}
