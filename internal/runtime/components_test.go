package runtime

import (
	"testing"

	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
	"github.com/duvu/ya-router/internal/routing"
)

func TestNewComponentsRequiresExplicitDependencies(t *testing.T) {
	registry := provider.NewRegistry()
	config := &configschema.Config{}
	router := routing.NewRouter(registry, configschema.Routing{})
	components, err := NewComponents(config, registry, router)
	if err != nil {
		t.Fatal(err)
	}
	if components.Config != config || components.Providers != registry || components.Router != router {
		t.Fatal("composition did not retain explicit dependencies")
	}
	if _, err := NewComponents(nil, registry, router); err == nil {
		t.Fatal("nil config should fail")
	}
}
