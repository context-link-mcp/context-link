package tools

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link/context-link/internal/store"
)

// setupMemoryDB creates a migrated DB with a single symbol pre-seeded.
func setupMemoryDB(t *testing.T) (*store.DB, int64) {
	t.Helper()
	db := openToolTestDB(t)
	symID := insertSymbol(t, db, "repo", "validateToken", "function", "func validateToken() {}")
	return db, symID
}

func TestSaveMemoryHandler_Found(t *testing.T) {
	t.Parallel()
	db, _ := setupMemoryDB(t)

	h := saveMemoryHandler(db, "repo")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "validateToken",
		"note":        "This validates JWT tokens.",
	}
	result, err := h(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestSaveMemoryHandler_SymbolNotFound(t *testing.T) {
	t.Parallel()
	db, _ := setupMemoryDB(t)

	h := saveMemoryHandler(db, "repo")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "nonExistent",
		"note":        "some note",
	}
	result, err := h(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestSaveMemoryHandler_MissingNote(t *testing.T) {
	t.Parallel()
	db, _ := setupMemoryDB(t)

	h := saveMemoryHandler(db, "repo")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"symbol_name": "validateToken"}
	result, err := h(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestSaveMemoryHandler_MissingSymbolName(t *testing.T) {
	t.Parallel()
	db, _ := setupMemoryDB(t)

	h := saveMemoryHandler(db, "repo")
	result, err := callHandler(t, h)
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestSaveMemoryHandler_NoteTooLong(t *testing.T) {
	t.Parallel()
	db, _ := setupMemoryDB(t)

	longNote := make([]rune, 2001)
	for i := range longNote {
		longNote[i] = 'x'
	}

	h := saveMemoryHandler(db, "repo")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "validateToken",
		"note":        string(longNote),
	}
	result, err := h(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestSaveMemoryHandler_DuplicateRejected(t *testing.T) {
	t.Parallel()
	db, _ := setupMemoryDB(t)

	h := saveMemoryHandler(db, "repo")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "validateToken",
		"note":        "unique note",
	}

	res1, err := h(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, res1.IsError, "first save should succeed")

	res2, err := h(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, res2.IsError, "duplicate save should fail")
}

func TestGetMemoriesHandler_BySymbolName(t *testing.T) {
	t.Parallel()
	db, _ := setupMemoryDB(t)

	// Save a memory first.
	h := saveMemoryHandler(db, "repo")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"symbol_name": "validateToken", "note": "test note"}
	_, err := h(context.Background(), req)
	require.NoError(t, err)

	gh := getMemoriesHandler(db, "repo")
	req2 := mcp.CallToolRequest{}
	req2.Params.Arguments = map[string]any{"symbol_name": "validateToken"}
	result, err := gh(context.Background(), req2)
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestGetMemoriesHandler_ByFilePath(t *testing.T) {
	t.Parallel()
	db, _ := setupMemoryDB(t)

	h := saveMemoryHandler(db, "repo")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"symbol_name": "validateToken", "note": "file note"}
	_, err := h(context.Background(), req)
	require.NoError(t, err)

	gh := getMemoriesHandler(db, "repo")
	req2 := mcp.CallToolRequest{}
	req2.Params.Arguments = map[string]any{"file_path": "test.ts"}
	result, err := gh(context.Background(), req2)
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestGetMemoriesHandler_NeitherParam(t *testing.T) {
	t.Parallel()
	db, _ := setupMemoryDB(t)

	gh := getMemoriesHandler(db, "repo")
	result, err := callHandler(t, gh)
	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestPurgeMemoriesHandler(t *testing.T) {
	t.Parallel()
	db, symID := setupMemoryDB(t)
	ctx := context.Background()

	// Save and mark stale.
	_, err := db.ExecContext(ctx, `
		INSERT INTO memories (symbol_id, note, author, is_stale, last_known_symbol, last_known_file)
		VALUES (?, 'stale note', 'agent', 1, 'validateToken', 'test.ts')
	`, symID)
	require.NoError(t, err)

	h := purgeMemoriesHandler(db, "repo")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"orphaned_only": false}
	result, err := h(ctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestGetMemoriesHandler_Pagination(t *testing.T) {
	t.Parallel()
	db, _ := setupMemoryDB(t)
	ctx := context.Background()

	sh := saveMemoryHandler(db, "repo")
	for _, note := range []string{"note1", "note2", "note3"} {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{"symbol_name": "validateToken", "note": note}
		_, err := sh(ctx, req)
		require.NoError(t, err)
	}

	gh := getMemoriesHandler(db, "repo")
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"symbol_name": "validateToken",
		"limit":       float64(2),
		"offset":      float64(0),
	}
	result, err := gh(ctx, req)
	require.NoError(t, err)
	assert.False(t, result.IsError)
}
