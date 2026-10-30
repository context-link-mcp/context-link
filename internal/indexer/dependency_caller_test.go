package indexer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/pkg/models"
)

func TestEnclosingSymbol(t *testing.T) {
	t.Parallel()
	syms := []models.Symbol{
		{Name: "router", StartLine: 1, EndLine: 1},
		{Name: "Service", StartLine: 3, EndLine: 10},
		{Name: "run", StartLine: 4, EndLine: 6},
		{Name: "outer", StartLine: 12, EndLine: 12},
		{Name: "inner", StartLine: 12, EndLine: 12},
	}
	tests := []struct {
		name string
		line int
		want string
		ok   bool
	}{
		{"single-line symbol", 1, "router", true},
		{"method beats its class", 5, "run", true},
		{"class body outside methods", 8, "Service", true},
		{"same span and start: first in list", 12, "outer", true},
		{"module-level line", 2, "", false},
		{"after every symbol", 20, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := enclosingSymbol(syms, tt.line)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got.Name)
		})
	}
}

const callerAttributionSource = `import os

router = make_router()


def helper():
    return 1


class Service:
    def run(self):
        return helper()


def handler():
    return helper()


def make_router():
    return None


print(helper())
`

// TestResolveDependenciesAttributesCallsToEnclosingSymbol guards against every
// call in a file being attached to the file's first symbol.
func TestResolveDependenciesAttributesCallsToEnclosingSymbol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := setupTestDB(t)

	registry := NewLanguageRegistry()
	require.NoError(t, registry.Register(adapters.NewPythonAdapter()))
	root := t.TempDir()
	writeGoFile(t, root, "app.py", callerAttributionSource)

	stats, err := NewIndexer(registry, db, 1, nil).IndexRepo(ctx, "repo", root)
	require.NoError(t, err)
	require.Equal(t, 0, stats.Errors)

	sym := func(qualifiedName string) *models.Symbol {
		s, err := store.GetSymbolByQualifiedName(ctx, db, "repo", qualifiedName)
		require.NoError(t, err, qualifiedName)
		return s
	}
	callees := func(qualifiedName string) []int64 {
		deps, err := store.GetDependenciesByCaller(ctx, db, sym(qualifiedName).ID)
		require.NoError(t, err)
		var ids []int64
		for _, d := range deps {
			ids = append(ids, d.CalleeID)
		}
		return ids
	}
	helper := sym("helper").ID

	assert.Equal(t, []int64{sym("make_router").ID}, callees("router"))
	assert.Equal(t, []int64{helper}, callees("Service.run"))
	assert.Equal(t, []int64{helper}, callees("handler"))
	assert.Empty(t, callees("helper"))
	assert.Empty(t, callees("Service"), "the call inside run belongs to run, not its class")

	callers, err := store.GetDependenciesByCallee(ctx, db, helper)
	require.NoError(t, err)
	assert.Len(t, callers, 2, "module-level print(helper()) has no calling symbol")
}
