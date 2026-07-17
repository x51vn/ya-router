package yarouter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	controlpkg "github.com/duvu/ya-router/internal/control"
	providerpkg "github.com/duvu/ya-router/internal/provider"
	runtimepkg "github.com/duvu/ya-router/internal/runtime"
	statepkg "github.com/duvu/ya-router/internal/state"
)

// recordingReloader captures reconcile calls to prove hot reload runs on commit
// and never on a rejected/dry-run mutation.
type recordingReloader struct {
	calls   int
	lastCfg []providerpkg.DesiredProvider
	err     error
}

func (r *recordingReloader) Reconcile(_ context.Context, desired []providerpkg.DesiredProvider) (providerpkg.DrainReport, error) {
	r.calls++
	r.lastCfg = desired
	return providerpkg.DrainReport{}, r.err
}

func withManagedState(t *testing.T) uint64 {
	t.Helper()
	oldOverride := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "config.json")
	t.Cleanup(func() { configPathOverride = oldOverride })
	release, err := acquireManagedConfigState("test-daemon")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = release() })
	return currentConfigState().Snapshot().Revision
}

func TestApplyManagedMutationCommitsAndReloads(t *testing.T) {
	revision := withManagedState(t)
	reloader := &recordingReloader{}

	enabled := true
	result, err := applyManagedMutation(controlpkg.MutationRequest{
		Kind:             controlpkg.MutationProviderEnabled,
		Provider:         "codex",
		Enabled:          &enabled,
		ExpectedRevision: revision,
	}, "operator", reloader)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !result.Applied || !result.RuntimePublished || result.NextRevision <= revision {
		t.Fatalf("result = %+v", result)
	}
	if reloader.calls != 1 {
		t.Fatalf("reconcile calls = %d, want 1", reloader.calls)
	}
	// The committed effective config must be visible in the snapshot.
	snapshot := currentConfigState().Snapshot()
	if !snapshot.Desired.Providers.Codex.Enabled {
		t.Fatal("codex enable not persisted")
	}
}

func TestApplyManagedMutationDryRunDoesNotCommitOrReload(t *testing.T) {
	revision := withManagedState(t)
	reloader := &recordingReloader{}

	result, err := applyManagedMutation(controlpkg.MutationRequest{
		Kind:             controlpkg.MutationDefaultModel,
		Model:            "gpt-5.4-mini",
		ExpectedRevision: revision,
		DryRun:           true,
	}, "operator", reloader)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if result.Applied {
		t.Fatal("dry-run must not apply")
	}
	if reloader.calls != 0 {
		t.Fatalf("dry-run reconcile calls = %d, want 0", reloader.calls)
	}
	if got := currentConfigState().Snapshot().Revision; got != revision {
		t.Fatalf("revision changed on dry-run: %d != %d", got, revision)
	}
}

// TestApplyManagedMutationConflict proves a stale expected revision is rejected
// with a typed conflict and makes no change (no silent overwrite).
func TestApplyManagedMutationConflict(t *testing.T) {
	revision := withManagedState(t)
	reloader := &recordingReloader{}

	_, err := applyManagedMutation(controlpkg.MutationRequest{
		Kind:             controlpkg.MutationDefaultModel,
		Model:            "new-model",
		ExpectedRevision: revision + 99, // stale/wrong
	}, "operator", reloader)
	var conflict *statepkg.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if reloader.calls != 0 {
		t.Fatalf("conflict must not reload: calls=%d", reloader.calls)
	}
	status, code := mutationHTTPStatus(err)
	if status != 409 || code != "revision_conflict" {
		t.Fatalf("status=%d code=%s", status, code)
	}
}

// TestApplyManagedMutationRejectedValidationMakesNoChange proves an invalid
// mutation leaves the effective runtime untouched.
func TestApplyManagedMutationRejectedValidationMakesNoChange(t *testing.T) {
	revision := withManagedState(t)
	reloader := &recordingReloader{}

	_, err := applyManagedMutation(controlpkg.MutationRequest{
		Kind:             controlpkg.MutationDefaultProvider,
		Value:            "bogus-provider",
		ExpectedRevision: revision,
	}, "operator", reloader)
	if err == nil {
		t.Fatal("expected validation rejection")
	}
	if reloader.calls != 0 {
		t.Fatalf("rejected mutation must not reload: calls=%d", reloader.calls)
	}
	if got := currentConfigState().Snapshot().Revision; got != revision {
		t.Fatalf("revision changed on rejected mutation: %d != %d", got, revision)
	}
}

func TestApplyManagedMutationReconcileFailureRestoresPriorState(t *testing.T) {
	revision := withManagedState(t)
	reloader := &recordingReloader{err: errors.New("provider build failed")}
	enabled := true

	result, err := applyManagedMutation(controlpkg.MutationRequest{
		Kind:             controlpkg.MutationProviderEnabled,
		Provider:         "codex",
		Enabled:          &enabled,
		ExpectedRevision: revision,
	}, "operator", reloader)
	if err == nil {
		t.Fatal("expected reconcile failure")
	}
	if reloader.calls != 1 {
		t.Fatalf("reconcile calls = %d, want 1", reloader.calls)
	}
	snapshot := currentConfigState().Snapshot()
	if snapshot.Desired.Providers.Codex.Enabled || snapshot.Effective.Providers.Codex.Enabled {
		t.Fatalf("failed mutation persisted provider enablement: %+v", snapshot)
	}
	if snapshot.Revision <= revision {
		t.Fatalf("recovery revision = %d, want > %d", snapshot.Revision, revision)
	}
	if result.Applied || result.RuntimePublished || result.NextRevision != snapshot.Revision {
		t.Fatalf("recovery result = %+v, snapshot revision = %d", result, snapshot.Revision)
	}
}

func TestManagedMutationDoesNotDisturbConcurrentRequestSnapshot(t *testing.T) {
	revision := withManagedState(t)
	config := defaultConfig()
	config.Timeouts.ProxyContext = 2
	provider := &mockProvider{
		id: ProviderCopilot, name: "copilot", caps: []Capability{CapabilityChat},
		health:    ProviderHealth{Authenticated: true},
		models:    &ModelList{Object: "list", Data: []Model{{ID: "gpt-5-mini", Object: "model"}}},
		lastKnown: []string{"gpt-5-mini"},
		proxyFunc: func(_ context.Context, writer http.ResponseWriter, _ *http.Request, _ []byte, _ Capability) error {
			writer.WriteHeader(http.StatusNoContent)
			return nil
		},
	}
	runtimeManager, err := runtimepkg.NewManager(config, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtimeManager.Close(context.Background()) }()
	handler := managedProxyHandler(runtimeManager)

	var requests sync.WaitGroup
	requests.Add(1)
	go func() {
		defer requests.Done()
		for index := 0; index < 32; index++ {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"github/gpt-5-mini","messages":[]}`)))
			if recorder.Code != http.StatusNoContent {
				t.Errorf("request %d status = %d", index, recorder.Code)
			}
		}
	}()

	model := "gpt-5.4-mini"
	result, err := applyManagedMutation(controlpkg.MutationRequest{
		Kind: controlpkg.MutationDefaultModel, Model: model, ExpectedRevision: revision,
	}, "operator", &recordingReloader{})
	if err != nil || !result.RuntimePublished {
		t.Fatalf("mutation result=%+v err=%v", result, err)
	}
	requests.Wait()
}
