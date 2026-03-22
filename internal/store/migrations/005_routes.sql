-- Route definitions and call sites for cross-service HTTP route detection.
CREATE TABLE IF NOT EXISTS routes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_name TEXT NOT NULL,
    method TEXT NOT NULL,
    path_pattern TEXT NOT NULL,
    normalized_path TEXT NOT NULL,
    handler_symbol_id INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
    file_path TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    framework TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'definition',
    indexed_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_routes_repo ON routes(repo_name, method);
CREATE INDEX IF NOT EXISTS idx_routes_path ON routes(repo_name, normalized_path);
