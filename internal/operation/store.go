package operation

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type entry struct {
	record          Record
	idempotencyHash string
	requestDigest   string
}

type storeCheckpoint struct {
	operations   map[string]*entry
	events       []Event
	nextSequence uint64
}

// Store is the synchronous durable state machine. Each mutation is persisted
// before observers receive its lifecycle event.
type Store struct {
	mu           sync.RWMutex
	options      Options
	operations   map[string]*entry
	events       []Event
	nextSequence uint64
	subscribers  map[uint64]chan Event
	nextSubID    uint64
	closed       bool
}

var safeKind = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
var safeMetadataKey = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)

func Open(options Options) (*Store, error) {
	if strings.TrimSpace(options.Path) == "" {
		return nil, fmt.Errorf("operation state path is required")
	}
	if options.MaxOperations <= 0 {
		options.MaxOperations = 1024
	}
	if options.MaxEvents <= 0 {
		options.MaxEvents = 4096
	}
	if options.Retention <= 0 {
		options.Retention = 7 * 24 * time.Hour
	}
	if options.DefaultTTL <= 0 {
		options.DefaultTTL = 15 * time.Minute
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	persisted, err := loadPersisted(options.Path)
	if err != nil {
		return nil, err
	}
	store := &Store{
		options:      options,
		operations:   make(map[string]*entry, len(persisted.Operations)),
		events:       append([]Event(nil), persisted.Events...),
		nextSequence: persisted.NextSequence,
		subscribers:  make(map[uint64]chan Event),
	}
	for _, item := range persisted.Operations {
		record := copyRecord(item.Record)
		store.operations[record.ID] = &entry{record: record, idempotencyHash: item.IdempotencyHash, requestDigest: item.RequestDigest}
		if record.Sequence > store.nextSequence {
			store.nextSequence = record.Sequence
		}
	}
	for _, event := range store.events {
		if event.Sequence > store.nextSequence {
			store.nextSequence = event.Sequence
		}
	}
	changed := store.recoverLocked()
	if store.pruneLocked() {
		changed = true
	}
	if changed {
		if err := store.persistLocked(); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (store *Store) DefaultTTL() time.Duration { return store.options.DefaultTTL }
func (store *Store) Now() time.Time            { return store.options.Now().UTC() }

func (store *Store) Create(request CreateRequest) (Record, bool, error) {
	if err := validateCreate(request, store.options.Now().UTC()); err != nil {
		return Record{}, false, err
	}
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return Record{}, false, fmt.Errorf("operation store is closed")
	}
	checkpoint := store.checkpointLocked()
	pruned := store.pruneLocked()
	idempotencyHash := hashIdempotency(request.Owner, request.IdempotencyKey)
	if idempotencyHash != "" {
		for _, existing := range store.operations {
			if existing.idempotencyHash != idempotencyHash {
				continue
			}
			if existing.requestDigest != request.RequestDigest {
				store.restoreLocked(checkpoint)
				store.mu.Unlock()
				return Record{}, false, &IdempotencyConflictError{}
			}
			if pruned {
				if err := store.persistLocked(); err != nil {
					store.restoreLocked(checkpoint)
					store.mu.Unlock()
					return Record{}, false, err
				}
			}
			record := copyRecord(existing.record)
			store.mu.Unlock()
			return record, false, nil
		}
	}
	if len(store.operations) >= store.options.MaxOperations {
		store.restoreLocked(checkpoint)
		store.mu.Unlock()
		return Record{}, false, &CapacityError{}
	}
	now := store.options.Now().UTC()
	record := Record{
		ID:             newID(),
		Kind:           request.Kind,
		Target:         strings.TrimSpace(request.Target),
		Owner:          strings.TrimSpace(request.Owner),
		State:          StatePending,
		Progress:       0,
		Cancelable:     request.Cancelable,
		RecoveryPolicy: request.RecoveryPolicy,
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      request.ExpiresAt.UTC(),
		Metadata:       sanitizeMetadata(request.Metadata),
	}
	if record.RecoveryPolicy == "" {
		record.RecoveryPolicy = RecoveryFail
	}
	event := store.nextEventLocked(record.ID, "created", record.State, now)
	record.Sequence = event.Sequence
	store.operations[record.ID] = &entry{record: record, idempotencyHash: idempotencyHash, requestDigest: request.RequestDigest}
	store.appendEventLocked(event)
	if err := store.persistLocked(); err != nil {
		store.restoreLocked(checkpoint)
		store.mu.Unlock()
		return Record{}, false, err
	}
	store.mu.Unlock()
	store.publish(event)
	return copyRecord(record), true, nil
}

func (store *Store) Get(id string) (Record, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	item, exists := store.operations[id]
	if !exists {
		return Record{}, &NotFoundError{ID: id}
	}
	return copyRecord(item.record), nil
}

func (store *Store) List() []Record {
	store.mu.RLock()
	defer store.mu.RUnlock()
	result := make([]Record, 0, len(store.operations))
	for _, item := range store.operations {
		result = append(result, copyRecord(item.record))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].CreatedAt.Equal(result[right].CreatedAt) {
			return result[left].ID < result[right].ID
		}
		return result[left].CreatedAt.Before(result[right].CreatedAt)
	})
	return result
}

func (store *Store) Transition(id string, next State, progress int, result map[string]string, failure *Failure, eventType string) (Record, error) {
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return Record{}, fmt.Errorf("operation store is closed")
	}
	checkpoint := store.checkpointLocked()
	item, exists := store.operations[id]
	if !exists {
		store.mu.Unlock()
		return Record{}, &NotFoundError{ID: id}
	}
	if !allowedTransition(item.record.State, next) {
		store.mu.Unlock()
		return Record{}, &InvalidTransitionError{ID: id, From: item.record.State, To: next}
	}
	now := store.options.Now().UTC()
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	item.record.State = next
	item.record.Progress = progress
	item.record.UpdatedAt = now
	item.record.Result = sanitizeMetadata(result)
	item.record.Failure = nil
	if failure != nil {
		clean := NewFailure(failure.Code, failure.Message, failure.Retryable)
		item.record.Failure = &clean
	}
	if eventType == "" {
		eventType = "state_changed"
	}
	event := store.nextEventLocked(id, eventType, next, now)
	item.record.Sequence = event.Sequence
	store.appendEventLocked(event)
	if err := store.persistLocked(); err != nil {
		store.restoreLocked(checkpoint)
		store.mu.Unlock()
		return Record{}, err
	}
	record := copyRecord(item.record)
	store.mu.Unlock()
	store.publish(event)
	return record, nil
}

func (store *Store) UpdateProgress(id string, progress int, metadata map[string]string) (Record, error) {
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return Record{}, fmt.Errorf("operation store is closed")
	}
	checkpoint := store.checkpointLocked()
	item, exists := store.operations[id]
	if !exists {
		store.mu.Unlock()
		return Record{}, &NotFoundError{ID: id}
	}
	if item.record.State.Terminal() {
		store.mu.Unlock()
		return Record{}, &InvalidTransitionError{ID: id, From: item.record.State, To: item.record.State}
	}
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	now := store.options.Now().UTC()
	item.record.Progress = progress
	item.record.UpdatedAt = now
	if metadata != nil {
		item.record.Result = sanitizeMetadata(metadata)
	}
	event := store.nextEventLocked(id, "progress", item.record.State, now)
	item.record.Sequence = event.Sequence
	store.appendEventLocked(event)
	if err := store.persistLocked(); err != nil {
		store.restoreLocked(checkpoint)
		store.mu.Unlock()
		return Record{}, err
	}
	record := copyRecord(item.record)
	store.mu.Unlock()
	store.publish(event)
	return record, nil
}

func (store *Store) Cancel(id string) (Record, error) {
	item, err := store.Get(id)
	if err != nil {
		return Record{}, err
	}
	if !item.Cancelable {
		return Record{}, fmt.Errorf("operation %q is not cancelable", id)
	}
	failure := NewFailure("operation_cancelled", "The operation was cancelled.", false)
	return store.Transition(id, StateCancelled, item.Progress, nil, &failure, "cancelled")
}

func (store *Store) Events(after uint64) []Event {
	store.mu.RLock()
	defer store.mu.RUnlock()
	result := make([]Event, 0)
	for _, event := range store.events {
		if event.Sequence > after {
			result = append(result, event)
		}
	}
	return result
}

func (store *Store) Subscribe(buffer int) (<-chan Event, func()) {
	if buffer < 1 {
		buffer = 1
	}
	store.mu.Lock()
	if store.closed {
		stream := make(chan Event)
		close(stream)
		store.mu.Unlock()
		return stream, func() {}
	}
	store.nextSubID++
	id := store.nextSubID
	stream := make(chan Event, buffer)
	store.subscribers[id] = stream
	store.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			store.mu.Lock()
			if current, exists := store.subscribers[id]; exists {
				delete(store.subscribers, id)
				close(current)
			}
			store.mu.Unlock()
		})
	}
	return stream, cancel
}

func (store *Store) Close() {
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return
	}
	store.closed = true
	for id, subscriber := range store.subscribers {
		delete(store.subscribers, id)
		close(subscriber)
	}
	store.mu.Unlock()
}

func (store *Store) recoverLocked() bool {
	changed := false
	now := store.options.Now().UTC()
	ids := make([]string, 0, len(store.operations))
	for id := range store.operations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		item := store.operations[id]
		if item.record.State.Terminal() {
			continue
		}
		changed = true
		if !item.record.ExpiresAt.After(now) || item.record.RecoveryPolicy == RecoveryExpire {
			failure := NewFailure("operation_expired", "The operation expired before it could be resumed.", true)
			item.record.State = StateExpired
			item.record.Failure = &failure
		} else {
			failure := NewFailure("operation_interrupted", "The daemon restarted before the operation completed.", true)
			item.record.State = StateFailed
			item.record.Failure = &failure
		}
		item.record.UpdatedAt = now
		event := store.nextEventLocked(id, "recovered", item.record.State, now)
		item.record.Sequence = event.Sequence
		store.appendEventLocked(event)
	}
	return changed
}

func (store *Store) pruneLocked() bool {
	changed := false
	cutoff := store.options.Now().UTC().Add(-store.options.Retention)
	for id, item := range store.operations {
		if item.record.State.Terminal() && item.record.UpdatedAt.Before(cutoff) {
			delete(store.operations, id)
			changed = true
		}
	}
	if len(store.operations) <= store.options.MaxOperations {
		return changed
	}
	type candidate struct {
		id      string
		updated time.Time
	}
	var terminal []candidate
	for id, item := range store.operations {
		if item.record.State.Terminal() {
			terminal = append(terminal, candidate{id: id, updated: item.record.UpdatedAt})
		}
	}
	sort.Slice(terminal, func(left, right int) bool { return terminal[left].updated.Before(terminal[right].updated) })
	for _, value := range terminal {
		if len(store.operations) <= store.options.MaxOperations {
			break
		}
		delete(store.operations, value.id)
		changed = true
	}
	return changed
}

func (store *Store) checkpointLocked() storeCheckpoint {
	operations := make(map[string]*entry, len(store.operations))
	for id, item := range store.operations {
		operations[id] = &entry{
			record:          copyRecord(item.record),
			idempotencyHash: item.idempotencyHash,
			requestDigest:   item.requestDigest,
		}
	}
	return storeCheckpoint{
		operations:   operations,
		events:       append([]Event(nil), store.events...),
		nextSequence: store.nextSequence,
	}
}

func (store *Store) restoreLocked(checkpoint storeCheckpoint) {
	store.operations = checkpoint.operations
	store.events = checkpoint.events
	store.nextSequence = checkpoint.nextSequence
}

func (store *Store) persistLocked() error {
	snapshot := persistedStore{SchemaVersion: schemaVersion, NextSequence: store.nextSequence, Events: append([]Event(nil), store.events...)}
	ids := make([]string, 0, len(store.operations))
	for id := range store.operations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		item := store.operations[id]
		snapshot.Operations = append(snapshot.Operations, persistedOperation{Record: copyRecord(item.record), IdempotencyHash: item.idempotencyHash, RequestDigest: item.requestDigest})
	}
	return persistSnapshot(store.options.Path, snapshot)
}

func (store *Store) nextEventLocked(id, eventType string, state State, now time.Time) Event {
	store.nextSequence++
	return Event{Sequence: store.nextSequence, OperationID: id, Type: eventType, State: state, Timestamp: now}
}

func (store *Store) appendEventLocked(event Event) {
	store.events = append(store.events, event)
	if len(store.events) > store.options.MaxEvents {
		overflow := len(store.events) - store.options.MaxEvents
		copy(store.events, store.events[overflow:])
		store.events = store.events[:store.options.MaxEvents]
	}
}

func (store *Store) publish(event Event) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, subscriber := range store.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func allowedTransition(from, to State) bool {
	if from.Terminal() {
		return false
	}
	switch from {
	case StatePending:
		return to == StateRunning || to == StateWaitingForUser || to == StateSucceeded || to == StateFailed || to == StateCancelled || to == StateExpired
	case StateRunning:
		return to == StateWaitingForUser || to == StateSucceeded || to == StateFailed || to == StateCancelled || to == StateExpired
	case StateWaitingForUser:
		return to == StateRunning || to == StateSucceeded || to == StateFailed || to == StateCancelled || to == StateExpired
	default:
		return false
	}
}

func validateCreate(request CreateRequest, now time.Time) error {
	if !safeKind.MatchString(request.Kind) {
		return fmt.Errorf("invalid operation kind")
	}
	if strings.TrimSpace(request.Owner) == "" || len(request.Owner) > 256 {
		return fmt.Errorf("operation owner is required")
	}
	if len(request.Target) > 512 {
		return fmt.Errorf("operation target is too long")
	}
	if !request.ExpiresAt.After(now) {
		return fmt.Errorf("operation expiry must be in the future")
	}
	if request.RecoveryPolicy != "" && request.RecoveryPolicy != RecoveryFail && request.RecoveryPolicy != RecoveryExpire {
		return fmt.Errorf("unsupported recovery policy")
	}
	if _, err := validateMetadata(request.Metadata); err != nil {
		return err
	}
	if request.IdempotencyKey != "" && strings.TrimSpace(request.RequestDigest) == "" {
		return fmt.Errorf("request digest is required with an idempotency key")
	}
	return nil
}

func validateMetadata(source map[string]string) (map[string]string, error) {
	if len(source) > 32 {
		return nil, fmt.Errorf("operation metadata contains too many fields")
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		key = strings.TrimSpace(key)
		if !safeMetadataKey.MatchString(key) {
			return nil, fmt.Errorf("invalid operation metadata key")
		}
		value = strings.TrimSpace(value)
		if len(value) > 1024 {
			return nil, fmt.Errorf("operation metadata value is too long")
		}
		result[key] = value
	}
	return result, nil
}

func sanitizeMetadata(source map[string]string) map[string]string {
	validated, err := validateMetadata(source)
	if err != nil || len(validated) == 0 {
		return nil
	}
	return validated
}

func hashIdempotency(owner, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(owner + "\x00" + key))
	return hex.EncodeToString(digest[:])
}

func newID() string {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
		copy(random[:], digest[:16])
	}
	return "op_" + hex.EncodeToString(random[:])
}
