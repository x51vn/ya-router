package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
)

var ErrManagerClosed = errors.New("runtime manager is closed")

// Manager atomically publishes immutable snapshots. Acquiring a retired
// snapshot fails and retries against the current pointer, closing the load /
// publish race without a request-global mutex.
type Manager struct {
	current atomic.Pointer[Snapshot]

	mu         sync.Mutex
	closed     bool
	generation uint64
	live       map[*Snapshot]struct{}
}

func NewManager(config *configschema.Config, providers ...provider.Provider) (*Manager, error) {
	if config == nil {
		return nil, fmt.Errorf("runtime config is required")
	}
	if err := validateProviders(providers); err != nil {
		return nil, err
	}
	manager := &Manager{generation: 1, live: make(map[*Snapshot]struct{})}
	initial := newSnapshot(manager.generation, config, providers)
	manager.live[initial] = struct{}{}
	manager.current.Store(initial)
	manager.track(initial)
	return manager, nil
}

func (manager *Manager) Acquire() (*Lease, error) {
	for {
		snapshot := manager.current.Load()
		if snapshot == nil {
			return nil, ErrManagerClosed
		}
		if snapshot.tryAcquire() {
			return &Lease{snapshot: snapshot}, nil
		}
	}
}

// PublishProviders implements provider.SnapshotPublisher using the current
// effective configuration.
func (manager *Manager) PublishProviders(providers []provider.Provider) (provider.Publication, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return provider.Publication{}, ErrManagerClosed
	}
	current := manager.current.Load()
	if current == nil {
		return provider.Publication{}, ErrManagerClosed
	}
	return manager.publishLocked(current.config, providers)
}

// Publish atomically replaces both effective config and providers. YA-TUI-03
// will place revision checks and persistence in front of this primitive.
func (manager *Manager) Publish(config *configschema.Config, providers []provider.Provider) (provider.Publication, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return provider.Publication{}, ErrManagerClosed
	}
	return manager.publishLocked(config, providers)
}

func (manager *Manager) publishLocked(config *configschema.Config, providers []provider.Provider) (provider.Publication, error) {
	if config == nil {
		return provider.Publication{}, fmt.Errorf("runtime config is required")
	}
	if err := validateProviders(providers); err != nil {
		return provider.Publication{}, err
	}
	manager.generation++
	next := newSnapshot(manager.generation, config, providers)
	priorSnapshots := make([]*Snapshot, 0, len(manager.live))
	for snapshot := range manager.live {
		priorSnapshots = append(priorSnapshots, snapshot)
	}
	manager.live[next] = struct{}{}
	old := manager.current.Swap(next)
	manager.track(next)
	if old != nil {
		old.retire()
	}
	retirement := newRetirement(priorSnapshots)
	return provider.Publication{Generation: next.generation, Retirement: retirement}, nil
}

// Close prevents new leases, retires every live snapshot, and waits for
// in-flight requests according to ctx.
func (manager *Manager) Close(ctx context.Context) error {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil
	}
	manager.closed = true
	manager.current.Store(nil)
	snapshots := make([]*Snapshot, 0, len(manager.live))
	for snapshot := range manager.live {
		snapshot.retire()
		snapshots = append(snapshots, snapshot)
	}
	manager.mu.Unlock()
	return newRetirement(snapshots).Wait(ctx)
}

func (manager *Manager) track(snapshot *Snapshot) {
	go func() {
		<-snapshot.drained
		manager.mu.Lock()
		delete(manager.live, snapshot)
		manager.mu.Unlock()
	}()
}

func validateProviders(providers []provider.Provider) error {
	seen := make(map[provider.ID]struct{}, len(providers))
	for _, registered := range providers {
		if registered == nil {
			return fmt.Errorf("runtime provider cannot be nil")
		}
		if registered.ID() == "" {
			return fmt.Errorf("runtime provider ID is required")
		}
		if _, exists := seen[registered.ID()]; exists {
			return fmt.Errorf("runtime provider %q is duplicated", registered.ID())
		}
		seen[registered.ID()] = struct{}{}
	}
	return nil
}

type retirement struct {
	done      chan struct{}
	snapshots []*Snapshot
}

func newRetirement(snapshots []*Snapshot) *retirement {
	retirement := &retirement{done: make(chan struct{})}
	pendingSnapshots := make([]*Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot != nil && !snapshot.isDrained() {
			pendingSnapshots = append(pendingSnapshots, snapshot)
		}
	}
	retirement.snapshots = pendingSnapshots
	if len(pendingSnapshots) == 0 {
		close(retirement.done)
		return retirement
	}
	// One waiter per retirement avoids creating another waiter goroutine for
	// every still-live snapshot on every publication. Pending() observes each
	// drained channel directly, so its count remains accurate while this waiter
	// advances sequentially.
	go func() {
		for _, snapshot := range pendingSnapshots {
			<-snapshot.drained
		}
		close(retirement.done)
	}()
	return retirement
}

func (retirement *retirement) Wait(ctx context.Context) error {
	if retirement == nil {
		return nil
	}
	select {
	case <-retirement.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (retirement *retirement) Pending() int {
	if retirement == nil {
		return 0
	}
	pending := 0
	for _, snapshot := range retirement.snapshots {
		if !snapshot.isDrained() {
			pending++
		}
	}
	return pending
}
