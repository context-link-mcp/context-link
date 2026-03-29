package store

import (
	"context"
	"fmt"
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

func TestGetSymbolByQualifiedName_NotFound(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	_, err := GetSymbolByQualifiedName(ctx, db, "repo", "NonExistent.Symbol")
	assert.ErrorIs(t, err, ErrSymbolNotFound)
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

	symA, err := GetSymbolByName(ctx, db, "repo", "A")
	require.NoError(t, err)
	symB, err := GetSymbolByName(ctx, db, "repo", "B")
	require.NoError(t, err)
	symC, err := GetSymbolByName(ctx, db, "repo", "C")
	require.NoError(t, err)
	symD, err := GetSymbolByName(ctx, db, "repo", "D")
	require.NoError(t, err)

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

func TestSearchCodePatterns_BasicMatch(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert symbols with specific code patterns
	err := BatchInsertSymbols(ctx, db, []models.Symbol{
		{
			RepoName: "repo", Name: "findUser", QualifiedName: "findUser",
			Kind: "function", FilePath: "src/users.ts", ContentHash: "h1",
			CodeBlock: "function findUser() { return db.query('SELECT * FROM users'); }",
			StartLine: 1, EndLine: 1, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "findPost", QualifiedName: "findPost",
			Kind: "function", FilePath: "src/posts.ts", ContentHash: "h2",
			CodeBlock: "function findPost() { return db.query('SELECT * FROM posts'); }",
			StartLine: 5, EndLine: 5, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "createUser", QualifiedName: "createUser",
			Kind: "function", FilePath: "src/users.ts", ContentHash: "h3",
			CodeBlock: "function createUser() { return db.insert('users'); }",
			StartLine: 10, EndLine: 10, Language: "typescript",
		},
	})
	require.NoError(t, err)

	// Search for "db.query" pattern
	results, err := SearchCodePatterns(ctx, db, "repo", "db.query", "", "", 50)
	require.NoError(t, err)
	assert.Len(t, results, 2, "should match functions with db.query")
}

func TestSearchCodePatterns_FilePrefix(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert symbols in different directories
	err := BatchInsertSymbols(ctx, db, []models.Symbol{
		{
			RepoName: "repo", Name: "authFunc", QualifiedName: "authFunc",
			Kind: "function", FilePath: "src/auth/helpers.ts", ContentHash: "h1",
			CodeBlock: "function authFunc() { return true; }",
			StartLine: 1, EndLine: 1, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "utilFunc", QualifiedName: "utilFunc",
			Kind: "function", FilePath: "src/utils/helpers.ts", ContentHash: "h2",
			CodeBlock: "function utilFunc() { return true; }",
			StartLine: 5, EndLine: 5, Language: "typescript",
		},
	})
	require.NoError(t, err)

	// Search with file path prefix filter
	results, err := SearchCodePatterns(ctx, db, "repo", "function", "src/auth", "", 50)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "authFunc", results[0].Name)
}

func TestSearchCodePatterns_KindFilter(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert symbols of different kinds
	err := BatchInsertSymbols(ctx, db, []models.Symbol{
		{
			RepoName: "repo", Name: "MyClass", QualifiedName: "MyClass",
			Kind: "class", FilePath: "src/models.ts", ContentHash: "h1",
			CodeBlock: "class MyClass { constructor() {} }",
			StartLine: 1, EndLine: 3, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "myFunction", QualifiedName: "myFunction",
			Kind: "function", FilePath: "src/utils.ts", ContentHash: "h2",
			CodeBlock: "function myFunction() { return constructor(); }",
			StartLine: 5, EndLine: 5, Language: "typescript",
		},
	})
	require.NoError(t, err)

	// Search for "constructor" pattern but filter by kind=class
	results, err := SearchCodePatterns(ctx, db, "repo", "constructor", "", "class", 50)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "MyClass", results[0].Name)
}

func TestSearchCodePatterns_CombinedFilters(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert various symbols
	err := BatchInsertSymbols(ctx, db, []models.Symbol{
		{
			RepoName: "repo", Name: "authHandler", QualifiedName: "authHandler",
			Kind: "function", FilePath: "src/auth/handlers.ts", ContentHash: "h1",
			CodeBlock: "function authHandler() { return authenticate(); }",
			StartLine: 1, EndLine: 1, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "AuthService", QualifiedName: "AuthService",
			Kind: "class", FilePath: "src/auth/service.ts", ContentHash: "h2",
			CodeBlock: "class AuthService { authenticate() {} }",
			StartLine: 5, EndLine: 7, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "utilAuth", QualifiedName: "utilAuth",
			Kind: "function", FilePath: "src/utils/auth.ts", ContentHash: "h3",
			CodeBlock: "function utilAuth() { return authenticate(); }",
			StartLine: 10, EndLine: 10, Language: "typescript",
		},
	})
	require.NoError(t, err)

	// Search with all filters: pattern + file prefix + kind
	results, err := SearchCodePatterns(ctx, db, "repo", "authenticate", "src/auth", "function", 50)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "authHandler", results[0].Name)
}

func TestSearchCodePatterns_LimitDefault(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert 60 symbols
	var symbols []models.Symbol
	for i := 0; i < 60; i++ {
		symbols = append(symbols, models.Symbol{
			RepoName: "repo", Name: fmt.Sprintf("func%d", i), QualifiedName: fmt.Sprintf("func%d", i),
			Kind: "function", FilePath: "src/file.ts", ContentHash: fmt.Sprintf("h%d", i),
			CodeBlock: fmt.Sprintf("function func%d() { return true; }", i),
			StartLine: i, EndLine: i, Language: "typescript",
		})
	}
	err := BatchInsertSymbols(ctx, db, symbols)
	require.NoError(t, err)

	// Search with default limit (0 should use 50)
	results, err := SearchCodePatterns(ctx, db, "repo", "return true", "", "", 0)
	require.NoError(t, err)
	assert.Len(t, results, 50, "default limit should be 50")
}

func TestSearchCodePatterns_LimitMax(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert 250 symbols
	var symbols []models.Symbol
	for i := 0; i < 250; i++ {
		symbols = append(symbols, models.Symbol{
			RepoName: "repo", Name: fmt.Sprintf("func%d", i), QualifiedName: fmt.Sprintf("func%d", i),
			Kind: "function", FilePath: "src/file.ts", ContentHash: fmt.Sprintf("h%d", i),
			CodeBlock: fmt.Sprintf("function func%d() { return true; }", i),
			StartLine: i, EndLine: i, Language: "typescript",
		})
	}
	err := BatchInsertSymbols(ctx, db, symbols)
	require.NoError(t, err)

	// Search with limit > 200 (should cap at 200)
	results, err := SearchCodePatterns(ctx, db, "repo", "return true", "", "", 500)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(results), 200, "limit should cap at 200")
}

func TestSearchCodePatterns_NoResults(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert symbols
	err := BatchInsertSymbols(ctx, db, []models.Symbol{
		{
			RepoName: "repo", Name: "myFunc", QualifiedName: "myFunc",
			Kind: "function", FilePath: "src/utils.ts", ContentHash: "h1",
			CodeBlock: "function myFunc() { return false; }",
			StartLine: 1, EndLine: 1, Language: "typescript",
		},
	})
	require.NoError(t, err)

	// Search for non-existent pattern
	results, err := SearchCodePatterns(ctx, db, "repo", "nonexistent_pattern_xyz", "", "", 50)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchCodePatterns_SpecialChars(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Insert symbols with special characters
	err := BatchInsertSymbols(ctx, db, []models.Symbol{
		{
			RepoName: "repo", Name: "regexFunc", QualifiedName: "regexFunc",
			Kind: "function", FilePath: "src/utils.ts", ContentHash: "h1",
			CodeBlock: "function regexFunc() { return /[a-z]+/.test(str); }",
			StartLine: 1, EndLine: 1, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "arrayFunc", QualifiedName: "arrayFunc",
			Kind: "function", FilePath: "src/utils.ts", ContentHash: "h2",
			CodeBlock: "function arrayFunc() { return [1, 2, 3]; }",
			StartLine: 5, EndLine: 5, Language: "typescript",
		},
	})
	require.NoError(t, err)

	// Search for patterns with special SQL LIKE characters
	results, err := SearchCodePatterns(ctx, db, "repo", "[a-z]", "", "", 50)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "regexFunc", results[0].Name)
}

func TestGetCallTree_Callees(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Create a call chain: root -> level1 -> level2
	symbols := []models.Symbol{
		{
			RepoName: "repo", Name: "root", QualifiedName: "root",
			Kind: "function", FilePath: "src/main.ts", ContentHash: "h1",
			CodeBlock: "function root() {}", StartLine: 1, EndLine: 1, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "level1", QualifiedName: "level1",
			Kind: "function", FilePath: "src/main.ts", ContentHash: "h2",
			CodeBlock: "function level1() {}", StartLine: 5, EndLine: 5, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "level2", QualifiedName: "level2",
			Kind: "function", FilePath: "src/main.ts", ContentHash: "h3",
			CodeBlock: "function level2() {}", StartLine: 10, EndLine: 10, Language: "typescript",
		},
	}
	err := BatchInsertSymbols(ctx, db, symbols)
	require.NoError(t, err)

	// Get symbol IDs
	root, err := GetSymbolByName(ctx, db, "repo", "root")
	require.NoError(t, err)
	level1, err := GetSymbolByName(ctx, db, "repo", "level1")
	require.NoError(t, err)
	level2, err := GetSymbolByName(ctx, db, "repo", "level2")
	require.NoError(t, err)

	// Create dependencies
	err = BatchInsertDependencies(ctx, db, []models.Dependency{
		{CallerID: root.ID, CalleeID: level1.ID, Kind: "call"},
		{CallerID: level1.ID, CalleeID: level2.ID, Kind: "call"},
	})
	require.NoError(t, err)

	// Get call tree (callees direction)
	edges, err := GetCallTree(ctx, db, root.ID, "callees", 2)
	require.NoError(t, err)
	assert.Len(t, edges, 2, "should return level1 and level2")

	// Verify depths
	assert.Equal(t, 1, edges[0].Depth)
	assert.Equal(t, 2, edges[1].Depth)
}

func TestGetCallTree_Callers(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Create a reverse call chain: caller1, caller2 -> target
	symbols := []models.Symbol{
		{
			RepoName: "repo", Name: "target", QualifiedName: "target",
			Kind: "function", FilePath: "src/main.ts", ContentHash: "h1",
			CodeBlock: "function target() {}", StartLine: 1, EndLine: 1, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "caller1", QualifiedName: "caller1",
			Kind: "function", FilePath: "src/main.ts", ContentHash: "h2",
			CodeBlock: "function caller1() {}", StartLine: 5, EndLine: 5, Language: "typescript",
		},
		{
			RepoName: "repo", Name: "caller2", QualifiedName: "caller2",
			Kind: "function", FilePath: "src/main.ts", ContentHash: "h3",
			CodeBlock: "function caller2() {}", StartLine: 10, EndLine: 10, Language: "typescript",
		},
	}
	err := BatchInsertSymbols(ctx, db, symbols)
	require.NoError(t, err)

	// Get symbol IDs
	target, err := GetSymbolByName(ctx, db, "repo", "target")
	require.NoError(t, err)
	caller1, err := GetSymbolByName(ctx, db, "repo", "caller1")
	require.NoError(t, err)
	caller2, err := GetSymbolByName(ctx, db, "repo", "caller2")
	require.NoError(t, err)

	// Create dependencies
	err = BatchInsertDependencies(ctx, db, []models.Dependency{
		{CallerID: caller1.ID, CalleeID: target.ID, Kind: "call"},
		{CallerID: caller2.ID, CalleeID: target.ID, Kind: "call"},
	})
	require.NoError(t, err)

	// Get call tree (callers direction)
	edges, err := GetCallTree(ctx, db, target.ID, "callers", 1)
	require.NoError(t, err)
	assert.Len(t, edges, 2, "should return caller1 and caller2")
	assert.Equal(t, 1, edges[0].Depth)
	assert.Equal(t, 1, edges[1].Depth)
}

func TestGetCallTree_MaxDepth(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Create a deep call chain: a -> b -> c -> d -> e
	symbols := []models.Symbol{
		{RepoName: "repo", Name: "a", QualifiedName: "a", Kind: "function", FilePath: "f.ts", ContentHash: "h1", CodeBlock: "function a() {}", StartLine: 1, EndLine: 1, Language: "typescript"},
		{RepoName: "repo", Name: "b", QualifiedName: "b", Kind: "function", FilePath: "f.ts", ContentHash: "h2", CodeBlock: "function b() {}", StartLine: 2, EndLine: 2, Language: "typescript"},
		{RepoName: "repo", Name: "c", QualifiedName: "c", Kind: "function", FilePath: "f.ts", ContentHash: "h3", CodeBlock: "function c() {}", StartLine: 3, EndLine: 3, Language: "typescript"},
		{RepoName: "repo", Name: "d", QualifiedName: "d", Kind: "function", FilePath: "f.ts", ContentHash: "h4", CodeBlock: "function d() {}", StartLine: 4, EndLine: 4, Language: "typescript"},
		{RepoName: "repo", Name: "e", QualifiedName: "e", Kind: "function", FilePath: "f.ts", ContentHash: "h5", CodeBlock: "function e() {}", StartLine: 5, EndLine: 5, Language: "typescript"},
	}
	err := BatchInsertSymbols(ctx, db, symbols)
	require.NoError(t, err)

	// Get symbol IDs
	a, err := GetSymbolByName(ctx, db, "repo", "a")
	require.NoError(t, err)
	b, err := GetSymbolByName(ctx, db, "repo", "b")
	require.NoError(t, err)
	c, err := GetSymbolByName(ctx, db, "repo", "c")
	require.NoError(t, err)
	d, err := GetSymbolByName(ctx, db, "repo", "d")
	require.NoError(t, err)
	e, err := GetSymbolByName(ctx, db, "repo", "e")
	require.NoError(t, err)

	// Create deep chain
	err = BatchInsertDependencies(ctx, db, []models.Dependency{
		{CallerID: a.ID, CalleeID: b.ID, Kind: "call"},
		{CallerID: b.ID, CalleeID: c.ID, Kind: "call"},
		{CallerID: c.ID, CalleeID: d.ID, Kind: "call"},
		{CallerID: d.ID, CalleeID: e.ID, Kind: "call"},
	})
	require.NoError(t, err)

	// Max depth is capped at 3
	edges, err := GetCallTree(ctx, db, a.ID, "callees", 10)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(edges), 3, "max depth should be capped at 3")
}

func TestGetCallTree_CircularDeps(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Create circular dependency: a -> b -> a
	symbols := []models.Symbol{
		{RepoName: "repo", Name: "a", QualifiedName: "a", Kind: "function", FilePath: "f.ts", ContentHash: "h1", CodeBlock: "function a() {}", StartLine: 1, EndLine: 1, Language: "typescript"},
		{RepoName: "repo", Name: "b", QualifiedName: "b", Kind: "function", FilePath: "f.ts", ContentHash: "h2", CodeBlock: "function b() {}", StartLine: 2, EndLine: 2, Language: "typescript"},
	}
	err := BatchInsertSymbols(ctx, db, symbols)
	require.NoError(t, err)

	a, err := GetSymbolByName(ctx, db, "repo", "a")
	require.NoError(t, err)
	b, err := GetSymbolByName(ctx, db, "repo", "b")
	require.NoError(t, err)

	err = BatchInsertDependencies(ctx, db, []models.Dependency{
		{CallerID: a.ID, CalleeID: b.ID, Kind: "call"},
		{CallerID: b.ID, CalleeID: a.ID, Kind: "call"},
	})
	require.NoError(t, err)

	// Should handle circular dependencies without infinite loop
	edges, err := GetCallTree(ctx, db, a.ID, "callees", 3)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(edges), 1, "should only visit b once (cycle detection)")
}

func TestFindDeadSymbols_Complex(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Create symbols: main (entry) -> used, orphan (no callers), exported
	symbols := []models.Symbol{
		{RepoName: "repo", Name: "main", QualifiedName: "main", Kind: "function", FilePath: "f.ts", ContentHash: "h1", CodeBlock: "function main() {}", StartLine: 1, EndLine: 1, Language: "typescript"},
		{RepoName: "repo", Name: "used", QualifiedName: "used", Kind: "function", FilePath: "f.ts", ContentHash: "h2", CodeBlock: "function used() {}", StartLine: 2, EndLine: 2, Language: "typescript"},
		{RepoName: "repo", Name: "orphan", QualifiedName: "orphan", Kind: "function", FilePath: "f.ts", ContentHash: "h3", CodeBlock: "function orphan() {}", StartLine: 3, EndLine: 3, Language: "typescript"},
		{RepoName: "repo", Name: "Exported", QualifiedName: "Exported", Kind: "function", FilePath: "f.ts", ContentHash: "h4", CodeBlock: "function Exported() {}", StartLine: 4, EndLine: 4, Language: "typescript"},
	}
	err := BatchInsertSymbols(ctx, db, symbols)
	require.NoError(t, err)

	main, err := GetSymbolByName(ctx, db, "repo", "main")
	require.NoError(t, err)
	used, err := GetSymbolByName(ctx, db, "repo", "used")
	require.NoError(t, err)

	err = BatchInsertDependencies(ctx, db, []models.Dependency{
		{CallerID: main.ID, CalleeID: used.ID, Kind: "call"},
	})
	require.NoError(t, err)

	// Find dead symbols (exclude exported)
	deadSymbols, err := FindDeadSymbols(ctx, db, "repo", DeadCodeOptions{
		ExcludeExported: true,
		Limit:           50,
	})
	require.NoError(t, err)

	// Should find orphan (not main, not used, not Exported)
	assert.Len(t, deadSymbols, 1)
	assert.Equal(t, "orphan", deadSymbols[0].Name)
}

func TestGetCallTree_EdgeCap(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	ctx := context.Background()

	// Create root with 150 direct callees (exceeds 100 edge cap)
	var symbols []models.Symbol
	symbols = append(symbols, models.Symbol{
		RepoName: "repo", Name: "root", QualifiedName: "root", Kind: "function",
		FilePath: "f.ts", ContentHash: "h0", CodeBlock: "function root() {}",
		StartLine: 1, EndLine: 1, Language: "typescript",
	})

	for i := 1; i <= 150; i++ {
		symbols = append(symbols, models.Symbol{
			RepoName: "repo", Name: fmt.Sprintf("callee%d", i), QualifiedName: fmt.Sprintf("callee%d", i),
			Kind: "function", FilePath: "f.ts", ContentHash: fmt.Sprintf("h%d", i),
			CodeBlock: "function callee() {}", StartLine: i + 1, EndLine: i + 1, Language: "typescript",
		})
	}
	err := BatchInsertSymbols(ctx, db, symbols)
	require.NoError(t, err)

	root, err := GetSymbolByName(ctx, db, "repo", "root")
	require.NoError(t, err)

	// Create dependencies
	var deps []models.Dependency
	for i := 1; i <= 150; i++ {
		callee, err := GetSymbolByName(ctx, db, "repo", fmt.Sprintf("callee%d", i))
		require.NoError(t, err)
		deps = append(deps, models.Dependency{
			CallerID: root.ID,
			CalleeID: callee.ID,
			Kind:     "call",
		})
	}
	err = BatchInsertDependencies(ctx, db, deps)
	require.NoError(t, err)

	// Should cap at 100 edges
	edges, err := GetCallTree(ctx, db, root.ID, "callees", 1)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(edges), 100, "should cap at 100 edges")
}
