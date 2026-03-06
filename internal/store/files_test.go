package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/context-link/context-link/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpsertFile_Insert(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	f := &models.File{
		RepoName:    "testrepo",
		Path:        "src/main.ts",
		ContentHash: "abc123",
		SizeBytes:   1024,
	}

	err := UpsertFile(ctx, db, f)
	require.NoError(t, err)

	got, err := GetFileByPath(ctx, db, "testrepo", "src/main.ts")
	require.NoError(t, err)
	assert.Equal(t, "testrepo", got.RepoName)
	assert.Equal(t, "src/main.ts", got.Path)
	assert.Equal(t, "abc123", got.ContentHash)
	assert.Equal(t, int64(1024), got.SizeBytes)
}

func TestUpsertFile_Update(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	f := &models.File{RepoName: "repo", Path: "a.ts", ContentHash: "hash1", SizeBytes: 100}
	require.NoError(t, UpsertFile(ctx, db, f))

	// Update with new hash.
	f.ContentHash = "hash2"
	f.SizeBytes = 200
	require.NoError(t, UpsertFile(ctx, db, f))

	got, err := GetFileByPath(ctx, db, "repo", "a.ts")
	require.NoError(t, err)
	assert.Equal(t, "hash2", got.ContentHash)
	assert.Equal(t, int64(200), got.SizeBytes)
}

func TestGetFileByPath_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	_, err := GetFileByPath(ctx, db, "repo", "nonexistent.ts")
	assert.ErrorIs(t, err, ErrFileNotFound)
}

func TestListFiles(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	require.NoError(t, UpsertFile(ctx, db, &models.File{RepoName: "repo", Path: "b.ts", ContentHash: "h2", SizeBytes: 50}))
	require.NoError(t, UpsertFile(ctx, db, &models.File{RepoName: "repo", Path: "a.ts", ContentHash: "h1", SizeBytes: 100}))
	require.NoError(t, UpsertFile(ctx, db, &models.File{RepoName: "other", Path: "c.ts", ContentHash: "h3", SizeBytes: 75}))

	files, err := ListFiles(ctx, db, "repo")
	require.NoError(t, err)
	assert.Len(t, files, 2)
	assert.Equal(t, "a.ts", files[0].Path) // sorted alphabetically
	assert.Equal(t, "b.ts", files[1].Path)
}

func TestDeleteFileByPath(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	require.NoError(t, UpsertFile(ctx, db, &models.File{RepoName: "repo", Path: "a.ts", ContentHash: "h1", SizeBytes: 100}))
	require.NoError(t, DeleteFileByPath(ctx, db, "repo", "a.ts"))

	_, err := GetFileByPath(ctx, db, "repo", "a.ts")
	assert.ErrorIs(t, err, ErrFileNotFound)
}

// setupTestDB creates a temporary SQLite database for testing.
func setupTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, Migrate(db))
	t.Cleanup(func() { db.Close() })
	return db
}
