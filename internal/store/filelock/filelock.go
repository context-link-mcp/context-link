// Package filelock provides an exclusive, advisory, cross-process lock on a
// file: LockFileEx on Windows, flock on Unix.
//
// The operating system releases the lock when the holding process exits or
// crashes, so a lock can never be left stale. The lock file itself stays on
// disk after Unlock; it holds no data, and its presence means nothing.
//
// Locks are taken per open file, not per process: two Lock values on the same
// path exclude each other even within one process. On network filesystems
// the guarantee is only as good as the filesystem's lock support (NFS and SMB
// implementations vary).
package filelock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// ErrLocked is returned by TryLock when another holder has the lock.
var ErrLocked = errors.New("filelock: locked by another holder")

// DefaultPoll is the interval at which Acquire retries TryLock.
const DefaultPoll = 150 * time.Millisecond

// Lock is a held lock. Release it with Unlock.
type Lock struct {
	f *os.File
}

// TryLock creates path if needed (mode 0600) and takes an exclusive lock on
// it without blocking. It returns ErrLocked if the lock is held elsewhere.
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("filelock: failed to open %s: %w", path, err)
	}
	if err := tryLockFile(f); err != nil {
		f.Close() //nolint:errcheck
		if errors.Is(err, ErrLocked) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("filelock: failed to lock %s: %w", path, err)
	}
	return &Lock{f: f}, nil
}

// Acquire takes the lock on path, retrying TryLock every poll interval
// (DefaultPoll if poll <= 0) until it succeeds or ctx ends. onWait, if not
// nil, is called once, the first time the lock turns out to be held.
func Acquire(ctx context.Context, path string, poll time.Duration, onWait func()) (*Lock, error) {
	if poll <= 0 {
		poll = DefaultPoll
	}
	var ticker *time.Ticker
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("filelock: interrupted while waiting for %s: %w", path, err)
		}
		l, err := TryLock(path)
		if err == nil {
			return l, nil
		}
		if !errors.Is(err, ErrLocked) {
			return nil, err
		}
		if ticker == nil {
			ticker = time.NewTicker(poll)
			defer ticker.Stop()
			if onWait != nil {
				onWait()
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil, fmt.Errorf("filelock: interrupted while waiting for %s: %w", path, ctx.Err())
		}
	}
}

// Unlock releases the lock and closes the lock file. It is safe to call on a
// nil Lock and more than once.
func (l *Lock) Unlock() error {
	if l == nil || l.f == nil {
		return nil
	}
	uerr := unlockFile(l.f)
	cerr := l.f.Close()
	l.f = nil
	if uerr != nil {
		return fmt.Errorf("filelock: failed to unlock: %w", uerr)
	}
	if cerr != nil {
		return fmt.Errorf("filelock: failed to close lock file: %w", cerr)
	}
	return nil
}
