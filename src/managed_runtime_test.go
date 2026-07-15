package yarouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	providerpkg "github.com/duvu/ya-router/internal/provider"
	runtimepkg "github.com/duvu/ya-router/internal/runtime"
)

func TestManagedProxyHandlerPinsRequestSnapshot(t *testing.T) {
	config := defaultConfig()
	config.Timeouts.ProxyContext = 2
	config.Routing.DefaultProvider = string(ProviderCopilot)
	started := make(chan struct{})
	allowOldRequest := make(chan struct{})
	oldProvider := &mockProvider{
		id:     ProviderCopilot,
		name:   "old",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "old"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			close(started)
			<-allowOldRequest
			w.WriteHeader(http.StatusOK)
			return nil
		},
	}
	runtimeManager, err := runtimepkg.NewManager(config, oldProvider)
	if err != nil {
		t.Fatal(err)
	}
	handler := managedProxyHandler(runtimeManager)

	oldDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"github/old"}`))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		oldDone <- recorder
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old request did not reach its provider")
	}

	newProvider := &mockProvider{
		id:     ProviderCopilot,
		name:   "new",
		caps:   []Capability{CapabilityChat},
		health: ProviderHealth{Authenticated: true},
		models: &ModelList{Object: "list", Data: []Model{{ID: "new"}}},
		proxyFunc: func(_ context.Context, w http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			w.WriteHeader(http.StatusNoContent)
			return nil
		},
	}
	publication, err := runtimeManager.PublishProviders([]providerpkg.Provider{newProvider})
	if err != nil {
		t.Fatal(err)
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	if err := publication.Retirement.Wait(waitContext); err == nil {
		cancel()
		t.Fatal("old HTTP request did not retain its runtime snapshot")
	}
	cancel()

	newRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"github/new"}`))
	newRecorder := httptest.NewRecorder()
	handler.ServeHTTP(newRecorder, newRequest)
	if newRecorder.Code != http.StatusNoContent {
		t.Fatalf("new request status = %d, want %d", newRecorder.Code, http.StatusNoContent)
	}

	close(allowOldRequest)
	select {
	case oldRecorder := <-oldDone:
		if oldRecorder.Code != http.StatusOK {
			t.Fatalf("old request status = %d, want %d", oldRecorder.Code, http.StatusOK)
		}
	case <-time.After(time.Second):
		t.Fatal("old request did not finish")
	}
	waitContext, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := publication.Retirement.Wait(waitContext); err != nil {
		t.Fatalf("snapshot did not drain: %v", err)
	}
}

func TestCompiledProviderDescriptorsIncludeDisabledProviders(t *testing.T) {
	config := defaultConfig()
	config.Providers.Copilot.Enabled = false
	config.Providers.Codex.Enabled = false
	config.Providers.Kilo.Enabled = true
	runtimeManager, err := runtimepkg.NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	providerManager, err := newProviderManager(config, runtimeManager)
	if err != nil {
		t.Fatal(err)
	}
	statuses := providerManager.List()
	if len(statuses) != 3 {
		t.Fatalf("provider descriptors = %d, want 3", len(statuses))
	}
	enabled := 0
	for _, status := range statuses {
		if status.Descriptor.SchemaVersion != 1 {
			t.Fatalf("provider %q schema version = %d", status.Descriptor.ID, status.Descriptor.SchemaVersion)
		}
		if status.Enabled {
			enabled++
			if status.Descriptor.ID != ProviderKilo {
				t.Fatalf("unexpected enabled provider %q", status.Descriptor.ID)
			}
		}
	}
	if enabled != 1 {
		t.Fatalf("enabled provider count = %d", enabled)
	}
}
