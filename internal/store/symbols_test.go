package store

import (
	"context"
	"testing"

	"github.com/context-link-mcp/context-link/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchInsertSymbols(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	symbols := []models.Symbol{
		{
			RepoName: "repo", Name: "main", QualifiedName: "main",
			Kind: "function", FilePath: "src/main.ts", ContentHash: "h1",
			CodeBlock: "function main() {}", StartLine: 1, EndLine: 1, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "UserAuth", QualifiedName: "UserAuth",
			Kind: "class", FilePath: "src/auth.ts", ContentHash: "h2",
			CodeBlock: "class UserAuth {}", StartLine: 5, EndLine: 20, Language: "typescript",
		},
	}

	err := BatchInsertSymbols(ctx, db, symbols)
	require.NoError(t, err)

	count, err := CountSymbols(ctx, db, "repo")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestGetSymbolByName(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "validateToken", "UserAuth.validateToken", "function", "src/auth.ts")

	sym, err := GetSymbolByName(ctx, db, "repo", "validateToken")
	require.NoError(t, err)
	assert.Equal(t, "validateToken", sym.Name)
	assert.Equal(t, "UserAuth.validateToken", sym.QualifiedName)
}

func TestGetSymbolByName_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	_, err := GetSymbolByName(ctx, db, "repo", "nonexistent")
	assert.ErrorIs(t, err, ErrSymbolNotFound)
}

func TestGetSymbolByQualifiedName(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "validateToken", "UserAuth.validateToken", "function", "src/auth.ts")

	sym, err := GetSymbolByQualifiedName(ctx, db, "repo", "UserAuth.validateToken")
	require.NoError(t, err)
	assert.Equal(t, "UserAuth.validateToken", sym.QualifiedName)
}

func TestSearchSymbolsByName(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "validateToken", "UserAuth.validateToken", "function", "src/auth.ts")
	insertTestSymbol(t, db, "repo", "generateToken", "UserAuth.generateToken", "function", "src/auth.ts")
	insertTestSymbol(t, db, "repo", "UserAuth", "UserAuth", "class", "src/auth.ts")

	results, err := SearchSymbolsByName(ctx, db, "repo", "Token", 10)
	require.NoError(t, err)
	assert.Len(t, results, 2) // validateToken and generateToken
}

func TestGetSymbolsByFile(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "foo", "foo", "function", "src/a.ts")
	insertTestSymbol(t, db, "repo", "bar", "bar", "function", "src/a.ts")
	insertTestSymbol(t, db, "repo", "baz", "baz", "function", "src/b.ts")

	syms, err := GetSymbolsByFile(ctx, db, "repo", "src/a.ts")
	require.NoError(t, err)
	assert.Len(t, syms, 2)
}

func TestDeleteSymbolsByFile(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "foo", "foo", "function", "src/a.ts")
	insertTestSymbol(t, db, "repo", "bar", "bar", "function", "src/b.ts")

	require.NoError(t, DeleteSymbolsByFile(ctx, db, "repo", "src/a.ts"))

	count, err := CountSymbols(ctx, db, "repo")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Remaining symbol should be "bar".
	sym, err := GetSymbolByName(ctx, db, "repo", "bar")
	require.NoError(t, err)
	assert.Equal(t, "bar", sym.Name)
}

func TestGetSymbolNameIndex(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	insertTestSymbol(t, db, "repo", "foo", "MyClass.foo", "method", "src/a.ts")
	insertTestSymbol(t, db, "repo", "bar", "bar", "function", "src/a.ts")

	index, err := GetSymbolNameIndex(ctx, db, "repo")
	require.NoError(t, err)

	_, hasFoo := index["foo"]
	_, hasQualified := index["MyClass.foo"]
	_, hasBar := index["bar"]
	assert.True(t, hasFoo)
	assert.True(t, hasQualified)
	assert.True(t, hasBar)
}

func TestGetSymbolWithDependencies_NoSymbol(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	_, _, err := GetSymbolWithDependencies(ctx, db, "repo", "nonexistent", 1)
	assert.ErrorIs(t, err, ErrSymbolNotFound)
}

func TestBatchInsertSymbols_Empty(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	err := BatchInsertSymbols(ctx, db, nil)
	assert.NoError(t, err)
}

func TestBatchInsertSymbols_Replace(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert initial symbol.
	insertTestSymbol(t, db, "repo", "foo", "foo", "function", "src/a.ts")

	// Re-insert with updated code_block (same qualified_name + file_path).
	symbols := []models.Symbol{
		{
			RepoName: "repo", Name: "foo", QualifiedName: "foo",
			Kind: "function", FilePath: "src/a.ts", ContentHash: "newhash",
			CodeBlock: "function foo() { return 42; }", StartLine: 1, EndLine: 1, Language: "typescript",
		},
	}
	require.NoError(t, BatchInsertSymbols(ctx, db, symbols))

	sym, err := GetSymbolByName(ctx, db, "repo", "foo")
	require.NoError(t, err)
	assert.Equal(t, "newhash", sym.ContentHash)
}

func TestGetSymbolWithDependencies_TransitiveBFS(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Create a chain: A → B → C → D
	insertTestSymbol(t, db, "repo", "A", "A", "function", "src/a.ts")
	insertTestSymbol(t, db, "repo", "B", "B", "function", "src/b.ts")
	insertTestSymbol(t, db, "repo", "C", "C", "function", "src/c.ts")
	insertTestSymbol(t, db, "repo", "D", "D", "function", "src/d.ts")

	symA, _ := GetSymbolByName(ctx, db, "repo", "A")
	symB, _ := GetSymbolByName(ctx, db, "repo", "B")
	symC, _ := GetSymbolByName(ctx, db, "repo", "C")
	symD, _ := GetSymbolByName(ctx, db, "repo", "D")

	require.NoError(t, InsertDependency(ctx, db, &models.Dependency{CallerID: symA.ID, CalleeID: symB.ID, Kind: "call"}))
	require.NoError(t, InsertDependency(ctx, db, &models.Dependency{CallerID: symB.ID, CalleeID: symC.ID, Kind: "call"}))
	require.NoError(t, InsertDependency(ctx, db, &models.Dependency{CallerID: symC.ID, CalleeID: symD.ID, Kind: "call"}))

	// depth=0: no deps
	_, deps, err := GetSymbolWithDependencies(ctx, db, "repo", "A", 0)
	require.NoError(t, err)
	assert.Empty(t, deps)

	// depth=1: only B
	_, deps, err = GetSymbolWithDependencies(ctx, db, "repo", "A", 1)
	require.NoError(t, err)
	assert.Len(t, deps, 1)
	assert.Equal(t, "B", deps[0].Name)

	// depth=2: B and C
	_, deps, err = GetSymbolWithDependencies(ctx, db, "repo", "A", 2)
	require.NoError(t, err)
	assert.Len(t, deps, 2)
	names := []string{deps[0].Name, deps[1].Name}
	assert.Contains(t, names, "B")
	assert.Contains(t, names, "C")

	// depth=3: B, C, and D
	_, deps, err = GetSymbolWithDependencies(ctx, db, "repo", "A", 3)
	require.NoError(t, err)
	assert.Len(t, deps, 3)
	names = []string{deps[0].Name, deps[1].Name, deps[2].Name}
	assert.Contains(t, names, "B")
	assert.Contains(t, names, "C")
	assert.Contains(t, names, "D")

	// Verify no duplicates with diamond: A → B, A → C, B → D, C → D
	require.NoError(t, InsertDependency(ctx, db, &models.Dependency{CallerID: symA.ID, CalleeID: symC.ID, Kind: "call"}))
	_, deps, err = GetSymbolWithDependencies(ctx, db, "repo", "A", 3)
	require.NoError(t, err)
	assert.Len(t, deps, 3) // B, C, D — no duplicates
}

// insertTestSymbol is a test helper that inserts a single symbol.
func insertTestSymbol(t *testing.T, db *DB, repoName, name, qualifiedName, kind, filePath string) {
	t.Helper()
	err := BatchInsertSymbols(context.Background(), db, []models.Symbol{
		{
			RepoName: repoName, Name: name, QualifiedName: qualifiedName,
			Kind: kind, FilePath: filePath, ContentHash: "hash_" + name,
			CodeBlock: kind + " " + name + "() {}", StartLine: 1, EndLine: 1, Language: "typescript",
		},
	})
	require.NoError(t, err)
}
