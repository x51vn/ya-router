package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	configschema "github.com/duvu/ya-router/internal/config"
)

func (manager *Manager) persist(next Snapshot) error {
	configPayload, err := marshalConfig(next.Desired)
	if err != nil {
		return err
	}
	meta := metadata{SchemaVersion: next.SchemaVersion, Revision: next.Revision, Digest: next.Digest, EffectiveDigest: next.EffectiveDigest, RestartRequired: next.RestartRequired, UpdatedAt: next.UpdatedAt, Actor: next.Actor}
	metaPayload, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	metaPayload = append(metaPayload, '\n')
	configTemp, err := writeTemp(manager.options.Path, configPayload, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(configTemp)
	metaTemp, err := writeTemp(manager.metadataPath(), metaPayload, 0o600)
	if err != nil {
		return err
	}
	defer os.Remove(metaTemp)
	if err := manager.fault(FaultBeforeBackup); err != nil {
		return err
	}
	if err := manager.backupCurrent(); err != nil {
		return err
	}
	if err := manager.fault(FaultBeforeConfigRename); err != nil {
		return err
	}
	if err := os.Rename(configTemp, manager.options.Path); err != nil {
		return fmt.Errorf("replace configuration: %w", err)
	}
	if err := manager.fault(FaultAfterConfigRename); err != nil {
		return err
	}
	if err := manager.fault(FaultBeforeMetadataRename); err != nil {
		return err
	}
	if err := os.Rename(metaTemp, manager.metadataPath()); err != nil {
		return fmt.Errorf("replace state metadata: %w", err)
	}
	if err := manager.fault(FaultAfterMetadataRename); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(manager.options.Path)); err != nil {
		return err
	}
	return nil
}

func (manager *Manager) backupCurrent() error {
	if _, err := os.Stat(manager.options.Path); err == nil {
		if err := copyFileDurable(manager.options.Path, manager.backupPath(), 0o600); err != nil {
			return fmt.Errorf("backup configuration: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(manager.metadataPath()); err == nil {
		if err := copyFileDurable(manager.metadataPath(), manager.metadataBackupPath(), 0o600); err != nil {
			return fmt.Errorf("backup state metadata: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (manager *Manager) loadBackup() (*configschema.Config, error) {
	path := manager.backupPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read last-known-good configuration: %w", err)
	}
	if manager.options.Loader != nil {
		// The loader may include legacy migration/default handling and expects a path.
		return manager.options.Loader(path)
	}
	var config configschema.Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("decode last-known-good configuration: %w", err)
	}
	return &config, nil
}

func readMetadata(path string) (*metadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value metadata
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return &value, nil
}
func writeAtomicJSON(path string, value any, mode os.FileMode, hook func() error) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	temp, err := writeTemp(path, payload, mode)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	if hook != nil {
		if err := hook(); err != nil {
			return err
		}
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
func writeTemp(path string, payload []byte, mode os.FileMode) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		return "", err
	}
	if _, err := file.Write(payload); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	ok = true
	return name, nil
}
func copyFileDurable(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	temp, err := os.CreateTemp(filepath.Dir(destination), filepath.Base(destination)+".tmp-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	ok := false
	defer func() {
		_ = temp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if _, err := io.Copy(temp, in); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, destination); err != nil {
		return err
	}
	ok = true
	return syncDirectory(filepath.Dir(destination))
}
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open state directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync state directory: %w", err)
	}
	return nil
}
func marshalConfig(config *configschema.Config) ([]byte, error) {
	payload, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}
func configDigest(config *configschema.Config) (string, error) {
	payload, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
func cloneSnapshot(source Snapshot) Snapshot {
	source.Desired = configschema.Clone(source.Desired)
	source.Effective = configschema.Clone(source.Effective)
	source.RestartRequired = append([]string(nil), source.RestartRequired...)
	return source
}
