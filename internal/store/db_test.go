package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/context-link/context-link/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpen(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := store.Open(dbPath)
	require.NoError(t, err)
	require.NotNil(t, db)
	defer db.Close()

	// Verify the database file was created.
	_, err = os.Stat(dbPath)
	assert.NoError(t, err, "database file should exist after Open")

	// Verify we can ping it.
	err = db.Ping()
	assert.NoError(t, err)
}

func TestMigrate_Fresh(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	defer db.Close()

	err = store.Migrate(db)
	require.NoError(t, err, "first migration run should succeed")

	// Verify core tables exist.
	tables := []string{"files", "symbols", "dependencies", "memories", "schema_version", "vec_symbols"}
	for _, table := range tables {
		var count int
		err = db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&count)
		require.NoError(t, err)
		assert.Equal(t, 1, count, "table %q should exist after migration", table)
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	defer db.Close()

	// Run migrations twice — should not error or duplicate.
	err = store.Migrate(db)
	require.NoError(t, err, "first run should succeed")

	err = store.Migrate(db)
	require.NoError(t, err, "second run (idempotency) should also succeed")

	// Verify schema_version has exactly one row per migration file (no duplicates).
	var total, distinct int
	err = db.QueryRow(`SELECT COUNT(*), COUNT(DISTINCT filename) FROM schema_version`).Scan(&total, &distinct)
	require.NoError(t, err)
	assert.Equal(t, total, distinct, "each migration file should be recorded exactly once (no duplicates)")
}
