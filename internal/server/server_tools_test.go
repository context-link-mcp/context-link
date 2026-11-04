package server

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/config"
	"github.com/context-link-mcp/context-link/internal/store"
)

// registeredTools returns the sorted names of the tools a server built from
// the given tools config exposes.
func registeredTools(t *testing.T, enabled []string) []string {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "tools.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	require.NoError(t, store.Migrate(db))

	cfg := &config.Config{ProjectRoot: t.TempDir(), Tools: enabled}
	s := New(cfg, db, nil, nil, "test")
	var names []string
	for name := range s.mcp.ListTools() {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestRegisterTools_MemoryToolsIndividually: naming one memory tool exposes
// only that tool. On master any memory tool name registered all three,
// including the destructive purge_stale_memories.
func TestRegisterTools_MemoryToolsIndividually(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		enabled []string
		want    []string
	}{
		{"read only", []string{"get_symbol_memories"}, []string{"get_symbol_memories"}},
		{"save only", []string{"save_symbol_memory"}, []string{"save_symbol_memory"}},
		{"save and read", []string{"save_symbol_memory", "get_symbol_memories"},
			[]string{"get_symbol_memories", "save_symbol_memory"}},
		{"group", []string{"memory"},
			[]string{"get_symbol_memories", "purge_stale_memories", "save_symbol_memory"}},
		{"purge only", []string{"purge_stale_memories"}, []string{"purge_stale_memories"}},
		{"unrelated", []string{"ping"}, []string{"ping"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, registeredTools(t, tc.enabled))
		})
	}
}

// TestRegisterTools_AllByDefault: an empty tools list still exposes every
// memory tool.
func TestRegisterTools_AllByDefault(t *testing.T) {
	t.Parallel()
	names := registeredTools(t, nil)
	for _, want := range []string{"save_symbol_memory", "get_symbol_memories", "purge_stale_memories", "search_code_patterns"} {
		assert.Contains(t, names, want)
	}
}
