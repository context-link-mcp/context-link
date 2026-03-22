package server_test

import (
	"path/filepath"
	"testing"

	"github.com/context-link-mcp/context-link/internal/config"
	"github.com/context-link-mcp/context-link/internal/server"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/stretchr/testify/require"
)

// openTestDB opens a migrated in-memory SQLite database for testing.
func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	require.NoError(t, store.Migrate(db))
	t.Cleanup(func() { db.Close() })
	return db
}

// TestNew_DoesNotPanic verifies that creating a Server registers all tools
// and prompts without panicking, even when the embedder is nil.
func TestNew_DoesNotPanic(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	cfg := &config.Config{
		ProjectRoot: t.TempDir(),
		DBPath:      filepath.Join(t.TempDir(), "test.db"),
	}

	require.NotPanics(t, func() {
		s := server.New(cfg, db, nil, "test")
		require.NotNil(t, s)
	})
}

// TestNew_WithNilEmbedder verifies that the server starts correctly when
// semantic search is disabled (no embedder configured).
func TestNew_WithNilEmbedder(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	cfg := &config.Config{
		ProjectRoot: t.TempDir(),
		DBPath:      filepath.Join(t.TempDir(), "test.db"),
	}

	s := server.New(cfg, db, nil, "test")
	require.NotNil(t, s, "server must not be nil even with nil embedder")
}
