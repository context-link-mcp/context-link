-- Migration 001: Initial schema
-- Creates all core tables for context-link Phase 1–4.
-- Uses IF NOT EXISTS on all objects for idempotency.

-- Schema version tracking (created by the Go migration runner itself,
-- but included here for documentation purposes).
CREATE TABLE IF NOT EXISTS schema_version (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    filename    TEXT NOT NULL UNIQUE,
    applied_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- File registry for incremental re-indexing.
CREATE TABLE IF NOT EXISTS files (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_name     TEXT NOT NULL,
    path          TEXT NOT NULL,
    content_hash  TEXT NOT NULL,
    last_indexed  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    size_bytes    INTEGER,
    UNIQUE(repo_name, path)
);
CREATE INDEX IF NOT EXISTS idx_files_hash ON files(content_hash);
CREATE INDEX IF NOT EXISTS idx_files_repo  ON files(repo_name);

-- Symbol registry: every parsed code symbol.
CREATE TABLE IF NOT EXISTS symbols (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_name      TEXT NOT NULL,
    name           TEXT NOT NULL,
    qualified_name TEXT NOT NULL,
    kind           TEXT NOT NULL,
    file_path      TEXT NOT NULL,
    content_hash   TEXT NOT NULL,
    code_block     TEXT NOT NULL,
    start_line     INTEGER NOT NULL,
    end_line       INTEGER NOT NULL,
    language       TEXT NOT NULL DEFAULT 'typescript',
    indexed_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_symbols_name      ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_file      ON symbols(repo_name, file_path);
CREATE UNIQUE INDEX IF NOT EXISTS idx_symbols_qualified ON symbols(repo_name, qualified_name, file_path);

-- Dependency graph: directed edges between symbols.
CREATE TABLE IF NOT EXISTS dependencies (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    caller_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    callee_id INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    kind      TEXT NOT NULL DEFAULT 'call',
    UNIQUE(caller_id, callee_id, kind)
);
CREATE INDEX IF NOT EXISTS idx_deps_caller ON dependencies(caller_id);
CREATE INDEX IF NOT EXISTS idx_deps_callee ON dependencies(callee_id);

-- Memory store: persistent notes linked to symbols.
-- Uses ON DELETE SET NULL so orphaned memories survive symbol deletion.
CREATE TABLE IF NOT EXISTS memories (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    symbol_id          INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
    note               TEXT NOT NULL,
    author             TEXT NOT NULL DEFAULT 'agent',
    is_stale           BOOLEAN DEFAULT 0,
    stale_reason       TEXT,
    last_known_symbol  TEXT,
    last_known_file    TEXT,
    created_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_memories_symbol   ON memories(symbol_id);
CREATE INDEX IF NOT EXISTS idx_memories_orphaned ON memories(symbol_id) WHERE symbol_id IS NULL;
