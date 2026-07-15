package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// AccountState is the provider-neutral portion of desired account state.
// IDs are daemon-owned opaque identifiers, not raw upstream account IDs.
type AccountState struct {
	ID       string `json:"id"`
	Label    string `json:"label,omitempty"`
	Enabled  bool   `json:"enabled"`
	Priority int    `json:"priority"`
}

// DesiredProvider is reconciliation input. Config remains opaque to the
// manager and is never returned by List.
type DesiredProvider struct {
	ID                ID
	Enabled           bool
	Config            any
	ConfigFingerprint string
	Accounts          []AccountState
}

// ProviderStatus is a redacted snapshot of one compiled-in provider.
type ProviderStatus struct {
	Descriptor            Descriptor     `json:"descriptor"`
	Enabled               bool           `json:"enabled"`
	EffectiveCapabilities []Capability   `json:"effective_capabilities"`
	Accounts              []AccountState `json:"accounts"`
	Health                HealthRecord   `json:"health"`
	Generation            uint64         `json:"generation"`
}

// DrainReport makes bounded drain behavior observable to management callers.
type DrainReport struct {
	StartedAt        time.Time `json:"started_at"`
	Deadline         time.Time `json:"deadline"`
	CompletedAt      time.Time `json:"completed_at,omitempty"`
	Completed        bool      `json:"completed"`
	TimedOut         bool      `json:"timed_out"`
	PendingSnapshots int       `json:"pending_snapshots"`
	CloseErrors      int       `json:"close_errors"`
}

type ManagerOptions struct {
	DrainTimeout time.Duration
	CloseTimeout time.Duration
}

type desiredRecord struct {
	enabled     bool
	accounts    []AccountState
	fingerprint string
}

type effectiveRecord struct {
	provider   Provider
	generation uint64
}

type removalPlan struct {
	report      DrainReport
	publication Publication
	retired     []Provider
	needsDrain  bool
}

// Manager serializes provider lifecycle mutations while allowing concurrent
// list and health reads. Runtime snapshots, not this map, serve data requests.
type Manager struct {
	mutations sync.Mutex
	mu        sync.RWMutex

	publisher    SnapshotPublisher
	health       *HealthRegistry
	events       *EventBus
	options      ManagerOptions
	factories    map[ID]Factory
	factoryOrder []ID
	desired      map[ID]desiredRecord
	effective    map[ID]effectiveRecord
}

func NewManager(publisher SnapshotPublisher, health *HealthRegistry, events *EventBus, options ManagerOptions) (*Manager, error) {
	if publisher == nil {
		return nil, fmt.Errorf("snapshot publisher is required")
	}
	if health == nil {
		health = NewHealthRegistry()
	}
	if events == nil {
		events = NewEventBus(256)
	}
	if options.DrainTimeout <= 0 {
		options.DrainTimeout = 30 * time.Second
	}
	if options.CloseTimeout <= 0 {
		options.CloseTimeout = 5 * time.Second
	}
	return &Manager{
		publisher: publisher,
		health:    health,
		events:    events,
		options:   options,
		factories: make(map[ID]Factory),
		desired:   make(map[ID]desiredRecord),
		effective: make(map[ID]effectiveRecord),
	}, nil
}

func (manager *Manager) Events() *EventBus { return manager.events }

func (manager *Manager) HealthRegistry() *HealthRegistry { return manager.health }

// RegisterFactory publishes descriptor metadata even while the provider is
// disabled.
func (manager *Manager) RegisterFactory(factory Factory) error {
	if factory == nil {
		return fmt.Errorf("provider factory is required")
	}
	descriptor := factory.Descriptor()
	if err := ValidateDescriptor(descriptor); err != nil {
		return err
	}

	manager.mutations.Lock()
	defer manager.mutations.Unlock()
	manager.mu.Lock()
	if _, exists := manager.factories[descriptor.ID]; exists {
		manager.mu.Unlock()
		return fmt.Errorf("provider factory %q is already registered", descriptor.ID)
	}
	manager.factories[descriptor.ID] = factory
	manager.factoryOrder = append(manager.factoryOrder, descriptor.ID)
	manager.mu.Unlock()
	manager.health.Set(HealthRecord{ProviderID: descriptor.ID, State: StateDisabled})
	manager.events.Publish(LifecycleEvent{Type: EventFactoryRegistered, ProviderID: descriptor.ID})
	return nil
}

// UnregisterFactory removes an effective instance first, then removes its
// descriptor. Compiled-in callers normally keep factories registered.
func (manager *Manager) UnregisterFactory(ctx context.Context, id ID) (DrainReport, error) {
	manager.mutations.Lock()
	locked := true
	defer func() {
		if locked {
			manager.mutations.Unlock()
		}
	}()
	if err := ctx.Err(); err != nil {
		return DrainReport{}, err
	}
	manager.mu.RLock()
	_, exists := manager.factories[id]
	manager.mu.RUnlock()
	if !exists {
		return DrainReport{}, fmt.Errorf("provider factory %q is not registered", id)
	}
	plan, err := manager.prepareRemoveLocked(id)
	if err != nil {
		return plan.report, err
	}
	manager.mu.Lock()
	delete(manager.factories, id)
	delete(manager.desired, id)
	for index, orderedID := range manager.factoryOrder {
		if orderedID == id {
			manager.factoryOrder = append(manager.factoryOrder[:index], manager.factoryOrder[index+1:]...)
			break
		}
	}
	manager.mu.Unlock()
	manager.health.Remove(id)
	manager.events.Publish(LifecycleEvent{Type: EventFactoryUnregistered, ProviderID: id})
	manager.mutations.Unlock()
	locked = false
	if !plan.needsDrain {
		return plan.report, nil
	}
	return manager.drainRetired(plan.publication, plan.retired)
}

// Reconcile atomically replaces the entire desired provider/account set. All
// enabled replacements are built and validated before publication, so one
// failure leaves the prior effective runtime untouched.
func (manager *Manager) Reconcile(ctx context.Context, desired []DesiredProvider) (DrainReport, error) {
	manager.mutations.Lock()
	locked := true
	defer func() {
		if locked {
			manager.mutations.Unlock()
		}
	}()
	if err := ctx.Err(); err != nil {
		return DrainReport{}, err
	}

	requested := make(map[ID]DesiredProvider, len(desired))
	for _, spec := range desired {
		if spec.ID == "" {
			return DrainReport{}, fmt.Errorf("desired provider ID is required")
		}
		if _, exists := requested[spec.ID]; exists {
			return DrainReport{}, fmt.Errorf("desired provider %q is duplicated", spec.ID)
		}
		if err := validateAccounts(spec.ID, spec.Accounts); err != nil {
			return DrainReport{}, err
		}
		requested[spec.ID] = spec
	}

	manager.mu.RLock()
	factoryOrder := append([]ID(nil), manager.factoryOrder...)
	factories := make(map[ID]Factory, len(manager.factories))
	for id, factory := range manager.factories {
		factories[id] = factory
	}
	oldEffective := cloneEffective(manager.effective)
	oldDesired := cloneDesired(manager.desired)
	manager.mu.RUnlock()

	for id := range requested {
		if _, exists := factories[id]; !exists {
			return DrainReport{}, fmt.Errorf("provider factory %q is not registered", id)
		}
	}

	nextEffective := make(map[ID]effectiveRecord)
	newlyBuilt := make([]Provider, 0, len(desired))
	reused := make(map[ID]struct{}, len(oldEffective))
	changed := false
	for _, id := range factoryOrder {
		spec, exists := requested[id]
		if !exists || !spec.Enabled {
			if _, active := oldEffective[id]; active {
				changed = true
			}
			continue
		}
		fingerprint, fingerprintOK := desiredFingerprint(spec)
		priorDesired, wasDesired := oldDesired[id]
		priorEffective, wasEffective := oldEffective[id]
		if fingerprintOK && wasDesired && wasEffective && priorDesired.enabled && priorDesired.fingerprint == fingerprint {
			nextEffective[id] = priorEffective
			reused[id] = struct{}{}
			continue
		}
		registered, err := manager.build(ctx, factories[id], spec.Config)
		if err != nil {
			manager.closeProviders(newlyBuilt)
			manager.events.Publish(LifecycleEvent{Type: EventReconcileFailed, ProviderID: id, Reason: "build_or_validation_failed"})
			return DrainReport{}, err
		}
		changed = true
		newlyBuilt = append(newlyBuilt, registered)
		nextEffective[id] = effectiveRecord{provider: registered}
	}

	nextDesired := make(map[ID]desiredRecord, len(requested))
	for id, spec := range requested {
		fingerprint, _ := desiredFingerprint(spec)
		nextDesired[id] = desiredRecord{enabled: spec.Enabled, accounts: cloneAccounts(spec.Accounts), fingerprint: fingerprint}
	}
	if !changed {
		manager.mu.Lock()
		manager.desired = nextDesired
		manager.mu.Unlock()
		manager.mutations.Unlock()
		locked = false
		return completedDrainReport(), nil
	}

	providers := providersFromEffective(nextEffective, factoryOrder)
	publication, err := manager.publisher.PublishProviders(providers)
	if err != nil {
		manager.closeProviders(newlyBuilt)
		manager.events.Publish(LifecycleEvent{Type: EventReconcileFailed, Reason: "publication_failed"})
		return DrainReport{}, fmt.Errorf("publish provider reconciliation: %w", err)
	}
	for id, record := range nextEffective {
		record.generation = publication.Generation
		nextEffective[id] = record
	}
	manager.mu.Lock()
	manager.effective = nextEffective
	manager.desired = nextDesired
	manager.mu.Unlock()
	manager.updateHealth(publication.Generation)
	for _, id := range factoryOrder {
		if _, active := nextEffective[id]; active {
			manager.events.Publish(LifecycleEvent{Type: EventPublished, ProviderID: id, Generation: publication.Generation})
		}
	}
	retiredEffective := make(map[ID]effectiveRecord)
	for id, record := range oldEffective {
		if _, kept := reused[id]; !kept {
			retiredEffective[id] = record
		}
	}
	retired := providersFromEffective(retiredEffective, factoryOrder)
	manager.mutations.Unlock()
	locked = false
	return manager.drainRetired(publication, retired)
}

// Replace constructs and publishes one provider while retaining all other
// effective instances.
func (manager *Manager) Replace(ctx context.Context, id ID, config any) (DrainReport, error) {
	manager.mutations.Lock()
	locked := true
	defer func() {
		if locked {
			manager.mutations.Unlock()
		}
	}()
	if err := ctx.Err(); err != nil {
		return DrainReport{}, err
	}

	manager.mu.RLock()
	factory, exists := manager.factories[id]
	oldEffective := cloneEffective(manager.effective)
	order := append([]ID(nil), manager.factoryOrder...)
	desired := manager.desired[id]
	manager.mu.RUnlock()
	if !exists {
		return DrainReport{}, fmt.Errorf("provider factory %q is not registered", id)
	}

	replacement, err := manager.build(ctx, factory, config)
	if err != nil {
		manager.events.Publish(LifecycleEvent{Type: EventReconcileFailed, ProviderID: id, Reason: "build_or_validation_failed"})
		return DrainReport{}, err
	}
	nextEffective := cloneEffective(oldEffective)
	nextEffective[id] = effectiveRecord{provider: replacement}
	providers := providersFromEffective(nextEffective, order)
	publication, err := manager.publisher.PublishProviders(providers)
	if err != nil {
		manager.closeProviders([]Provider{replacement})
		return DrainReport{}, fmt.Errorf("publish provider %q replacement: %w", id, err)
	}
	for providerID, record := range nextEffective {
		record.generation = publication.Generation
		nextEffective[providerID] = record
	}
	manager.mu.Lock()
	manager.effective = nextEffective
	desired.enabled = true
	desired.fingerprint, _ = configFingerprint(config)
	manager.desired[id] = desired
	manager.mu.Unlock()
	manager.updateHealth(publication.Generation)
	manager.events.Publish(LifecycleEvent{Type: EventPublished, ProviderID: id, Generation: publication.Generation})
	var retired []Provider
	if old, found := oldEffective[id]; found {
		retired = append(retired, old.provider)
	}
	manager.mutations.Unlock()
	locked = false
	return manager.drainRetired(publication, retired)
}

// Remove atomically publishes a snapshot without id. Its factory remains
// registered, so List reports the provider as disabled.
func (manager *Manager) Remove(ctx context.Context, id ID) (DrainReport, error) {
	manager.mutations.Lock()
	locked := true
	defer func() {
		if locked {
			manager.mutations.Unlock()
		}
	}()
	if err := ctx.Err(); err != nil {
		return DrainReport{}, err
	}
	plan, err := manager.prepareRemoveLocked(id)
	if err != nil {
		return plan.report, err
	}
	manager.mutations.Unlock()
	locked = false
	if !plan.needsDrain {
		return plan.report, nil
	}
	return manager.drainRetired(plan.publication, plan.retired)
}

func (manager *Manager) prepareRemoveLocked(id ID) (removalPlan, error) {
	manager.mu.RLock()
	oldEffective := cloneEffective(manager.effective)
	order := append([]ID(nil), manager.factoryOrder...)
	desired := manager.desired[id]
	_, factoryExists := manager.factories[id]
	manager.mu.RUnlock()
	if !factoryExists {
		return removalPlan{}, fmt.Errorf("provider factory %q is not registered", id)
	}
	old, active := oldEffective[id]
	if !active {
		desired.enabled = false
		manager.mu.Lock()
		manager.desired[id] = desired
		manager.mu.Unlock()
		manager.health.Set(HealthRecord{ProviderID: id, State: StateDisabled})
		return removalPlan{report: completedDrainReport()}, nil
	}

	delete(oldEffective, id)
	publication, err := manager.publisher.PublishProviders(providersFromEffective(oldEffective, order))
	if err != nil {
		return removalPlan{}, fmt.Errorf("publish provider %q removal: %w", id, err)
	}
	for providerID, record := range oldEffective {
		record.generation = publication.Generation
		oldEffective[providerID] = record
	}
	manager.mu.Lock()
	manager.effective = oldEffective
	desired.enabled = false
	manager.desired[id] = desired
	manager.mu.Unlock()
	manager.health.Set(HealthRecord{ProviderID: id, State: StateDisabled, Generation: publication.Generation})
	manager.events.Publish(LifecycleEvent{Type: EventRemoved, ProviderID: id, Generation: publication.Generation})
	return removalPlan{publication: publication, retired: []Provider{old.provider}, needsDrain: true}, nil
}

// ActiveProviders returns a deterministic copy of effective instances for
// startup authentication and internal health refresh only.
func (manager *Manager) ActiveProviders() []Provider {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return providersFromEffective(manager.effective, manager.factoryOrder)
}

// List includes registered-but-disabled providers and never returns config.
func (manager *Manager) List() []ProviderStatus {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	statuses := make([]ProviderStatus, 0, len(manager.factoryOrder))
	for _, id := range manager.factoryOrder {
		factory := manager.factories[id]
		desired := manager.desired[id]
		effective, enabled := manager.effective[id]
		health, found := manager.health.Get(id)
		if !found {
			health = HealthRecord{ProviderID: id, State: StateDisabled}
		}
		statuses = append(statuses, ProviderStatus{
			Descriptor:            factory.Descriptor(),
			Enabled:               enabled && desired.enabled,
			EffectiveCapabilities: effectiveCapabilities(effective.provider),
			Accounts:              cloneAccounts(desired.accounts),
			Health:                health,
			Generation:            effective.generation,
		})
	}
	return statuses
}

// RefreshHealth samples live instances without holding the manager lock while
// provider code runs.
func (manager *Manager) RefreshHealth(ctx context.Context) {
	manager.mutations.Lock()
	defer manager.mutations.Unlock()
	manager.mu.RLock()
	effective := cloneEffective(manager.effective)
	manager.mu.RUnlock()
	for id, record := range effective {
		health := record.provider.Health(ctx)
		manager.health.Set(HealthRecord{
			ProviderID: id,
			State:      stateFromHealth(health),
			Health:     health,
			Generation: record.generation,
		})
	}
}

func (manager *Manager) build(ctx context.Context, factory Factory, config any) (Provider, error) {
	descriptor := factory.Descriptor()
	manager.events.Publish(LifecycleEvent{Type: EventConstructing, ProviderID: descriptor.ID})
	if err := factory.ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("validate provider %q config: %w", descriptor.ID, err)
	}
	registered, err := factory.Build(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("construct provider %q: %w", descriptor.ID, err)
	}
	if registered == nil {
		return nil, fmt.Errorf("construct provider %q: factory returned nil", descriptor.ID)
	}
	if registered.ID() != descriptor.ID {
		manager.closeProviders([]Provider{registered})
		return nil, fmt.Errorf("construct provider %q: instance reported ID %q", descriptor.ID, registered.ID())
	}
	manager.events.Publish(LifecycleEvent{Type: EventValidating, ProviderID: descriptor.ID})
	if err := validateCapabilities(descriptor, registered); err != nil {
		manager.closeProviders([]Provider{registered})
		return nil, err
	}
	if err := factory.ValidateProvider(ctx, registered); err != nil {
		manager.closeProviders([]Provider{registered})
		return nil, fmt.Errorf("validate provider %q instance: %w", descriptor.ID, err)
	}
	return registered, nil
}

func (manager *Manager) updateHealth(generation uint64) {
	manager.mu.RLock()
	factories := append([]ID(nil), manager.factoryOrder...)
	effective := cloneEffective(manager.effective)
	manager.mu.RUnlock()
	for _, id := range factories {
		record, active := effective[id]
		if !active {
			manager.health.Set(HealthRecord{ProviderID: id, State: StateDisabled, Generation: generation})
			continue
		}
		health := record.provider.Health(context.Background())
		manager.health.Set(HealthRecord{ProviderID: id, State: stateFromHealth(health), Health: health, Generation: generation})
	}
}

func (manager *Manager) drainRetired(publication Publication, retired []Provider) (DrainReport, error) {
	if len(retired) == 0 || publication.Retirement == nil {
		return completedDrainReport(), nil
	}
	started := time.Now().UTC()
	report := DrainReport{
		StartedAt:        started,
		Deadline:         started.Add(manager.options.DrainTimeout),
		PendingSnapshots: publication.Retirement.Pending(),
	}
	for _, registered := range retired {
		manager.events.Publish(LifecycleEvent{
			Type:             EventDrainStarted,
			ProviderID:       registered.ID(),
			Generation:       publication.Generation,
			PendingSnapshots: publication.Retirement.Pending(),
		})
	}
	drainContext, cancel := context.WithTimeout(context.Background(), manager.options.DrainTimeout)
	err := publication.Retirement.Wait(drainContext)
	cancel()
	if err == nil {
		report.Completed = true
		report.PendingSnapshots = 0
		report.CloseErrors = manager.closeProviders(retired)
		report.CompletedAt = time.Now().UTC()
		manager.publishDrained(publication.Generation, retired, report.CloseErrors)
		return report, nil
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		return report, fmt.Errorf("wait for provider drain: %w", err)
	}
	report.TimedOut = true
	report.PendingSnapshots = publication.Retirement.Pending()
	for _, registered := range retired {
		manager.events.Publish(LifecycleEvent{
			Type:             EventDrainTimedOut,
			ProviderID:       registered.ID(),
			Generation:       publication.Generation,
			Reason:           "bounded_wait_elapsed",
			PendingSnapshots: report.PendingSnapshots,
		})
	}
	go func() {
		_ = publication.Retirement.Wait(context.Background())
		closeErrors := manager.closeProviders(retired)
		manager.publishDrained(publication.Generation, retired, closeErrors)
	}()
	return report, nil
}

func (manager *Manager) publishDrained(generation uint64, retired []Provider, closeErrors int) {
	reason := ""
	if closeErrors > 0 {
		reason = "close_failed"
	}
	for _, registered := range retired {
		manager.events.Publish(LifecycleEvent{Type: EventDrained, ProviderID: registered.ID(), Generation: generation, Reason: reason})
	}
}

func (manager *Manager) closeProviders(providers []Provider) int {
	closeErrors := 0
	for _, registered := range providers {
		if registered == nil {
			continue
		}
		if err := manager.closeProvider(registered); err != nil {
			closeErrors++
		}
	}
	return closeErrors
}

func (manager *Manager) closeProvider(registered Provider) error {
	ctx, cancel := context.WithTimeout(context.Background(), manager.options.CloseTimeout)
	defer cancel()
	done := make(chan error, 1)
	switch lifecycle := registered.(type) {
	case Lifecycle:
		go func() { done <- lifecycle.Close(ctx) }()
	case interface{ Close() error }:
		go func() { done <- lifecycle.Close() }()
	default:
		return nil
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func validateAccounts(providerID ID, accounts []AccountState) error {
	seen := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		if account.ID == "" {
			return fmt.Errorf("provider %q account ID is required", providerID)
		}
		if account.Priority < 0 {
			return fmt.Errorf("provider %q account %q priority cannot be negative", providerID, account.ID)
		}
		if _, exists := seen[account.ID]; exists {
			return fmt.Errorf("provider %q account %q is duplicated", providerID, account.ID)
		}
		seen[account.ID] = struct{}{}
	}
	return nil
}

func validateCapabilities(descriptor Descriptor, registered Provider) error {
	supported := make(map[Capability]struct{}, len(descriptor.Capabilities))
	for _, capability := range descriptor.Capabilities {
		supported[capability] = struct{}{}
	}
	for _, capability := range registered.Capabilities() {
		if _, exists := supported[capability]; !exists {
			return fmt.Errorf("provider %q instance exposes undescribed capability %q", descriptor.ID, capability)
		}
	}
	return nil
}

func stateFromHealth(health Health) State {
	switch {
	case health.Authenticated:
		return StateReady
	case health.LastError != "":
		return StateError
	case health.CanRefresh:
		return StateDegraded
	default:
		return StateEnabledUnconfigured
	}
}

func cloneAccounts(accounts []AccountState) []AccountState {
	return append([]AccountState(nil), accounts...)
}

func effectiveCapabilities(registered Provider) []Capability {
	if registered == nil {
		return nil
	}
	return append([]Capability(nil), registered.Capabilities()...)
}

func cloneEffective(source map[ID]effectiveRecord) map[ID]effectiveRecord {
	cloned := make(map[ID]effectiveRecord, len(source))
	for id, record := range source {
		cloned[id] = record
	}
	return cloned
}

func cloneDesired(source map[ID]desiredRecord) map[ID]desiredRecord {
	cloned := make(map[ID]desiredRecord, len(source))
	for id, record := range source {
		record.accounts = cloneAccounts(record.accounts)
		cloned[id] = record
	}
	return cloned
}

func desiredFingerprint(spec DesiredProvider) (string, bool) {
	if spec.ConfigFingerprint != "" {
		return spec.ConfigFingerprint, true
	}
	return configFingerprint(spec.Config)
}

func configFingerprint(config any) (string, bool) {
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), true
}

func providersFromEffective(effective map[ID]effectiveRecord, order []ID) []Provider {
	providers := make([]Provider, 0, len(effective))
	seen := make(map[ID]struct{}, len(effective))
	for _, id := range order {
		if record, exists := effective[id]; exists {
			providers = append(providers, record.provider)
			seen[id] = struct{}{}
		}
	}
	if len(providers) != len(effective) {
		remaining := make([]string, 0, len(effective)-len(providers))
		for id := range effective {
			if _, exists := seen[id]; !exists {
				remaining = append(remaining, string(id))
			}
		}
		sort.Strings(remaining)
		for _, id := range remaining {
			providers = append(providers, effective[ID(id)].provider)
		}
	}
	return providers
}

func completedDrainReport() DrainReport {
	now := time.Now().UTC()
	return DrainReport{StartedAt: now, Deadline: now, CompletedAt: now, Completed: true}
}
