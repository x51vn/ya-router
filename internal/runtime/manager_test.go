package runtime

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/duvu/ya-router/internal/api"
	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
)

type runtimeTestProvider struct {
	id    provider.ID
	model string
}

func (registered *runtimeTestProvider) ID() provider.ID { return registered.id }
func (registered *runtimeTestProvider) Name() string    { return string(registered.id) }
func (registered *runtimeTestProvider) Capabilities() []provider.Capability {
	return []provider.Capability{provider.CapabilityChat}
}
func (registered *runtimeTestProvider) EnsureAuthenticated(context.Context) error { return nil }
func (registered *runtimeTestProvider) ListModels(context.Context) (*api.ModelList, error) {
	return &api.ModelList{Object: "list", Data: []api.Model{{ID: registered.model}}}, nil
}
func (registered *runtimeTestProvider) ProxyRequest(context.Context, http.ResponseWriter, *http.Request, []byte, provider.Capability) error {
	return nil
}
func (registered *runtimeTestProvider) Health(context.Context) provider.Health {
	return provider.Health{Authenticated: true}
}

func TestLeaseRetainsSnapshotAcrossPublication(t *testing.T) {
	config := &configschema.Config{Routing: configschema.Routing{DefaultProvider: string(provider.Copilot)}}
	oldProvider := &runtimeTestProvider{id: provider.Copilot, model: "old"}
	manager, err := NewManager(config, oldProvider)
	if err != nil {
		t.Fatal(err)
	}

	oldLease, err := manager.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if !oldLease.Snapshot().Providers().Frozen() {
		t.Fatal("published provider registry is mutable")
	}
	firstPublication, err := manager.PublishProviders([]provider.Provider{oldProvider})
	if err != nil {
		t.Fatal(err)
	}
	sharedLease, err := manager.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	newProvider := &runtimeTestProvider{id: provider.Copilot, model: "new"}
	publication, err := manager.PublishProviders([]provider.Provider{newProvider})
	if err != nil {
		t.Fatal(err)
	}
	newLease, err := manager.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer newLease.Release()

	oldRoute, err := oldLease.Snapshot().Router().Resolve(context.Background(), "github/old", provider.CapabilityChat)
	if err != nil || oldRoute.Provider != oldProvider {
		t.Fatalf("old snapshot route = %#v, %v", oldRoute, err)
	}
	newRoute, err := newLease.Snapshot().Router().Resolve(context.Background(), "github/new", provider.CapabilityChat)
	if err != nil || newRoute.Provider != newProvider {
		t.Fatalf("new snapshot route = %#v, %v", newRoute, err)
	}

	waitContext, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	if err := publication.Retirement.Wait(waitContext); err == nil {
		cancel()
		t.Fatal("retirement completed while old leases were active")
	}
	cancel()
	sharedLease.Release()
	waitContext, cancel = context.WithTimeout(context.Background(), 5*time.Millisecond)
	if err := publication.Retirement.Wait(waitContext); err == nil {
		cancel()
		t.Fatal("retirement ignored an older snapshot sharing the provider")
	}
	cancel()
	oldLease.Release()
	waitContext, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := publication.Retirement.Wait(waitContext); err != nil {
		t.Fatalf("retirement after release: %v", err)
	}
	if err := firstPublication.Retirement.Wait(waitContext); err != nil {
		t.Fatalf("first retirement after release: %v", err)
	}
}

func TestConcurrentAcquirePublishListAndRoute(t *testing.T) {
	config := &configschema.Config{Routing: configschema.Routing{DefaultProvider: string(provider.Copilot)}}
	manager, err := NewManager(config, &runtimeTestProvider{id: provider.Copilot, model: "model-0"})
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errorsFound := make(chan error, 32)
	for reader := 0; reader < 8; reader++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 200; iteration++ {
				lease, acquireErr := manager.Acquire()
				if acquireErr != nil {
					errorsFound <- acquireErr
					return
				}
				snapshot := lease.Snapshot()
				providers := snapshot.Providers().All()
				if len(providers) != 1 {
					errorsFound <- fmt.Errorf("providers=%d", len(providers))
					lease.Release()
					return
				}
				models, listErr := providers[0].ListModels(context.Background())
				if listErr != nil || len(models.Data) != 1 {
					errorsFound <- fmt.Errorf("list models: %v", listErr)
					lease.Release()
					return
				}
				if _, routeErr := snapshot.Router().Resolve(context.Background(), "github/"+models.Data[0].ID, provider.CapabilityChat); routeErr != nil {
					errorsFound <- routeErr
					lease.Release()
					return
				}
				lease.Release()
			}
		}()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		for iteration := 1; iteration <= 200; iteration++ {
			_, publishErr := manager.PublishProviders([]provider.Provider{
				&runtimeTestProvider{id: provider.Copilot, model: fmt.Sprintf("model-%d", iteration)},
			})
			if publishErr != nil {
				errorsFound <- publishErr
				return
			}
		}
	}()
	wait.Wait()
	close(errorsFound)
	for found := range errorsFound {
		t.Error(found)
	}
}

func TestRuntimeSnapshotDeepCopiesConfig(t *testing.T) {
	config := &configschema.Config{Routing: configschema.Routing{
		DefaultModel: "original",
		ModelMap: map[string]configschema.ModelMapEntry{
			"alias": {Provider: string(provider.Copilot)},
		},
	}}
	manager, err := NewManager(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Routing.DefaultModel = "changed"
	config.Routing.ModelMap["alias"] = configschema.ModelMapEntry{Provider: string(provider.Kilo)}
	lease, err := manager.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	exposed := lease.Snapshot().Config()
	if exposed.Routing.DefaultModel != "original" || exposed.Routing.ModelMap["alias"].Provider != string(provider.Copilot) {
		t.Fatal("published config changed with caller-owned config")
	}
	exposed.Routing.DefaultModel = "caller-mutation"
	if lease.Snapshot().Config().Routing.DefaultModel != "original" {
		t.Fatal("snapshot exposed mutable effective config")
	}
}

func TestRetirementPendingTracksOutOfOrderDrains(t *testing.T) {
	config := &configschema.Config{}
	first := newSnapshot(1, config, nil)
	second := newSnapshot(2, config, nil)
	if !first.tryAcquire() || !second.tryAcquire() {
		t.Fatal("failed to acquire test snapshots")
	}
	first.retire()
	second.retire()
	retirement := newRetirement([]*Snapshot{first, second})
	if got := retirement.Pending(); got != 2 {
		t.Fatalf("initial pending snapshots = %d, want 2", got)
	}

	second.release()
	if got := retirement.Pending(); got != 1 {
		t.Fatalf("pending after second snapshot drained first = %d, want 1", got)
	}
	first.release()
	waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := retirement.Wait(waitContext); err != nil {
		t.Fatalf("retirement wait: %v", err)
	}
	if got := retirement.Pending(); got != 0 {
		t.Fatalf("final pending snapshots = %d, want 0", got)
	}
}
