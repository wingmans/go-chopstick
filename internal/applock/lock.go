// Package applock prevents multiple application owners from using one data
// root concurrently.
package applock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// ErrLocked means another process owns the application data root.
var ErrLocked = errors.New("application data is already in use")

// Lock owns an advisory process lock until Close is called.
type Lock struct {
	file *os.File
}

// Acquire obtains an exclusive, non-blocking lock for path.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open application lock: %w", err)
	}

	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()

		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}

		return nil, fmt.Errorf("acquire application lock: %w", err)
	}

	return &Lock{file: file}, nil
}

// Close releases the process lock.
func (l *Lock) Close() error {
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()

	return errors.Join(unlockErr, closeErr)
}
