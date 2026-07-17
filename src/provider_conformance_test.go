package yarouter

import (
	"context"
	"testing"

	providerpkg "github.com/duvu/ya-router/internal/provider"
)

func TestCompiledProviderFactoriesConformToDescriptors(t *testing.T) {
	config := defaultConfig()
	for _, factory := range compiledProviderFactories() {
		descriptor := factory.Descriptor()
		t.Run(string(descriptor.ID), func(t *testing.T) {
			runProviderConformance(t, descriptor, factory, config)
		})
	}
}

func TestReferenceProviderConformsToDescriptor(t *testing.T) {
	descriptor := providerpkg.Descriptor{
		ID: "reference", Name: "Reference", Capabilities: []Capability{CapabilityChat},
		AuthMethods: []providerpkg.AuthMethod{providerpkg.AuthAnonymous}, SchemaVersion: 1,
	}
	provider := &mockProvider{id: "reference", name: "Reference", caps: []Capability{CapabilityChat}}
	if err := providerpkg.ValidateDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	runProviderContract(t, descriptor, provider)
}

func runProviderConformance(t *testing.T, descriptor providerpkg.Descriptor, factory providerpkg.Factory, config *Config) {
	t.Helper()
	if err := providerpkg.ValidateDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	if err := factory.ValidateConfig(config); err != nil {
		t.Fatal(err)
	}
	provider, err := factory.Build(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if err := factory.ValidateProvider(context.Background(), provider); err != nil {
		t.Fatal(err)
	}
	runProviderContract(t, descriptor, provider)
}

func runProviderContract(t *testing.T, descriptor providerpkg.Descriptor, provider providerpkg.Provider) {
	t.Helper()
	if provider.ID() != descriptor.ID {
		t.Fatalf("provider ID = %q, descriptor ID = %q", provider.ID(), descriptor.ID)
	}
	if provider.Name() == "" || provider.Name() != descriptor.Name {
		t.Fatalf("provider name = %q, descriptor name = %q", provider.Name(), descriptor.Name)
	}
	if !capabilitiesSubset(provider.Capabilities(), descriptor.Capabilities) {
		t.Fatalf("provider capabilities = %v, descriptor capabilities = %v", provider.Capabilities(), descriptor.Capabilities)
	}
	_ = provider.Health(context.Background())
}

func capabilitiesSubset(actual, supported []Capability) bool {
	supportedSet := make(map[Capability]struct{}, len(supported))
	for _, capability := range supported {
		supportedSet[capability] = struct{}{}
	}
	for _, capability := range actual {
		if _, ok := supportedSet[capability]; !ok {
			return false
		}
	}
	return len(actual) > 0
}
