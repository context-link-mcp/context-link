-- Migration 003: Vector dimension metadata (Model2Vec transition)
-- Tracks the embedding dimension used, enabling detection of mismatches
-- when switching between ONNX (384-dim) and Model2Vec (128-dim) backends.

CREATE TABLE IF NOT EXISTS vec_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
