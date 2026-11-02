package store

import (
	"context"
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

// execWithoutForeignKeys runs query with foreign keys off, to write rows that
// point at missing symbols as databases from before Open enforced them hold.
func execWithoutForeignKeys(t *testing.T, db *DB, query string, args ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`)
	require.NoError(t, err)
	defer conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`) //nolint:errcheck
	_, err = conn.ExecContext(ctx, query, args...)
	require.NoError(t, err)
}
