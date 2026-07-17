package secret

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const persistedStoreVersion = 1

type persistedStore struct {
	Version int              `json:"version"`
	Entries []persistedEntry `json:"entries"`
}

// OpenFileStore opens a daemon-owned, owner-only store for managed secrets.
// Environment and official-store sources are registered separately and are
// deliberately never written to this file.
func OpenFileStore(path string, audit AuditSink) (*MemoryStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("secret store path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create secret store directory: %w", err)
	}
	entries, err := readPersistedEntries(path)
	if err != nil {
		return nil, err
	}
	store := NewMemoryStore(audit)
	for _, persisted := range entries {
		store.entries[persisted.ID] = &entry{
			value:     persisted.Value,
			source:    SourceManaged,
			version:   persisted.Version,
			updatedAt: persisted.UpdatedAt,
		}
	}
	store.persist = func(entries []persistedEntry) error {
		return writePersistedEntries(path, entries)
	}
	return store, nil
}

func readPersistedEntries(path string) ([]persistedEntry, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat secret store: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("secret store must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("secret store permissions must be owner-only")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open secret store: %w", err)
	}
	defer file.Close()
	var persisted persistedStore
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&persisted); err != nil {
		return nil, fmt.Errorf("decode secret store: %w", err)
	}
	if persisted.Version != persistedStoreVersion {
		return nil, fmt.Errorf("unsupported secret store version %d", persisted.Version)
	}
	seen := make(map[string]struct{}, len(persisted.Entries))
	for _, entry := range persisted.Entries {
		if strings.TrimSpace(entry.ID) == "" {
			return nil, fmt.Errorf("secret store contains an empty id")
		}
		if _, duplicate := seen[entry.ID]; duplicate {
			return nil, fmt.Errorf("secret store contains duplicate id %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
	}
	return persisted.Entries, nil
}

func writePersistedEntries(path string, entries []persistedEntry) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".secrets-")
	if err != nil {
		return fmt.Errorf("create secret store temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect secret store temporary file: %w", err)
	}
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(persistedStore{Version: persistedStoreVersion, Entries: entries}); err != nil {
		temporary.Close()
		return fmt.Errorf("encode secret store: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync secret store: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close secret store: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace secret store: %w", err)
	}
	return nil
}
