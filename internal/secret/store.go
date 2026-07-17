// Package secret defines the daemon-owned SecretStore and the credential-source
// precedence rules used by provider authentication. Stored secret material is
// never returned to control clients: the store exposes only redacted metadata
// and resolves values internally for the data plane.
//
// The store is the single writer of credential material. Clients reference a
// secret by ID (a write-only handle) and can read only its posture
// (configured/source/refreshable), never its value.
package secret

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned when a referenced secret does not exist.
var ErrNotFound = errors.New("secret not found")

// ErrReadOnly is returned when a write targets a read-only source (for example
// an environment-provided credential or the official Codex store).
var ErrReadOnly = errors.New("secret source is read-only")

// Source classifies where a credential value originates. Precedence between
// sources is fixed and defined by SourceRank.
type Source string

const (
	// SourceEnvironment is a process environment variable. It is read-only and
	// has the highest precedence so an operator export cannot be silently
	// shadowed by a lower-precedence managed write.
	SourceEnvironment Source = "environment"
	// SourceManaged is a daemon-owned, writable secret (the SecretStore).
	SourceManaged Source = "managed"
	// SourceOfficialStore is an external provider store (e.g. the official
	// Codex credentials). It is read-only; the daemon never writes to it.
	SourceOfficialStore Source = "official_store"
	// SourceLegacyConfig is an inline value from a pre-SecretStore config file.
	SourceLegacyConfig Source = "legacy_config"
)

// SourceRank orders credential sources from highest to lowest precedence.
// Environment always wins; legacy inline config always loses.
func SourceRank(source Source) int {
	switch source {
	case SourceEnvironment:
		return 4
	case SourceManaged:
		return 3
	case SourceOfficialStore:
		return 2
	case SourceLegacyConfig:
		return 1
	default:
		return 0
	}
}

// Metadata is the redacted posture of one secret. It never contains the value.
type Metadata struct {
	ID         string    `json:"id"`
	Source     Source    `json:"source"`
	Configured bool      `json:"configured"`
	ReadOnly   bool      `json:"read_only"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
	// Version increments on each managed write, enabling rotation auditing.
	Version uint64 `json:"version"`
}

// AuditEvent is emitted for every mutating store operation. It records what
// changed without the secret value.
type AuditEvent struct {
	Action    string    `json:"action"`
	SecretID  string    `json:"secret_id"`
	Source    Source    `json:"source"`
	Version   uint64    `json:"version"`
	Actor     string    `json:"actor,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// AuditSink receives redacted audit events. Implementations must not log secret
// values (the event never carries one).
type AuditSink interface {
	Record(AuditEvent)
}

// AuditSinkFunc adapts a function to AuditSink.
type AuditSinkFunc func(AuditEvent)

func (f AuditSinkFunc) Record(event AuditEvent) { f(event) }

// SecretStore is the daemon-owned credential store. Set/Delete mutate managed
// secrets; Resolve returns a value for internal data-plane use only; Metadata
// returns redacted posture safe for control clients.
type SecretStore interface {
	Set(actor, id, value string) (Metadata, error)
	Delete(actor, id string) error
	Resolve(id string) (string, Source, bool)
	Metadata(id string) (Metadata, bool)
	List() []Metadata
}

type entry struct {
	value     string
	source    Source
	readOnly  bool
	version   uint64
	updatedAt time.Time
}

type persistedEntry struct {
	ID        string    `json:"id"`
	Value     string    `json:"value"`
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MemoryStore is an in-memory SecretStore suitable for the daemon runtime and
// tests. Environment and official-store sources are registered read-only.
type MemoryStore struct {
	mu      sync.RWMutex
	entries map[string]*entry
	audit   AuditSink
	now     func() time.Time
	persist func([]persistedEntry) error
}

// NewMemoryStore builds an empty store. audit may be nil.
func NewMemoryStore(audit AuditSink) *MemoryStore {
	return &MemoryStore{
		entries: make(map[string]*entry),
		audit:   audit,
		now:     time.Now,
	}
}

// RegisterReadOnly records a read-only credential (environment or official
// store) so precedence and metadata reflect it without allowing writes.
func (store *MemoryStore) RegisterReadOnly(id, value string, source Source) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("secret id is required")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.entries[id] = &entry{value: value, source: source, readOnly: true, updatedAt: store.now().UTC()}
	return nil
}

// Set writes or replaces a managed secret. It refuses to overwrite a read-only
// entry so a lower-precedence managed write cannot shadow an environment value.
func (store *MemoryStore) Set(actor, id, value string) (Metadata, error) {
	if strings.TrimSpace(id) == "" {
		return Metadata{}, fmt.Errorf("secret id is required")
	}
	store.mu.Lock()
	existing, ok := store.entries[id]
	if ok && existing.readOnly {
		store.mu.Unlock()
		return Metadata{}, fmt.Errorf("%w: %q is provided by %s", ErrReadOnly, id, existing.source)
	}
	version := uint64(1)
	if ok {
		version = existing.version + 1
	}
	now := store.now().UTC()
	next := cloneEntries(store.entries)
	next[id] = &entry{value: value, source: SourceManaged, version: version, updatedAt: now}
	if err := store.persistManagedLocked(next); err != nil {
		store.mu.Unlock()
		return Metadata{}, err
	}
	store.entries = next
	store.mu.Unlock()

	store.emit(AuditEvent{Action: "set", SecretID: id, Source: SourceManaged, Version: version, Actor: actor, Timestamp: now})
	return Metadata{ID: id, Source: SourceManaged, Configured: value != "", ReadOnly: false, UpdatedAt: now, Version: version}, nil
}

// Delete removes a managed secret. Read-only entries cannot be deleted.
func (store *MemoryStore) Delete(actor, id string) error {
	store.mu.Lock()
	existing, ok := store.entries[id]
	if !ok {
		store.mu.Unlock()
		return ErrNotFound
	}
	if existing.readOnly {
		store.mu.Unlock()
		return fmt.Errorf("%w: %q is provided by %s", ErrReadOnly, id, existing.source)
	}
	next := cloneEntries(store.entries)
	delete(next, id)
	if err := store.persistManagedLocked(next); err != nil {
		store.mu.Unlock()
		return err
	}
	store.entries = next
	version := existing.version
	store.mu.Unlock()

	store.emit(AuditEvent{Action: "delete", SecretID: id, Source: SourceManaged, Version: version, Actor: actor, Timestamp: store.now().UTC()})
	return nil
}

// Resolve returns the stored value for internal data-plane use. It is never
// exposed to control clients.
func (store *MemoryStore) Resolve(id string) (string, Source, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	existing, ok := store.entries[id]
	if !ok {
		return "", "", false
	}
	return existing.value, existing.source, true
}

// Metadata returns redacted posture for one secret.
func (store *MemoryStore) Metadata(id string) (Metadata, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	existing, ok := store.entries[id]
	if !ok {
		return Metadata{}, false
	}
	return Metadata{
		ID:         id,
		Source:     existing.source,
		Configured: existing.value != "",
		ReadOnly:   existing.readOnly,
		UpdatedAt:  existing.updatedAt,
		Version:    existing.version,
	}, true
}

// List returns redacted metadata for every secret, sorted by ID.
func (store *MemoryStore) List() []Metadata {
	store.mu.RLock()
	metadata := make([]Metadata, 0, len(store.entries))
	for id, existing := range store.entries {
		metadata = append(metadata, Metadata{
			ID:         id,
			Source:     existing.source,
			Configured: existing.value != "",
			ReadOnly:   existing.readOnly,
			UpdatedAt:  existing.updatedAt,
			Version:    existing.version,
		})
	}
	store.mu.RUnlock()
	sort.Slice(metadata, func(i, j int) bool { return metadata[i].ID < metadata[j].ID })
	return metadata
}

func (store *MemoryStore) emit(event AuditEvent) {
	if store.audit != nil {
		store.audit.Record(event)
	}
}

func cloneEntries(entries map[string]*entry) map[string]*entry {
	cloned := make(map[string]*entry, len(entries))
	for id, existing := range entries {
		copy := *existing
		cloned[id] = &copy
	}
	return cloned
}

func (store *MemoryStore) persistManagedLocked(entries map[string]*entry) error {
	if store.persist == nil {
		return nil
	}
	persisted := make([]persistedEntry, 0, len(entries))
	for id, existing := range entries {
		if existing.readOnly {
			continue
		}
		persisted = append(persisted, persistedEntry{
			ID:        id,
			Value:     existing.value,
			Version:   existing.version,
			UpdatedAt: existing.updatedAt,
		})
	}
	sort.Slice(persisted, func(i, j int) bool { return persisted[i].ID < persisted[j].ID })
	return store.persist(persisted)
}
