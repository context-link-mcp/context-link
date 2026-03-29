package store

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// SetupTestDB creates a temporary in-memory database for testing.
// Exported for use in black-box tests (package store_test).
func SetupTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, Migrate(db))
	t.Cleanup(func() { db.Close() })
	return db
}

// setupTestDB is an alias for white-box tests (package store).
func setupTestDB(t *testing.T) *DB {
	return SetupTestDB(t)
}
