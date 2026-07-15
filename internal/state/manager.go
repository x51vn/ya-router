package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	configschema "github.com/duvu/ya-router/internal/config"
)

func Open(options Options) (*Manager, error) {
	if strings.TrimSpace(options.Path) == "" {
		return nil, fmt.Errorf("state path is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if strings.TrimSpace(options.Actor) == "" {
		options.Actor = "ya-routerd"
	}
	if err := os.MkdirAll(filepath.Dir(options.Path), 0o700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	lock, err := acquireProcessLock(options.Path + ".lock")
	if err != nil {
		return nil, err
	}
	manager := &Manager{options: options, lock: lock}
	if err := manager.load(); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return manager, nil
}

func (manager *Manager) load() error {
	config, err := manager.loadConfig()
	if err != nil {
		return err
	}
	config = configschema.Clone(config)
	if manager.options.Validator != nil {
		if err := manager.options.Validator(config); err != nil {
			return fmt.Errorf("validate persisted configuration: %w", err)
		}
	}
	digest, err := configDigest(config)
	if err != nil {
		return err
	}
	meta, metaErr := readMetadata(manager.metadataPath())
	now := manager.options.Now().UTC()
	if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
		return fmt.Errorf("read state metadata: %w", metaErr)
	}
	if meta == nil {
		meta = &metadata{SchemaVersion: metadataSchemaVersion, Revision: 1, Digest: digest, EffectiveDigest: digest, UpdatedAt: now, Actor: manager.options.Actor}
		if err := writeAtomicJSON(manager.metadataPath(), meta, 0o600, nil); err != nil {
			return fmt.Errorf("initialize state metadata: %w", err)
		}
	} else if meta.Digest != digest {
		// A crash may occur between the config and metadata renames. The config
		// file is authoritative once it parses and validates; repair metadata
		// monotonically without discarding the valid config.
		meta.Revision++
		meta.Digest = digest
		meta.EffectiveDigest = digest
		meta.RestartRequired = nil
		meta.UpdatedAt = now
		meta.Actor = "recovery"
		if err := writeAtomicJSON(manager.metadataPath(), meta, 0o600, nil); err != nil {
			return fmt.Errorf("repair state metadata: %w", err)
		}
	}
	if meta.Revision == 0 {
		meta.Revision = 1
	}
	if meta.SchemaVersion == 0 {
		meta.SchemaVersion = metadataSchemaVersion
	}
	manager.snapshot = Snapshot{SchemaVersion: meta.SchemaVersion, Revision: meta.Revision, Digest: digest, EffectiveDigest: digest, UpdatedAt: meta.UpdatedAt, Actor: meta.Actor, Desired: configschema.Clone(config), Effective: configschema.Clone(config), RestartRequired: append([]string(nil), meta.RestartRequired...)}
	return nil
}

func (manager *Manager) loadConfig() (*configschema.Config, error) {
	if _, err := os.Stat(manager.options.Path); err != nil {
		if os.IsNotExist(err) {
			if manager.options.Initial == nil {
				return nil, fmt.Errorf("configuration %q does not exist and no initial config was supplied", manager.options.Path)
			}
			return configschema.Clone(manager.options.Initial), nil
		}
		return nil, fmt.Errorf("stat configuration: %w", err)
	}
	if manager.options.Loader != nil {
		return manager.options.Loader(manager.options.Path)
	}
	data, err := os.ReadFile(manager.options.Path)
	if err != nil {
		return nil, fmt.Errorf("read configuration: %w", err)
	}
	var config configschema.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("decode configuration: %w", err)
	}
	return &config, nil
}

func (manager *Manager) Snapshot() Snapshot {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return cloneSnapshot(manager.snapshot)
}

func (manager *Manager) Validate(desired *configschema.Config, effective *configschema.Config) (Preview, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.previewLocked(desired, effective)
}

func (manager *Manager) previewLocked(desired, effective *configschema.Config) (Preview, error) {
	if desired == nil {
		return Preview{}, fmt.Errorf("desired configuration is required")
	}
	desired = configschema.Clone(desired)
	if manager.options.Validator != nil {
		if err := manager.options.Validator(desired); err != nil {
			return Preview{}, err
		}
	}
	if effective == nil {
		effective = manager.snapshot.Effective
	} else {
		effective = configschema.Clone(effective)
		if manager.options.Validator != nil {
			if err := manager.options.Validator(effective); err != nil {
				return Preview{}, fmt.Errorf("validate effective configuration: %w", err)
			}
		}
	}
	digest, err := configDigest(desired)
	if err != nil {
		return Preview{}, err
	}
	changedPaths, err := diffPaths(manager.snapshot.Desired, desired)
	if err != nil {
		return Preview{}, err
	}
	restart, err := diffPaths(effective, desired)
	if err != nil {
		return Preview{}, err
	}
	return Preview{CurrentRevision: manager.snapshot.Revision, NextRevision: manager.snapshot.Revision + 1, Digest: digest, Changed: digest != manager.snapshot.Digest, ChangedPaths: changedPaths, RestartRequired: restart}, nil
}

func (manager *Manager) Apply(expected uint64, desired, effective *configschema.Config, actor string, dryRun bool) (Snapshot, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return Snapshot{}, fmt.Errorf("state manager is closed")
	}
	if expected != manager.snapshot.Revision {
		return Snapshot{}, &ConflictError{Expected: expected, Current: manager.snapshot.Revision}
	}
	preview, err := manager.previewLockedWithoutLock(desired, effective)
	if err != nil {
		return Snapshot{}, err
	}
	if dryRun {
		candidate := cloneSnapshot(manager.snapshot)
		candidate.Revision = preview.NextRevision
		candidate.Digest = preview.Digest
		candidate.Desired = configschema.Clone(desired)
		if effective != nil {
			candidate.Effective = configschema.Clone(effective)
		}
		candidate.RestartRequired = append([]string(nil), preview.RestartRequired...)
		return candidate, nil
	}
	if !preview.Changed && effective == nil {
		return cloneSnapshot(manager.snapshot), nil
	}
	if strings.TrimSpace(actor) == "" {
		actor = manager.options.Actor
	}
	if effective == nil {
		effective = manager.snapshot.Effective
	} else {
		effective = configschema.Clone(effective)
	}
	effectiveDigest, err := configDigest(effective)
	if err != nil {
		return Snapshot{}, err
	}
	next := Snapshot{SchemaVersion: metadataSchemaVersion, Revision: manager.snapshot.Revision + 1, Digest: preview.Digest, EffectiveDigest: effectiveDigest, UpdatedAt: manager.options.Now().UTC(), Actor: actor, Desired: configschema.Clone(desired), Effective: configschema.Clone(effective), RestartRequired: append([]string(nil), preview.RestartRequired...)}
	if err := manager.persist(next); err != nil {
		return Snapshot{}, err
	}
	manager.snapshot = next
	return cloneSnapshot(next), nil
}

func (manager *Manager) previewLockedWithoutLock(desired, effective *configschema.Config) (Preview, error) {
	if desired == nil {
		return Preview{}, fmt.Errorf("desired configuration is required")
	}
	desired = configschema.Clone(desired)
	if manager.options.Validator != nil {
		if err := manager.options.Validator(desired); err != nil {
			return Preview{}, err
		}
	}
	if effective == nil {
		effective = manager.snapshot.Effective
	} else {
		effective = configschema.Clone(effective)
		if manager.options.Validator != nil {
			if err := manager.options.Validator(effective); err != nil {
				return Preview{}, fmt.Errorf("validate effective configuration: %w", err)
			}
		}
	}
	digest, err := configDigest(desired)
	if err != nil {
		return Preview{}, err
	}
	changed, err := diffPaths(manager.snapshot.Desired, desired)
	if err != nil {
		return Preview{}, err
	}
	restart, err := diffPaths(effective, desired)
	if err != nil {
		return Preview{}, err
	}
	return Preview{CurrentRevision: manager.snapshot.Revision, NextRevision: manager.snapshot.Revision + 1, Digest: digest, Changed: digest != manager.snapshot.Digest, ChangedPaths: changed, RestartRequired: restart}, nil
}

func (manager *Manager) Rollback(expected uint64, actor string) (Snapshot, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return Snapshot{}, fmt.Errorf("state manager is closed")
	}
	if expected != manager.snapshot.Revision {
		return Snapshot{}, &ConflictError{Expected: expected, Current: manager.snapshot.Revision}
	}
	backup, err := manager.loadBackup()
	if err != nil {
		return Snapshot{}, err
	}
	if manager.options.Validator != nil {
		if err := manager.options.Validator(backup); err != nil {
			return Snapshot{}, fmt.Errorf("validate last-known-good configuration: %w", err)
		}
	}
	digest, err := configDigest(backup)
	if err != nil {
		return Snapshot{}, err
	}
	if strings.TrimSpace(actor) == "" {
		actor = manager.options.Actor
	}
	next := Snapshot{SchemaVersion: metadataSchemaVersion, Revision: manager.snapshot.Revision + 1, Digest: digest, EffectiveDigest: manager.snapshot.EffectiveDigest, UpdatedAt: manager.options.Now().UTC(), Actor: actor, Desired: configschema.Clone(backup), Effective: configschema.Clone(manager.snapshot.Effective)}
	next.RestartRequired, _ = diffPaths(next.Effective, next.Desired)
	if err := manager.persist(next); err != nil {
		return Snapshot{}, err
	}
	manager.snapshot = next
	return cloneSnapshot(next), nil
}

func (manager *Manager) SetEffective(expected uint64, effective *configschema.Config, actor string) (Snapshot, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if expected != manager.snapshot.Revision {
		return Snapshot{}, &ConflictError{Expected: expected, Current: manager.snapshot.Revision}
	}
	if effective == nil {
		return Snapshot{}, fmt.Errorf("effective configuration is required")
	}
	effective = configschema.Clone(effective)
	if manager.options.Validator != nil {
		if err := manager.options.Validator(effective); err != nil {
			return Snapshot{}, err
		}
	}
	digest, err := configDigest(effective)
	if err != nil {
		return Snapshot{}, err
	}
	if strings.TrimSpace(actor) == "" {
		actor = manager.options.Actor
	}
	next := cloneSnapshot(manager.snapshot)
	next.Revision++
	next.Effective = effective
	next.EffectiveDigest = digest
	next.RestartRequired, _ = diffPaths(effective, next.Desired)
	next.UpdatedAt = manager.options.Now().UTC()
	next.Actor = actor
	if err := manager.persist(next); err != nil {
		return Snapshot{}, err
	}
	manager.snapshot = next
	return cloneSnapshot(next), nil
}

func (manager *Manager) Close() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil
	}
	manager.closed = true
	return manager.lock.Close()
}
func (manager *Manager) metadataPath() string       { return manager.options.Path + ".state.json" }
func (manager *Manager) backupPath() string         { return manager.options.Path + ".lkg" }
func (manager *Manager) metadataBackupPath() string { return manager.options.Path + ".state.lkg.json" }
func (manager *Manager) fault(stage FaultStage) error {
	if manager.options.FaultHook == nil {
		return nil
	}
	if err := manager.options.FaultHook(stage); err != nil {
		return fmt.Errorf("state persistence interrupted at %s: %w", stage, err)
	}
	return nil
}
