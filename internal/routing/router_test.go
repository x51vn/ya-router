package routing

import (
	"context"
	"net/http"
	"testing"

	"github.com/duvu/ya-router/internal/api"
	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
)

type catalogProvider struct {
	id     provider.ID
	models []api.Model
}

func (p *catalogProvider) ID() provider.ID { return p.id }
func (p *catalogProvider) Name() string    { return string(p.id) }
func (p *catalogProvider) Capabilities() []provider.Capability {
	return []provider.Capability{provider.CapabilityChat}
}
func (p *catalogProvider) EnsureAuthenticated(context.Context) error { return nil }
func (p *catalogProvider) Health(context.Context) provider.Health {
	return provider.Health{Authenticated: true}
}
func (p *catalogProvider) ListModels(context.Context) (*api.ModelList, error) {
	return &api.ModelList{Object: "list", Data: p.models}, nil
}
func (p *catalogProvider) ProxyRequest(context.Context, http.ResponseWriter, *http.Request, []byte, provider.Capability) error {
	return nil
}

func TestRouterHonorsProviderPrefix(t *testing.T) {
	registry := provider.NewRegistry()
	registry.Register(&catalogProvider{id: provider.Codex, models: []api.Model{{ID: "gpt-test"}}})
	router := NewRouter(registry, configschema.Routing{})
	result, err := router.Resolve(context.Background(), "codex/gpt-test", provider.CapabilityChat)
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider.ID() != provider.Codex || result.ResolvedModel != "gpt-test" {
		t.Fatalf("result=%+v", result)
	}
}
