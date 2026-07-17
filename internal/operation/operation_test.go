package operation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testOptions(t *testing.T, now *time.Time) Options {
	t.Helper()
	return Options{
		Path:          filepath.Join(t.TempDir(), "operations.json"),
		MaxOperations: 32,
		MaxEvents:     64,
		Retention:     time.Hour,
		DefaultTTL:    time.Minute,
		Now:           func() time.Time { return *now },
	}
}

func createTestOperation(t *testing.T, manager *Manager, now time.Time, key, digest string) Record {
	t.Helper()
	record, _, err := manager.Create(CreateRequest{
		Kind:           "auth_session",
		Target:         "copilot/primary",
		Owner:          "operator:test",
		Cancelable:     true,
		ExpiresAt:      now.Add(time.Minute),
		RecoveryPolicy: RecoveryFail,
		Metadata:       map[string]string{"provider_id": "copilot"},
		IdempotencyKey: key,
		RequestDigest:  digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestIdempotentCreationPersistsAcrossRestart(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	options := testOptions(t, &now)
	firstManager, err := OpenManager(options)
	if err != nil {
		t.Fatal(err)
	}
	first := createTestOperation(t, firstManager, now, "same-key", "digest-a")
	if err := firstManager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	secondManager, err := OpenManager(options)
	if err != nil {
		t.Fatal(err)
	}
	defer secondManager.Close(context.Background())
	second, created, err := secondManager.Create(CreateRequest{
		Kind:           "auth_session",
		Target:         "copilot/primary",
		Owner:          "operator:test",
		Cancelable:     true,
		ExpiresAt:      now.Add(time.Minute),
		RecoveryPolicy: RecoveryFail,
		Metadata:       map[string]string{"provider_id": "copilot"},
		IdempotencyKey: "same-key",
		RequestDigest:  "digest-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created || second.ID != first.ID {
		t.Fatalf("idempotent creation produced another operation: first=%s second=%s created=%t", first.ID, second.ID, created)
	}
	_, _, err = secondManager.Create(CreateRequest{
		Kind:           "auth_session",
		Target:         "copilot/primary",
		Owner:          "operator:test",
		Cancelable:     true,
		ExpiresAt:      now.Add(time.Minute),
		RecoveryPolicy: RecoveryFail,
		IdempotencyKey: "same-key",
		RequestDigest:  "different-digest",
	})
	if _, ok := err.(*IdempotencyConflictError); !ok {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
}

func TestRestartFailsUnsafeOperationWithoutPersistingRawFailure(t *testing.T) {
	now := time.Unix(2000, 0).UTC()
	options := testOptions(t, &now)
	manager, err := OpenManager(options)
	if err != nil {
		t.Fatal(err)
	}
	record := createTestOperation(t, manager, now, "", "")
	if _, err := manager.store.Transition(record.ID, StateRunning, 10, nil, nil, "started"); err != nil {
		t.Fatal(err)
	}
	manager.store.Close()

	restarted, err := OpenManager(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close(context.Background())
	recovered, err := restarted.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != StateFailed || recovered.Failure == nil || recovered.Failure.Code != "operation_interrupted" {
		t.Fatalf("unsafe operation was not failed on restart: %+v", recovered)
	}
	payload, err := os.ReadFile(options.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "credential-secret") || strings.Contains(string(payload), "upstream stack") {
		t.Fatalf("operation persistence leaked raw failure data: %s", payload)
	}
}

func TestRestartExpiresPastDeadline(t *testing.T) {
	now := time.Unix(3000, 0).UTC()
	options := testOptions(t, &now)
	manager, err := OpenManager(options)
	if err != nil {
		t.Fatal(err)
	}
	record := createTestOperation(t, manager, now, "", "")
	if _, err := manager.store.Transition(record.ID, StateWaitingForUser, 25, map[string]string{"verification_uri": "https://example.test"}, nil, "waiting_for_user"); err != nil {
		t.Fatal(err)
	}
	manager.store.Close()
	now = now.Add(2 * time.Minute)
	restarted, err := OpenManager(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close(context.Background())
	recovered, err := restarted.Get(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != StateExpired || recovered.Failure == nil || recovered.Failure.Code != "operation_expired" {
		t.Fatalf("past-deadline operation was not expired: %+v", recovered)
	}
}

func TestWorkerSurvivesInitiatingClientDisconnect(t *testing.T) {
	now := time.Now().UTC()
	options := testOptions(t, &now)
	options.Now = time.Now
	manager, err := OpenManager(options)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	record := createTestOperation(t, manager, time.Now().UTC(), "", "")

	clientContext, disconnect := context.WithCancel(context.Background())
	disconnect()
	_ = clientContext // The operation deliberately does not inherit this context.
	finished := make(chan struct{})
	if err := manager.Run(record.ID, func(ctx context.Context, reporter Reporter) (map[string]string, *Failure) {
		defer close(finished)
		if err := reporter.Progress(50, nil); err != nil {
			failure := NewFailure("progress_failed", "Progress could not be persisted.", true)
			return nil, &failure
		}
		select {
		case <-time.After(20 * time.Millisecond):
			return map[string]string{"status": "complete"}, nil
		case <-ctx.Done():
			failure := NewFailure("worker_cancelled", "Worker context was cancelled.", true)
			return nil, &failure
		}
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
	deadline := time.Now().Add(time.Second)
	for {
		current, err := manager.Get(record.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == StateSucceeded {
			if current.Result["status"] != "complete" {
				t.Fatalf("operation result missing: %+v", current)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation did not succeed after client disconnect: %+v", current)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTypedFailureIsSanitized(t *testing.T) {
	now := time.Now().UTC()
	options := testOptions(t, &now)
	options.Now = time.Now
	manager, err := OpenManager(options)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	record := createTestOperation(t, manager, time.Now().UTC(), "", "")
	if err := manager.Run(record.ID, func(context.Context, Reporter) (map[string]string, *Failure) {
		failure := NewFailure("INVALID CODE", "credential-secret upstream stack", false)
		return nil, &failure
	}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		current, err := manager.Get(record.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == StateFailed {
			if current.Failure == nil || current.Failure.Code != "operation_failed" || current.Failure.Message != "The operation failed." {
				t.Fatalf("failure was not sanitized: %+v", current.Failure)
			}
			payload, _ := json.Marshal(current)
			if strings.Contains(string(payload), "stack") || strings.Contains(string(payload), "credential-secret") {
				t.Fatalf("failure response leaked implementation detail: %s", payload)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation did not fail: %+v", current)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCancelPublishesMonotonicEvent(t *testing.T) {
	now := time.Unix(4000, 0).UTC()
	options := testOptions(t, &now)
	manager, err := OpenManager(options)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(context.Background())
	record := createTestOperation(t, manager, now, "", "")
	cancelled, err := manager.Cancel(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != StateCancelled || cancelled.Sequence <= record.Sequence {
		t.Fatalf("cancel did not advance operation state: before=%+v after=%+v", record, cancelled)
	}
	events := manager.Events(record.Sequence)
	if len(events) != 1 || events[0].State != StateCancelled || events[0].Sequence != cancelled.Sequence {
		t.Fatalf("unexpected cancellation event: %+v", events)
	}
}
