package provider_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/duvu/ya-router/internal/api"
	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
	runtimepkg "github.com/duvu/ya-router/internal/runtime"
)

type managedTestProvider struct {
	model     string
	closed    atomic.Bool
	closedCh  chan struct{}
	closeOnce sync.Once
	closeGate <-chan struct{}
}

func newManagedTestProvider(model string) *managedTestProvider {
	return &managedTestProvider{model: model, closedCh: make(chan struct{})}
}

func (registered *managedTestProvider) ID() provider.ID { return provider.Copilot }
func (registered *managedTestProvider) Name() string    { return "managed-test" }
func (registered *managedTestProvider) Capabilities() []provider.Capability {
	return []provider.Capability{provider.CapabilityChat}
}
func (registered *managedTestProvider) EnsureAuthenticated(context.Context) error { return nil }
func (registered *managedTestProvider) ListModels(context.Context) (*api.ModelList, error) {
	if registered.closed.Load() {
		return nil, errors.New("provider used after close")
	}
	return &api.ModelList{Object: "list", Data: []api.Model{{ID: registered.model}}}, nil
}
func (registered *managedTestProvider) ProxyRequest(context.Context, http.ResponseWriter, *http.Request, []byte, provider.Capability) error {
	if registered.closed.Load() {
		return errors.New("provider used after close")
	}
	return nil
}
func (registered *managedTestProvider) Health(context.Context) provider.Health {
	return provider.Health{Authenticated: !registered.closed.Load()}
}
func (registered *managedTestProvider) Close(context.Context) error {
	if registered.closeGate != nil {
		<-registered.closeGate
	}
	registered.closeOnce.Do(func() {
		registered.closed.Store(true)
		close(registered.closedCh)
	})
	return nil
}

func managedBlockingFactory(closeGate <-chan struct{}) provider.Factory {
	return provider.FactoryFuncs{
		ProviderDescriptor: provider.Descriptor{
			ID:            provider.Copilot,
			Name:          "Blocking managed test",
			Capabilities:  []provider.Capability{provider.CapabilityChat},
			AuthMethods:   []provider.AuthMethod{provider.AuthDeviceCode},
			SchemaVersion: 1,
		},
		ValidateConfigFunc: func(config any) error {
			if _, ok := config.(string); !ok {
				return errors.New("string config is required")
			}
			return nil
		},
		BuildFunc: func(_ context.Context, config any) (provider.Provider, error) {
			registered := newManagedTestProvider(config.(string))
			registered.closeGate = closeGate
			return registered, nil
		},
	}
}

func managedTestFactory() provider.Factory {
	return provider.FactoryFuncs{
		ProviderDescriptor: provider.Descriptor{
			ID:            provider.Copilot,
			Name:          "Managed test",
			Capabilities:  []provider.Capability{provider.CapabilityChat},
			AuthMethods:   []provider.AuthMethod{provider.AuthDeviceCode},
			MultiAccount:  true,
			SchemaVersion: 1,
			ConfigSchema:  []provider.ConfigField{{Name: "model", Type: provider.ConfigString}},
		},
		ValidateConfigFunc: func(config any) error {
			model, ok := config.(string)
			if !ok || model == "" {
				return errors.New("model is required")
			}
			if model == "config-fail" {
				return errors.New("rejected config")
			}
			return nil
		},
		BuildFunc: func(_ context.Context, config any) (provider.Provider, error) {
			model := config.(string)
			if model == "build-fail" {
				return nil, errors.New("construction failed")
			}
			return newManagedTestProvider(model), nil
		},
		ValidateFunc: func(_ context.Context, registered provider.Provider) error {
			if registered.(*managedTestProvider).model == "validation-fail" {
				return errors.New("instance not ready")
			}
			return nil
		},
	}
}

func newManagedTestManagers(t *testing.T, drainTimeout time.Duration) (*runtimepkg.Manager, *provider.Manager) {
	t.Helper()
	runtimeManager, err := runtimepkg.NewManager(&configschema.Config{Routing: configschema.Routing{DefaultProvider: string(provider.Copilot)}})
	if err != nil {
		t.Fatal(err)
	}
	providerManager, err := provider.NewManager(runtimeManager, provider.NewHealthRegistry(), provider.NewEventBus(64), provider.ManagerOptions{
		DrainTimeout: drainTimeout,
		CloseTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := providerManager.RegisterFactory(managedTestFactory()); err != nil {
		t.Fatal(err)
	}
	return runtimeManager, providerManager
}

func TestFailedReplacementLeavesOldProviderActive(t *testing.T) {
	runtimeManager, providerManager := newManagedTestManagers(t, time.Second)
	desired := []provider.DesiredProvider{{
		ID:       provider.Copilot,
		Enabled:  true,
		Config:   "old",
		Accounts: []provider.AccountState{{ID: "account-1", Label: "Primary", Enabled: true}},
	}}
	if _, err := providerManager.Reconcile(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	old := providerManager.ActiveProviders()[0]
	for _, rejected := range []string{"config-fail", "build-fail", "validation-fail"} {
		if _, err := providerManager.Replace(context.Background(), provider.Copilot, rejected); err == nil {
			t.Fatalf("replacement %q should fail", rejected)
		}
		lease, err := runtimeManager.Acquire()
		if err != nil {
			t.Fatal(err)
		}
		registered, err := lease.Snapshot().Providers().Get(provider.Copilot)
		lease.Release()
		if err != nil || registered != old {
			t.Fatalf("old provider changed after %q failure: %v", rejected, err)
		}
	}
	statuses := providerManager.List()
	if len(statuses) != 1 || len(statuses[0].Accounts) != 1 || statuses[0].Accounts[0].ID != "account-1" {
		t.Fatalf("reconciled account state = %#v", statuses)
	}
	statuses[0].Accounts[0].ID = "mutated"
	if providerManager.List()[0].Accounts[0].ID != "account-1" {
		t.Fatal("List returned mutable account state")
	}
}

func TestDrainIsBoundedObservableAndClosesAfterLeaseRelease(t *testing.T) {
	runtimeManager, providerManager := newManagedTestManagers(t, 10*time.Millisecond)
	if _, err := providerManager.Reconcile(context.Background(), []provider.DesiredProvider{{ID: provider.Copilot, Enabled: true, Config: "old"}}); err != nil {
		t.Fatal(err)
	}
	old := providerManager.ActiveProviders()[0].(*managedTestProvider)
	lease, err := runtimeManager.Acquire()
	if err != nil {
		t.Fatal(err)
	}

	report, err := providerManager.Replace(context.Background(), provider.Copilot, "new")
	if err != nil {
		t.Fatal(err)
	}
	if !report.TimedOut || report.Completed || report.PendingSnapshots == 0 {
		t.Fatalf("unexpected bounded drain report: %#v", report)
	}
	if old.closed.Load() {
		t.Fatal("old provider closed while an old snapshot lease was active")
	}
	foundTimeout := false
	for _, event := range providerManager.Events().History(0) {
		if event.Type == provider.EventDrainTimedOut && event.ProviderID == provider.Copilot {
			foundTimeout = true
		}
	}
	if !foundTimeout {
		t.Fatal("drain timeout event was not published")
	}

	lease.Release()
	select {
	case <-old.closedCh:
	case <-time.After(time.Second):
		t.Fatal("old provider did not close after the old lease released")
	}
	registered := providerManager.ActiveProviders()[0].(*managedTestProvider)
	if registered.model != "new" || registered.closed.Load() {
		t.Fatalf("replacement provider = %#v", registered)
	}
}

func TestNoOpReconcileReusesEffectiveProvider(t *testing.T) {
	_, providerManager := newManagedTestManagers(t, time.Second)
	desired := []provider.DesiredProvider{{
		ID:      provider.Copilot,
		Enabled: true,
		Config:  "stable",
		Accounts: []provider.AccountState{{
			ID: "primary", Label: "Primary", Enabled: true,
		}},
	}}
	if _, err := providerManager.Reconcile(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	active := providerManager.ActiveProviders()[0]
	eventsBefore := len(providerManager.Events().History(0))
	desired[0].Accounts[0].Label = "Renamed"
	report, err := providerManager.Reconcile(context.Background(), desired)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Completed || report.TimedOut {
		t.Fatalf("no-op drain report = %#v", report)
	}
	if got := providerManager.ActiveProviders()[0]; got != active {
		t.Fatal("no-op reconcile rebuilt an unchanged provider")
	}
	if got := len(providerManager.Events().History(0)); got != eventsBefore {
		t.Fatalf("no-op reconcile published lifecycle events: before=%d after=%d", eventsBefore, got)
	}
	if got := providerManager.List()[0].Accounts[0].Label; got != "Renamed" {
		t.Fatalf("desired account label = %q, want Renamed", got)
	}
}

func TestHealthRefreshDoesNotWaitForProviderDrain(t *testing.T) {
	runtimeManager, providerManager := newManagedTestManagers(t, time.Second)
	if _, err := providerManager.Reconcile(context.Background(), []provider.DesiredProvider{{ID: provider.Copilot, Enabled: true, Config: "old"}}); err != nil {
		t.Fatal(err)
	}
	lease, err := runtimeManager.Acquire()
	if err != nil {
		t.Fatal(err)
	}

	replaceDone := make(chan error, 1)
	go func() {
		_, replaceErr := providerManager.Replace(context.Background(), provider.Copilot, "new")
		replaceDone <- replaceErr
	}()
	deadline := time.Now().Add(time.Second)
	for {
		started := false
		for _, event := range providerManager.Events().History(0) {
			if event.Type == provider.EventDrainStarted {
				started = true
				break
			}
		}
		if started {
			break
		}
		if time.Now().After(deadline) {
			lease.Release()
			t.Fatal("replacement did not begin draining")
		}
		time.Sleep(time.Millisecond)
	}

	healthDone := make(chan struct{})
	go func() {
		providerManager.RefreshHealth(context.Background())
		close(healthDone)
	}()
	select {
	case <-healthDone:
	case <-time.After(100 * time.Millisecond):
		lease.Release()
		t.Fatal("health refresh waited for the provider drain")
	}
	lease.Release()
	select {
	case err := <-replaceDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement did not finish after lease release")
	}
}

func TestProviderCloseTimeoutIsEnforced(t *testing.T) {
	closeGate := make(chan struct{})
	runtimeManager, err := runtimepkg.NewManager(&configschema.Config{Routing: configschema.Routing{DefaultProvider: string(provider.Copilot)}})
	if err != nil {
		t.Fatal(err)
	}
	providerManager, err := provider.NewManager(runtimeManager, provider.NewHealthRegistry(), provider.NewEventBus(32), provider.ManagerOptions{
		DrainTimeout: time.Second,
		CloseTimeout: 15 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := providerManager.RegisterFactory(managedBlockingFactory(closeGate)); err != nil {
		t.Fatal(err)
	}
	if _, err := providerManager.Reconcile(context.Background(), []provider.DesiredProvider{{ID: provider.Copilot, Enabled: true, Config: "old"}}); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	report, err := providerManager.Replace(context.Background(), provider.Copilot, "new")
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Completed || report.CloseErrors != 1 {
		t.Fatalf("close timeout report = %#v", report)
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("replacement took %s despite close timeout", elapsed)
	}
	close(closeGate)
}

func TestConcurrentReplaceRemoveListAndRoute(t *testing.T) {
	runtimeManager, providerManager := newManagedTestManagers(t, 2*time.Millisecond)
	if _, err := providerManager.Reconcile(context.Background(), []provider.DesiredProvider{{ID: provider.Copilot, Enabled: true, Config: "model-0"}}); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errorsFound := make(chan error, 32)
	for reader := 0; reader < 6; reader++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				_ = providerManager.List()
				lease, err := runtimeManager.Acquire()
				if err != nil {
					errorsFound <- err
					return
				}
				for _, registered := range lease.Snapshot().Providers().All() {
					models, listErr := registered.ListModels(context.Background())
					if listErr != nil {
						errorsFound <- listErr
						lease.Release()
						return
					}
					if len(models.Data) == 1 {
						_, _ = lease.Snapshot().Router().Resolve(context.Background(), "github/"+models.Data[0].ID, provider.CapabilityChat)
					}
				}
				lease.Release()
			}
		}()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		for iteration := 1; iteration <= 50; iteration++ {
			if iteration%5 == 0 {
				if _, err := providerManager.Remove(context.Background(), provider.Copilot); err != nil {
					errorsFound <- err
					return
				}
			}
			if _, err := providerManager.Replace(context.Background(), provider.Copilot, fmt.Sprintf("model-%d", iteration)); err != nil {
				errorsFound <- err
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

func TestRemoveAndUnregisterLifecycle(t *testing.T) {
	_, providerManager := newManagedTestManagers(t, time.Second)
	if _, err := providerManager.Reconcile(context.Background(), []provider.DesiredProvider{{ID: provider.Copilot, Enabled: true, Config: "old"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := providerManager.Remove(context.Background(), provider.Copilot); err != nil {
		t.Fatal(err)
	}
	statuses := providerManager.List()
	if len(statuses) != 1 || statuses[0].Enabled || statuses[0].Health.State != provider.StateDisabled {
		t.Fatalf("removed status = %#v", statuses)
	}
	if _, err := providerManager.UnregisterFactory(context.Background(), provider.Copilot); err != nil {
		t.Fatal(err)
	}
	if statuses := providerManager.List(); len(statuses) != 0 {
		t.Fatalf("statuses after unregister = %#v", statuses)
	}
}
