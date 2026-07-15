package runtime

import (
	"sync"

	configschema "github.com/duvu/ya-router/internal/config"
	"github.com/duvu/ya-router/internal/provider"
	"github.com/duvu/ya-router/internal/routing"
)

// Snapshot is immutable after construction. Data-plane callers must hold a
// Lease while using any accessor result.
type Snapshot struct {
	generation uint64
	config     *configschema.Config
	providers  *provider.Registry
	router     *routing.Router

	mu        sync.Mutex
	active    int
	retired   bool
	drained   chan struct{}
	drainOnce sync.Once
}

func newSnapshot(generation uint64, config *configschema.Config, providers []provider.Provider) *Snapshot {
	effectiveConfig := configschema.Clone(config)
	registry := provider.NewRegistry(providers...)
	registry.Freeze()
	return &Snapshot{
		generation: generation,
		config:     effectiveConfig,
		providers:  registry,
		router:     routing.NewRouter(registry, effectiveConfig.Routing),
		drained:    make(chan struct{}),
	}
}

func (snapshot *Snapshot) Generation() uint64 { return snapshot.generation }

// Config returns an isolated copy so callers cannot mutate the configuration
// used by this snapshot's router.
func (snapshot *Snapshot) Config() *configschema.Config { return configschema.Clone(snapshot.config) }

// Providers returns the snapshot-owned frozen registry.
func (snapshot *Snapshot) Providers() *provider.Registry { return snapshot.providers }

func (snapshot *Snapshot) Router() *routing.Router { return snapshot.router }

func (snapshot *Snapshot) tryAcquire() bool {
	snapshot.mu.Lock()
	defer snapshot.mu.Unlock()
	if snapshot.retired {
		return false
	}
	snapshot.active++
	return true
}

func (snapshot *Snapshot) release() {
	snapshot.mu.Lock()
	if snapshot.active > 0 {
		snapshot.active--
	}
	shouldDrain := snapshot.retired && snapshot.active == 0
	snapshot.mu.Unlock()
	if shouldDrain {
		snapshot.drainOnce.Do(func() { close(snapshot.drained) })
	}
}

func (snapshot *Snapshot) retire() {
	snapshot.mu.Lock()
	snapshot.retired = true
	shouldDrain := snapshot.active == 0
	snapshot.mu.Unlock()
	if shouldDrain {
		snapshot.drainOnce.Do(func() { close(snapshot.drained) })
	}
}

func (snapshot *Snapshot) isDrained() bool {
	select {
	case <-snapshot.drained:
		return true
	default:
		return false
	}
}

// Lease pins one snapshot for a complete data-plane request.
type Lease struct {
	snapshot *Snapshot
	once     sync.Once
}

func (lease *Lease) Snapshot() *Snapshot {
	if lease == nil {
		return nil
	}
	return lease.snapshot
}

func (lease *Lease) Release() {
	if lease == nil || lease.snapshot == nil {
		return
	}
	lease.once.Do(lease.snapshot.release)
}
