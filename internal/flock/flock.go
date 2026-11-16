// Package flock serialises devc processes with exclusive file locks.
package flock

import (
	"fmt"
	"os"
	"path/filepath"
)

// Lock takes an exclusive lock on the file at path, creating it and its
// directory, and waits until the lock is free. The returned function
// releases it.
func Lock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() { f.Close() }, nil
}
