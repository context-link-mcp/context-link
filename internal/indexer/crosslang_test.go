package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/context-link/context-link/internal/indexer/adapters"
	"github.com/context-link/context-link/internal/store"
	"github.com/context-link/context-link/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCrossLanguage_SchemaConsistency verifies that symbols from Go and TS
// adapters are stored with consistent schema fields and can coexist in the
// same database scoped by repo_name.
func TestCrossLanguage_SchemaConsistency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)
	projectRoot := findProjectRoot(t)

	// Parse Go fixture.
	goAdapter := adapters.NewGoAdapter()
	goFixture := filepath.Join(projectRoot, "testdata", "langs", "go", "sample.go")
	goSource, err := os.ReadFile(goFixture)
	require.NoError(t, err)

	poolMgr := NewParserPoolManager()
	goTree, err := poolMgr.GetPool(goAdapter).Parse(ctx, goSource)
	require.NoError(t, err)

	extractor := NewExtractor()
	goSymbols, err := extractor.ExtractSymbols(ctx, goTree, goSource, goAdapter, "go-repo", "sample.go")
	require.NoError(t, err)

	// Parse TS fixture.
	tsAdapter := adapters.NewTypeScriptAdapter()
	tsFixture := filepath.Join(projectRoot, "testdata", "langs", "ts", "auth.ts")
	tsSource, err := os.ReadFile(tsFixture)
	require.NoError(t, err)

	tsTree, err := poolMgr.GetPool(tsAdapter).Parse(ctx, tsSource)
	require.NoError(t, err)

	tsSymbols, err := extractor.ExtractSymbols(ctx, tsTree, tsSource, tsAdapter, "ts-repo", "auth.ts")
	require.NoError(t, err)

	// Insert both into the same database.
	require.NoError(t, store.BatchInsertSymbols(ctx, db, goSymbols))
	require.NoError(t, store.BatchInsertSymbols(ctx, db, tsSymbols))

	// Verify repo scoping: each repo sees only its own symbols.
	goCount, err := store.CountSymbols(ctx, db, "go-repo")
	require.NoError(t, err)
	assert.Equal(t, int64(len(goSymbols)), goCount, "Go repo symbol count mismatch")

	tsCount, err := store.CountSymbols(ctx, db, "ts-repo")
	require.NoError(t, err)
	assert.Equal(t, int64(len(tsSymbols)), tsCount, "TS repo symbol count mismatch")

	// Verify symbols have correct language fields.
	goSym, err := store.GetSymbolByName(ctx, db, "go-repo", "NewCache")
	require.NoError(t, err)
	assert.Equal(t, "go", goSym.Language)
	assert.Equal(t, "function", goSym.Kind)
	assert.NotEmpty(t, goSym.CodeBlock)
	assert.NotEmpty(t, goSym.ContentHash)
	assert.Greater(t, goSym.StartLine, 0)
	assert.GreaterOrEqual(t, goSym.EndLine, goSym.StartLine)

	tsSym, err := store.GetSymbolByName(ctx, db, "ts-repo", "validateToken")
	require.NoError(t, err)
	assert.Equal(t, "typescript", tsSym.Language)
	assert.Equal(t, "method", tsSym.Kind)
	assert.NotEmpty(t, tsSym.CodeBlock)
	assert.NotEmpty(t, tsSym.ContentHash)
	assert.Greater(t, tsSym.StartLine, 0)
	assert.GreaterOrEqual(t, tsSym.EndLine, tsSym.StartLine)

	// Verify qualified names work across languages.
	tsQual, err := store.GetSymbolByQualifiedName(ctx, db, "ts-repo", "UserAuth.validateToken")
	require.NoError(t, err)
	assert.Equal(t, "UserAuth.validateToken", tsQual.QualifiedName)

	// Verify search is scoped by repo.
	results, err := store.SearchSymbolsByName(ctx, db, "go-repo", "Cache", 10)
	require.NoError(t, err)
	for _, r := range results {
		assert.Equal(t, "go-repo", r.RepoName, "search returned symbol from wrong repo")
	}
}

// TestCrossLanguage_DependencyEdgeKinds verifies all dependency kinds work
// correctly with symbols from different languages.
func TestCrossLanguage_DependencyEdgeKinds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)

	// Insert symbols simulating Go and TS in the same repo.
	allSymbols := []models.Symbol{
		{RepoName: "test-repo", Name: "HandleRequest", QualifiedName: "HandleRequest", Kind: "function", FilePath: "handler.go", ContentHash: "h1", CodeBlock: "func HandleRequest() {}", StartLine: 1, EndLine: 3, Language: "go"},
		{RepoName: "test-repo", Name: "Cache", QualifiedName: "Cache", Kind: "type", FilePath: "cache.go", ContentHash: "h2", CodeBlock: "type Cache struct{}", StartLine: 1, EndLine: 3, Language: "go"},
		{RepoName: "test-repo", Name: "UserAuth", QualifiedName: "UserAuth", Kind: "class", FilePath: "auth.ts", ContentHash: "h3", CodeBlock: "class UserAuth {}", StartLine: 1, EndLine: 10, Language: "typescript"},
		{RepoName: "test-repo", Name: "BaseAuth", QualifiedName: "BaseAuth", Kind: "class", FilePath: "base.ts", ContentHash: "h4", CodeBlock: "class BaseAuth {}", StartLine: 1, EndLine: 5, Language: "typescript"},
		{RepoName: "test-repo", Name: "AuthInterface", QualifiedName: "AuthInterface", Kind: "interface", FilePath: "types.ts", ContentHash: "h5", CodeBlock: "interface AuthInterface {}", StartLine: 1, EndLine: 5, Language: "typescript"},
	}
	require.NoError(t, store.BatchInsertSymbols(ctx, db, allSymbols))

	// Lookup IDs.
	handleReq, err := store.GetSymbolByName(ctx, db, "test-repo", "HandleRequest")
	require.NoError(t, err)
	cache, err := store.GetSymbolByName(ctx, db, "test-repo", "Cache")
	require.NoError(t, err)
	userAuth, err := store.GetSymbolByName(ctx, db, "test-repo", "UserAuth")
	require.NoError(t, err)
	baseAuth, err := store.GetSymbolByName(ctx, db, "test-repo", "BaseAuth")
	require.NoError(t, err)
	authIface, err := store.GetSymbolByName(ctx, db, "test-repo", "AuthInterface")
	require.NoError(t, err)

	// Insert all dependency edge kinds.
	deps := []models.Dependency{
		{CallerID: handleReq.ID, CalleeID: cache.ID, Kind: "call"},
		{CallerID: userAuth.ID, CalleeID: baseAuth.ID, Kind: "extends"},
		{CallerID: userAuth.ID, CalleeID: authIface.ID, Kind: "implements"},
	}
	require.NoError(t, store.BatchInsertDependencies(ctx, db, deps))

	// Verify transitive resolution: HandleRequest → Cache (depth 1).
	sym, foundDeps, err := store.GetSymbolWithDependencies(ctx, db, "test-repo", "HandleRequest", 1)
	require.NoError(t, err)
	assert.Equal(t, "HandleRequest", sym.Name)
	require.Len(t, foundDeps, 1)
	assert.Equal(t, "Cache", foundDeps[0].Name)

	// Verify extends/implements edges.
	_, userAuthDeps, err := store.GetSymbolWithDependencies(ctx, db, "test-repo", "UserAuth", 1)
	require.NoError(t, err)
	require.Len(t, userAuthDeps, 2)
	depNames := map[string]bool{}
	for _, d := range userAuthDeps {
		depNames[d.Name] = true
	}
	assert.True(t, depNames["BaseAuth"], "missing extends dependency")
	assert.True(t, depNames["AuthInterface"], "missing implements dependency")

	// Verify reverse lookup: who depends on Cache?
	reverseDeps, err := store.GetDependenciesByCallee(ctx, db, cache.ID)
	require.NoError(t, err)
	require.Len(t, reverseDeps, 1)
	assert.Equal(t, handleReq.ID, reverseDeps[0].CallerID)
}

// setupTestDB creates a temporary SQLite database for testing.
func setupTestDB(t *testing.T) *store.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(dbPath)
	require.NoError(t, err)
	require.NoError(t, store.Migrate(db))
	t.Cleanup(func() { db.Close() })
	return db
}
