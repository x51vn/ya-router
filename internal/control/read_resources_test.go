package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	providerpkg "github.com/duvu/ya-router/internal/provider"
)

type mockReadModel struct {
	providers []ProviderResource
	accounts  []AccountResource
	events    []providerpkg.LifecycleEvent
}

func (model *mockReadModel) Providers(context.Context) ([]ProviderResource, error) {
	return append([]ProviderResource(nil), model.providers...), nil
}
func (model *mockReadModel) Accounts(context.Context) ([]AccountResource, error) {
	return append([]AccountResource(nil), model.accounts...), nil
}
func (model *mockReadModel) Models(context.Context, bool) (ModelCatalogResponse, error) {
	return ModelCatalogResponse{Catalogs: []ProviderCatalog{}}, nil
}
func (model *mockReadModel) Configuration(context.Context) (ConfigResource, error) {
	return ConfigResource{}, nil
}
func (model *mockReadModel) Operations(context.Context) ([]OperationResource, error) {
	return []OperationResource{}, nil
}
func (model *mockReadModel) Events(after uint64) []providerpkg.LifecycleEvent {
	var result []providerpkg.LifecycleEvent
	for _, event := range model.events {
		if event.Sequence > after {
			result = append(result, event)
		}
	}
	return result
}
func (model *mockReadModel) SubscribeEvents(int) (<-chan providerpkg.LifecycleEvent, func()) {
	stream := make(chan providerpkg.LifecycleEvent)
	close(stream)
	return stream, func() {}
}

func TestReadProviderResourceReturnsAllCompiledProviders(t *testing.T) {
	model := &mockReadModel{providers: []ProviderResource{
		{Descriptor: providerpkg.Descriptor{ID: providerpkg.Copilot, Name: "GitHub Copilot"}},
		{Descriptor: providerpkg.Descriptor{ID: providerpkg.Codex, Name: "OpenAI Codex"}},
		{Descriptor: providerpkg.Descriptor{ID: providerpkg.Kilo, Name: "Kilo Gateway"}},
	}}
	api := NewAPI(APIOptions{})
	RegisterReadRoutes(api, model)
	request := httptest.NewRequest(http.MethodGet, "/control/v1/providers", nil)
	response := httptest.NewRecorder()
	api.Handler(localReadTestIdentity()).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("got %d: %s", response.Code, response.Body.String())
	}
	var payload struct {
		Data []ProviderResource `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 3 || payload.Data[0].Descriptor.ID != providerpkg.Copilot || payload.Data[1].Descriptor.ID != providerpkg.Codex || payload.Data[2].Descriptor.ID != providerpkg.Kilo {
		t.Fatalf("unexpected providers: %+v", payload.Data)
	}
}

func TestEventPollingAndSSEResumeFromLastEventID(t *testing.T) {
	model := &mockReadModel{events: []providerpkg.LifecycleEvent{
		{Sequence: 1, Type: providerpkg.EventFactoryRegistered, ProviderID: providerpkg.Copilot, Timestamp: time.Unix(1, 0)},
		{Sequence: 2, Type: providerpkg.EventPublished, ProviderID: providerpkg.Codex, Timestamp: time.Unix(2, 0)},
	}}
	api := NewAPI(APIOptions{})
	RegisterReadRoutes(api, model)

	poll := httptest.NewRequest(http.MethodGet, "/control/v1/events?after=1", nil)
	pollResponse := httptest.NewRecorder()
	api.Handler(localReadTestIdentity()).ServeHTTP(pollResponse, poll)
	var page EventPage
	if err := json.Unmarshal(pollResponse.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].Sequence != 2 || page.NextAfter != 2 {
		t.Fatalf("unexpected polling page: %+v", page)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream := httptest.NewRequest(http.MethodGet, "/control/v1/events/stream", nil).WithContext(ctx)
	stream.Header.Set("Last-Event-ID", "1")
	streamResponse := httptest.NewRecorder()
	api.Handler(localReadTestIdentity()).ServeHTTP(streamResponse, stream)
	body := streamResponse.Body.String()
	if strings.Contains(body, "id: 1\n") || !strings.Contains(body, "id: 2\n") {
		t.Fatalf("SSE did not resume correctly: %s", body)
	}
}

func TestAccountsExposeOnlyRedactedCredentialMetadata(t *testing.T) {
	model := &mockReadModel{accounts: []AccountResource{{
		ProviderID: providerpkg.Codex,
		ID:         "acct_daemon_owned",
		Label:      "primary",
		Credential: CredentialMetadata{Configured: true, Source: "secret_store:service", Refreshable: true},
	}}}
	api := NewAPI(APIOptions{})
	RegisterReadRoutes(api, model)
	request := httptest.NewRequest(http.MethodGet, "/control/v1/accounts", nil)
	response := httptest.NewRecorder()
	api.Handler(localReadTestIdentity()).ServeHTTP(response, request)
	body := response.Body.String()
	for _, forbidden := range []string{"access_token", "refresh_token", "api_key", "upstream_account_id"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("account response leaked forbidden field %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "secret_store:service") {
		t.Fatalf("credential source metadata missing: %s", body)
	}
}

func localReadTestIdentity() Authenticator {
	return FixedAuthenticator(Identity{Subject: "local:test", Role: RoleAdmin, Source: "test"})
}
