-- Migration 002: Vector embeddings for semantic search (Phase 3)
-- Stores L2-normalized float32 embeddings as BLOBs for Go-side KNN search.

CREATE TABLE IF NOT EXISTS vec_symbols (
    symbol_id  INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    repo_name  TEXT NOT NULL,
    embedding  BLOB NOT NULL,  -- float32 array, 384 dims, little-endian IEEE 754
    UNIQUE(symbol_id)
);
CREATE INDEX IF NOT EXISTS idx_vec_symbols_repo ON vec_symbols(repo_name);
