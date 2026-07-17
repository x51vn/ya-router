package operation

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Reporter lets a worker publish resumable progress without owning persistence.
type Reporter interface {
	Progress(int, map[string]string) error
	WaitingForUser(map[string]string) error
}

// Worker is provider-neutral long-running work. It receives a daemon-owned
// context independent from the initiating HTTP request.
type Worker func(context.Context, Reporter) (map[string]string, *Failure)

type runner struct {
	cancel context.CancelFunc
}

// Manager adds daemon-owned execution and cancellation to the durable store.
type Manager struct {
	store *Store

	mu      sync.Mutex
	runners map[string]runner
	closing bool
	wg      sync.WaitGroup
}

func OpenManager(options Options) (*Manager, error) {
	store, err := Open(options)
	if err != nil {
		return nil, err
	}
	return &Manager{store: store, runners: make(map[string]runner)}, nil
}

func (manager *Manager) DefaultTTL() time.Duration {
	return manager.store.DefaultTTL()
}

func (manager *Manager) Now() time.Time { return manager.store.Now() }

func (manager *Manager) Create(request CreateRequest) (Record, bool, error) {
	return manager.store.Create(request)
}

func (manager *Manager) Get(id string) (Record, error) { return manager.store.Get(id) }
func (manager *Manager) List() []Record                { return manager.store.List() }
func (manager *Manager) Events(after uint64) []Event   { return manager.store.Events(after) }
func (manager *Manager) Subscribe(buffer int) (<-chan Event, func()) {
	return manager.store.Subscribe(buffer)
}

// Run starts a worker using context.Background, not the request context that
// created the operation. Client disconnect therefore cannot cancel daemon work.
func (manager *Manager) Run(id string, worker Worker) error {
	if worker == nil {
		return fmt.Errorf("operation worker is required")
	}
	record, err := manager.store.Get(id)
	if err != nil {
		return err
	}
	if record.State.Terminal() {
		return &InvalidTransitionError{ID: id, From: record.State, To: StateRunning}
	}
	manager.mu.Lock()
	if manager.closing {
		manager.mu.Unlock()
		return fmt.Errorf("operation manager is closing")
	}
	if _, exists := manager.runners[id]; exists {
		manager.mu.Unlock()
		return fmt.Errorf("operation %q is already running", id)
	}
	ctx, cancel := context.WithDeadline(context.Background(), record.ExpiresAt)
	manager.runners[id] = runner{cancel: cancel}
	manager.wg.Add(1)
	manager.mu.Unlock()
	if _, err := manager.store.Transition(id, StateRunning, record.Progress, record.Result, nil, "started"); err != nil {
		manager.mu.Lock()
		delete(manager.runners, id)
		manager.mu.Unlock()
		cancel()
		manager.wg.Done()
		return err
	}
	go manager.execute(ctx, id, worker)
	return nil
}

func (manager *Manager) execute(ctx context.Context, id string, worker Worker) {
	defer manager.wg.Done()
	defer func() {
		manager.mu.Lock()
		running := manager.runners[id]
		delete(manager.runners, id)
		manager.mu.Unlock()
		running.cancel()
	}()

	reporter := managerReporter{manager: manager, id: id}
	var result map[string]string
	var failure *Failure
	func() {
		defer func() {
			if recover() != nil {
				clean := NewFailure("operation_panic", "The operation failed unexpectedly.", true)
				failure = &clean
			}
		}()
		result, failure = worker(ctx, reporter)
	}()

	manager.mu.Lock()
	closing := manager.closing
	manager.mu.Unlock()
	if closing {
		// Preserve the non-terminal record. Restart recovery will fail or expire
		// it deterministically without pretending partial work completed.
		return
	}
	current, err := manager.store.Get(id)
	if err != nil || current.State.Terminal() {
		return
	}
	if ctx.Err() != nil {
		now := manager.store.options.Now().UTC()
		if !current.ExpiresAt.After(now) {
			clean := NewFailure("operation_expired", "The operation expired.", true)
			_, _ = manager.store.Transition(id, StateExpired, current.Progress, nil, &clean, "expired")
			return
		}
		clean := NewFailure("operation_interrupted", "The operation was interrupted.", true)
		_, _ = manager.store.Transition(id, StateFailed, current.Progress, nil, &clean, "failed")
		return
	}
	if failure != nil {
		clean := NewFailure(failure.Code, failure.Message, failure.Retryable)
		_, _ = manager.store.Transition(id, StateFailed, current.Progress, nil, &clean, "failed")
		return
	}
	_, _ = manager.store.Transition(id, StateSucceeded, 100, result, nil, "succeeded")
}

func (manager *Manager) Cancel(id string) (Record, error) {
	record, err := manager.store.Cancel(id)
	if err != nil {
		return Record{}, err
	}
	manager.mu.Lock()
	running, exists := manager.runners[id]
	manager.mu.Unlock()
	if exists {
		running.cancel()
	}
	return record, nil
}

func (manager *Manager) Close(ctx context.Context) error {
	manager.mu.Lock()
	if manager.closing {
		manager.mu.Unlock()
		return nil
	}
	manager.closing = true
	for _, running := range manager.runners {
		running.cancel()
	}
	manager.mu.Unlock()

	done := make(chan struct{})
	go func() {
		manager.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		manager.store.Close()
		return nil
	case <-ctx.Done():
		manager.store.Close()
		return ctx.Err()
	}
}

type managerReporter struct {
	manager *Manager
	id      string
}

func (reporter managerReporter) Progress(progress int, metadata map[string]string) error {
	_, err := reporter.manager.store.UpdateProgress(reporter.id, progress, metadata)
	return err
}

func (reporter managerReporter) WaitingForUser(metadata map[string]string) error {
	record, err := reporter.manager.store.Get(reporter.id)
	if err != nil {
		return err
	}
	_, err = reporter.manager.store.Transition(reporter.id, StateWaitingForUser, record.Progress, metadata, nil, "waiting_for_user")
	return err
}
