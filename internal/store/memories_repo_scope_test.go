package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

// seedStaleMemories gives repo two stale memories on the same path as every
// other repo: one still linked to its symbol, one orphaned (symbol detached).
func seedStaleMemories(t *testing.T, db *store.DB, repo string) (linkedSymID int64) {
	t.Helper()
	ctx := context.Background()

	linkedSymID = seedSymbol(t, db, repo, "Login", "auth.go")
	_, err := store.SaveMemory(ctx, db, &models.Memory{
		SymbolID: &linkedSymID, Note: repo + " linked", Author: "agent",
		LastKnownSymbol: "Login", LastKnownFile: "auth.go",
	})
	require.NoError(t, err)
	_, err = store.SnapshotAndMarkStale(ctx, db, linkedSymID, "Login", "auth.go", "hash_changed")
	require.NoError(t, err)

	goneSymID := seedSymbol(t, db, repo, "Logout", "auth.go")
	_, err = store.SaveMemory(ctx, db, &models.Memory{
		SymbolID: &goneSymID, Note: repo + " orphan", Author: "agent",
		LastKnownSymbol: "Logout", LastKnownFile: "auth.go",
	})
	require.NoError(t, err)
	_, err = store.SnapshotAndMarkStale(ctx, db, goneSymID, "Logout", "auth.go", "symbol_deleted")
	require.NoError(t, err)
	require.NoError(t, store.DetachMemoriesFromSymbol(ctx, db, goneSymID))
	_, err = db.ExecContext(ctx, `DELETE FROM symbols WHERE id = ?`, goneSymID)
	require.NoError(t, err)
	return linkedSymID
}

func memoryNotes(t *testing.T, db *store.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT note FROM memories ORDER BY note`)
	require.NoError(t, err)
	defer rows.Close()
	var notes []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		notes = append(notes, n)
	}
	require.NoError(t, rows.Err())
	return notes
}

func TestPurgeStaleMemories_ScopedToRepo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		orphanedOnly bool
		wantPurged   int
		wantLeft     []string
	}{
		{"all stale", false, 2, []string{"repoB linked", "repoB orphan"}},
		{"orphaned only", true, 1, []string{"repoA linked", "repoB linked", "repoB orphan"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := store.SetupTestDB(t)
			seedStaleMemories(t, db, "repoA")
			seedStaleMemories(t, db, "repoB")

			n, err := store.PurgeStaleMemories(context.Background(), db, "repoA", tt.orphanedOnly)
			require.NoError(t, err)
			assert.Equal(t, tt.wantPurged, n)
			assert.Equal(t, tt.wantLeft, memoryNotes(t, db), "repoB's memories must survive a repoA purge")
		})
	}
}

func TestGetAllOrphanedMemories_ScopedToRepo(t *testing.T) {
	t.Parallel()
	db := store.SetupTestDB(t)
	ctx := context.Background()
	seedStaleMemories(t, db, "repoA")
	seedStaleMemories(t, db, "repoB")

	orphans, err := store.GetAllOrphanedMemories(ctx, db, "repoA")
	require.NoError(t, err)
	require.Len(t, orphans, 1)
	got := orphans["Logout\x00auth.go"]
	require.Len(t, got, 1)
	assert.Equal(t, "repoA orphan", got[0].Note)
	assert.Equal(t, "repoA", got[0].RepoName)

	byName, err := store.GetOrphanedMemoriesBySymbol(ctx, db, "repoB", "Logout", "auth.go")
	require.NoError(t, err)
	require.Len(t, byName, 1)
	assert.Equal(t, "repoB orphan", byName[0].Note)
}

func TestGetMemories_IncludesOrphansOfCurrentRepo(t *testing.T) {
	t.Parallel()
	db := store.SetupTestDB(t)
	ctx := context.Background()
	seedStaleMemories(t, db, "repoA")
	seedStaleMemories(t, db, "repoB")

	tests := []struct {
		name  string
		query func() ([]models.Memory, error)
		want  []string
	}{
		{"by symbol name, orphan", func() ([]models.Memory, error) {
			return store.GetMemoriesBySymbolName(ctx, db, "repoA", "Logout", 0, 10)
		}, []string{"repoA orphan"}},
		{"by symbol name, linked", func() ([]models.Memory, error) {
			return store.GetMemoriesBySymbolName(ctx, db, "repoB", "Login", 0, 10)
		}, []string{"repoB linked"}},
		{"by file path", func() ([]models.Memory, error) {
			return store.GetMemoriesByFilePath(ctx, db, "repoA", "auth.go", 0, 10)
		}, []string{"repoA linked", "repoA orphan"}},
		{"unknown repo sees nothing", func() ([]models.Memory, error) {
			return store.GetMemoriesByFilePath(ctx, db, "repoC", "auth.go", 0, 10)
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mems, err := tt.query()
			require.NoError(t, err)
			var notes []string
			for _, m := range mems {
				notes = append(notes, m.Note)
			}
			assert.ElementsMatch(t, tt.want, notes)
		})
	}
}

func TestSaveMemory_RepoNameFromSymbol(t *testing.T) {
	t.Parallel()
	db := store.SetupTestDB(t)
	ctx := context.Background()

	symID := seedSymbol(t, db, "repoA", "Login", "auth.go")
	_, err := store.SaveMemory(ctx, db, &models.Memory{SymbolID: &symID, Note: "n", Author: "agent"})
	require.NoError(t, err)

	mems, err := store.GetMemoriesBySymbolID(ctx, db, symID)
	require.NoError(t, err)
	require.Len(t, mems, 1)
	assert.Equal(t, "repoA", mems[0].RepoName, "repo_name is derived from the linked symbol when not given")
}
