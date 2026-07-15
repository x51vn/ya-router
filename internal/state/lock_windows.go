//go:build windows

package state

import (
	"fmt"
	"os"
)

type processLock struct {
	file *os.File
	path string
}

func acquireProcessLock(path string) (*processLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("daemon state is already locked at %q; another ya-routerd process may be running", path)
		}
		return nil, fmt.Errorf("create daemon state lock %q: %w", path, err)
	}
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	_ = file.Sync()
	return &processLock{file: file, path: path}, nil
}

func (lock *processLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := lock.file.Close()
	removeErr := os.Remove(lock.path)
	if err != nil {
		return err
	}
	if removeErr != nil && !os.IsNotExist(removeErr) {
		return removeErr
	}
	return nil
}
