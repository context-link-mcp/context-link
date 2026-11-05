package filelock_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store/filelock"
)

func TestTryLock_ExcludesSecondHolderUntilUnlock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "db.lock")

	first, err := filelock.TryLock(path)
	require.NoError(t, err)

	second, err := filelock.TryLock(path)
	assert.ErrorIs(t, err, filelock.ErrLocked)
	assert.Nil(t, second)

	require.NoError(t, first.Unlock())
	require.NoError(t, first.Unlock(), "a second Unlock is a no-op")

	third, err := filelock.TryLock(path)
	require.NoError(t, err)
	require.NoError(t, third.Unlock())
	assert.FileExists(t, path, "the lock file stays on disk")
}

func TestTryLock_BadPath(t *testing.T) {
	t.Parallel()
	_, err := filelock.TryLock(filepath.Join(t.TempDir(), "missing-dir", "db.lock"))
	require.Error(t, err)
	assert.False(t, errors.Is(err, filelock.ErrLocked))
}

func TestUnlock_NilLock(t *testing.T) {
	t.Parallel()
	var l *filelock.Lock
	assert.NoError(t, l.Unlock())
}

func TestAcquire_WaitsForRelease(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "db.lock")

	held, err := filelock.TryLock(path)
	require.NoError(t, err)

	var waits atomic.Int32
	done := make(chan error, 1)
	go func() {
		l, err := filelock.Acquire(context.Background(), path, 10*time.Millisecond, func() { waits.Add(1) })
		if err == nil {
			err = l.Unlock()
		}
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("Acquire returned while the lock was held: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, held.Unlock())

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire did not return after the lock was released")
	}
	assert.Equal(t, int32(1), waits.Load(), "onWait is called exactly once")
}

func TestAcquire_NoWaitCallbackWhenFree(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "db.lock")
	called := false
	l, err := filelock.Acquire(context.Background(), path, 0, func() { called = true })
	require.NoError(t, err)
	require.NoError(t, l.Unlock())
	assert.False(t, called)
}

func TestAcquire_ContextCancelStopsWait(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "db.lock")

	held, err := filelock.TryLock(path)
	require.NoError(t, err)
	defer held.Unlock() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	l, err := filelock.Acquire(ctx, path, 10*time.Millisecond, nil)
	assert.Nil(t, l)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestAcquire_CancelledBeforeStart(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := filelock.Acquire(ctx, filepath.Join(t.TempDir(), "db.lock"), 0, nil)
	require.ErrorIs(t, err, context.Canceled)
}
