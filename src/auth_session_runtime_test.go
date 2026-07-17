package yarouter

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	operationpkg "github.com/duvu/ya-router/internal/operation"
	secretpkg "github.com/duvu/ya-router/internal/secret"
)

func TestKiloAnonymousAuthSessionPublishesRuntime(t *testing.T) {
	oldPath := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldPath })
	release, err := acquireManagedConfigState("auth-session-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release() })

	reloader := &recordingReloader{}
	runner := authSessionRunner{reloader: reloader}
	record := operationpkg.Record{Owner: "operator", Metadata: map[string]string{
		"provider_id": "kilo",
		"auth_method": "anonymous",
	}}
	result, failure := runner.run(context.Background(), discardOperationReporter{}, record)
	if failure != nil {
		t.Fatalf("failure = %+v", failure)
	}
	if result["runtime_published"] != "true" || reloader.calls != 1 {
		t.Fatalf("result=%v reconcile calls=%d", result, reloader.calls)
	}
	config := currentConfigState().Snapshot().Effective
	if !config.Providers.Kilo.Enabled || !config.Providers.Kilo.AllowAnonymous {
		t.Fatalf("kilo configuration = %+v", config.Providers.Kilo)
	}
}

func TestCopilotDeviceAuthSessionPublishesRuntime(t *testing.T) {
	oldPath := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldPath })
	release, err := acquireManagedConfigState("auth-session-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release() })
	oldClient := sharedHTTPClient
	sharedHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{}`
		switch request.URL.String() {
		case copilotDeviceCodeURL:
			body = `{"device_code":"device-code","user_code":"USER-CODE","verification_uri":"https://example.test/device","expires_in":600,"interval":0}`
		case copilotTokenURL:
			body = `{"access_token":"github-token"}`
		case copilotAPIKeyURL:
			body = `{"token":"copilot-token","expires_at":4102444800,"refresh_in":3600}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	t.Cleanup(func() { sharedHTTPClient = oldClient })

	store := secretpkg.NewMemoryStore(nil)
	reloader := &recordingReloader{}
	runner := authSessionRunner{reloader: reloader, secrets: store}
	record := operationpkg.Record{Owner: "operator", Metadata: map[string]string{
		"provider_id": "copilot",
		"auth_method": "device_code",
	}}
	result, failure := runner.run(context.Background(), discardOperationReporter{}, record)
	if failure != nil {
		t.Fatalf("failure = %+v", failure)
	}
	if result["runtime_published"] != "true" || reloader.calls != 1 {
		t.Fatalf("result=%v reconcile calls=%d", result, reloader.calls)
	}
	if _, _, ok := store.Resolve("copilot/github_token"); !ok {
		t.Fatal("GitHub credential was not stored")
	}
	if _, _, ok := store.Resolve("copilot/token"); !ok {
		t.Fatal("Copilot credential was not stored")
	}
	if !currentConfigState().Snapshot().Effective.Providers.Copilot.Enabled {
		t.Fatal("Copilot was not enabled")
	}
}

func TestCodexDeviceAuthSessionPublishesRuntime(t *testing.T) {
	oldPath := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldPath })
	release, err := acquireManagedConfigState("auth-session-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release() })
	oldClient := sharedHTTPClient
	sharedHTTPClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := `{}`
		switch request.URL.String() {
		case codexAuthIssuer + "/api/accounts/deviceauth/usercode":
			body = `{"device_auth_id":"device-auth","user_code":"USER-CODE","interval":"0"}`
		case codexAuthIssuer + "/api/accounts/deviceauth/token":
			body = `{"authorization_code":"authorization-code","code_verifier":"verifier"}`
		case codexAuthIssuer + "/oauth/token":
			body = `{"id_token":"id-token","access_token":"access-token","refresh_token":"refresh-token","expires_in":3600}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	t.Cleanup(func() { sharedHTTPClient = oldClient })

	store := secretpkg.NewMemoryStore(nil)
	reloader := &recordingReloader{}
	runner := authSessionRunner{reloader: reloader, secrets: store}
	record := operationpkg.Record{Owner: "operator", Metadata: map[string]string{
		"provider_id": "codex",
		"auth_method": "device_code",
	}}
	result, failure := runner.run(context.Background(), discardOperationReporter{}, record)
	if failure != nil {
		t.Fatalf("failure = %+v", failure)
	}
	if result["runtime_published"] != "true" || reloader.calls != 1 {
		t.Fatalf("result=%v reconcile calls=%d", result, reloader.calls)
	}
	if _, _, ok := store.Resolve("codex/access_token"); !ok {
		t.Fatal("Codex access credential was not stored")
	}
	if _, _, ok := store.Resolve("codex/refresh_token"); !ok {
		t.Fatal("Codex refresh credential was not stored")
	}
	config := currentConfigState().Snapshot().Effective
	if !config.Providers.Codex.Enabled || config.Providers.Codex.Auth.Mode != "chatgpt" {
		t.Fatalf("codex configuration = %+v", config.Providers.Codex)
	}
}

func TestCodexAPIKeyAuthSessionPublishesRuntime(t *testing.T) {
	oldPath := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldPath })
	release, err := acquireManagedConfigState("auth-session-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release() })
	store := secretpkg.NewMemoryStore(nil)
	if _, err := store.Set("operator", "codex/api_key", "managed-key"); err != nil {
		t.Fatal(err)
	}
	reloader := &recordingReloader{}
	runner := authSessionRunner{reloader: reloader, secrets: store}
	record := operationpkg.Record{Owner: "operator", Metadata: map[string]string{
		"provider_id": "codex",
		"auth_method": "api_key",
	}}
	result, failure := runner.run(context.Background(), discardOperationReporter{}, record)
	if failure != nil {
		t.Fatalf("failure = %+v", failure)
	}
	if result["runtime_published"] != "true" || reloader.calls != 1 {
		t.Fatalf("result=%v reconcile calls=%d", result, reloader.calls)
	}
	config := currentConfigState().Snapshot().Effective
	if !config.Providers.Codex.Enabled || config.Providers.Codex.Auth.Mode != "api_key" {
		t.Fatalf("codex configuration = %+v", config.Providers.Codex)
	}
}

type discardOperationReporter struct{}

func (discardOperationReporter) Progress(int, map[string]string) error  { return nil }
func (discardOperationReporter) WaitingForUser(map[string]string) error { return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
