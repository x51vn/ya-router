package operation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type persistedStore struct {
	SchemaVersion int                  `json:"schema_version"`
	NextSequence  uint64               `json:"next_sequence"`
	Operations    []persistedOperation `json:"operations"`
	Events        []Event              `json:"events"`
}

type persistedOperation struct {
	Record          Record `json:"record"`
	IdempotencyHash string `json:"idempotency_hash,omitempty"`
	RequestDigest   string `json:"request_digest,omitempty"`
}

func loadPersisted(path string) (persistedStore, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return persistedStore{SchemaVersion: schemaVersion}, nil
		}
		return persistedStore{}, fmt.Errorf("read operations: %w", err)
	}
	var snapshot persistedStore
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return persistedStore{}, fmt.Errorf("decode operations: %w", err)
	}
	if snapshot.SchemaVersion != schemaVersion {
		return persistedStore{}, fmt.Errorf("unsupported operation schema version %d", snapshot.SchemaVersion)
	}
	return snapshot, nil
}

func persistSnapshot(path string, snapshot persistedStore) error {
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create operation directory: %w", err)
	}
	file, err := os.CreateTemp(directory, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(name)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace operation state: %w", err)
	}
	keep = true
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync operation directory: %w", err)
	}
	return nil
}
