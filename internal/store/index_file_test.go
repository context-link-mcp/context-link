package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/pkg/models"
)

func fileSym(name, path string, line int) models.Symbol {
	return models.Symbol{
		RepoName: "repo", Name: name, QualifiedName: name, Kind: "function",
		FilePath: path, ContentHash: "h", CodeBlock: "def " + name + "(): pass",
		StartLine: line, EndLine: line, Language: "python",
	}
}

func countRows(t *testing.T, db *DB, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(context.Background(), query, args...).Scan(&n))
	return n
}

func TestReplaceFileIndex(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)

	a := &models.File{RepoName: "repo", Path: "a.py", ContentHash: "", SizeBytes: 10}
	require.NoError(t, ReplaceFileIndex(ctx, db, a, []models.Symbol{fileSym("old", "a.py", 1)}, nil))
	require.NoError(t, ReplaceFileIndex(ctx, db, &models.File{RepoName: "repo", Path: "b.py", ContentHash: "hb"},
		[]models.Symbol{fileSym("caller", "b.py", 1)}, nil))

	old, err := GetSymbolByName(ctx, db, "repo", "old")
	require.NoError(t, err)
	caller, err := GetSymbolByName(ctx, db, "repo", "caller")
	require.NoError(t, err)
	require.NoError(t, BatchInsertDependencies(ctx, db, []models.Dependency{
		{CallerID: old.ID, CalleeID: caller.ID, Kind: "call"},
		{CallerID: caller.ID, CalleeID: old.ID, Kind: "call"},
	}))
	require.NoError(t, BatchInsertFTSSymbols(ctx, db, []FTSSymbol{{SymbolID: old.ID, RepoName: "repo", Name: "old"}}))

	a.ContentHash = "ha"
	sites := []CallSite{{Line: 1, Kind: "call", Name: "caller"}, {Line: 1, Kind: "import", Name: "b", Qualifier: "b"}}
	require.NoError(t, ReplaceFileIndex(ctx, db, a, []models.Symbol{fileSym("fresh", "a.py", 1)}, sites))

	_, err = GetSymbolByName(ctx, db, "repo", "old")
	assert.ErrorIs(t, err, ErrSymbolNotFound)
	_, err = GetSymbolByName(ctx, db, "repo", "fresh")
	assert.NoError(t, err)
	assert.Zero(t, countRows(t, db, `SELECT COUNT(*) FROM dependencies`), "edges of the old symbols are removed both ways")
	assert.Zero(t, countRows(t, db, `SELECT COUNT(*) FROM fts_symbols`))
	pending, err := GetPendingResolution(ctx, db, "repo")
	require.NoError(t, err)
	assert.Equal(t, []string{"b.py"}, pending, "b.py's edge into a.py is to be resolved again")
	got2, err := GetCallSitesByFiles(ctx, db, "repo", []string{"a.py", "b.py"})
	require.NoError(t, err)
	assert.Equal(t, map[string][]CallSite{"a.py": sites}, got2)

	got, err := GetFileByPath(ctx, db, "repo", "a.py")
	require.NoError(t, err)
	assert.Equal(t, "ha", got.ContentHash)
}

func TestReplaceFileIndex_RollsBackOnFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)

	f := &models.File{RepoName: "repo", Path: "a.py", ContentHash: "h1"}
	require.NoError(t, ReplaceFileIndex(ctx, db, f, []models.Symbol{fileSym("keep", "a.py", 1)}, nil))
	_, err := db.ExecContext(ctx, `CREATE TRIGGER fail_boom BEFORE INSERT ON symbols
		WHEN NEW.name = 'boom' BEGIN SELECT RAISE(ABORT, 'injected'); END`)
	require.NoError(t, err)

	f.ContentHash = "h2"
	err = ReplaceFileIndex(ctx, db, f, []models.Symbol{fileSym("new", "a.py", 1), fileSym("boom", "a.py", 2)}, nil)
	require.Error(t, err)

	_, err = GetSymbolByName(ctx, db, "repo", "keep")
	assert.NoError(t, err, "old symbols survive")
	_, err = GetSymbolByName(ctx, db, "repo", "new")
	assert.ErrorIs(t, err, ErrSymbolNotFound)
	got, err := GetFileByPath(ctx, db, "repo", "a.py")
	require.NoError(t, err)
	assert.Equal(t, "h1", got.ContentHash, "the hash is not advanced")
}

func TestSetFileHashes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)

	require.NoError(t, UpsertFile(ctx, db, &models.File{RepoName: "repo", Path: "a.py"}))
	require.NoError(t, UpsertFile(ctx, db, &models.File{RepoName: "other", Path: "a.py", ContentHash: "x"}))
	require.NoError(t, SetFileHashes(ctx, db, "repo", map[string]string{"a.py": "ha", "missing.py": "hm"}))
	require.NoError(t, SetFileHashes(ctx, db, "repo", nil))

	hashes, err := GetFileHashIndex(ctx, db, "repo")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"a.py": "ha"}, hashes, "missing paths are not created")
	other, err := GetFileHashIndex(ctx, db, "other")
	require.NoError(t, err)
	assert.Equal(t, "x", other["a.py"], "other repos are untouched")
}
