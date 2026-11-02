-- Scoped dependency resolution and crash-safe re-resolution.
--
-- call_sites keeps every raw reference extracted from a file (calls,
-- extends/implements and imports), so a file's edges can be resolved again
-- without re-parsing it: when a file it calls into is re-indexed or deleted,
-- or when a name it calls is added or removed elsewhere. For calls, qualifier
-- is the receiver ('' for a plain call, '*' for a complex expression); for
-- imports, name is the import source and qualifier the local name it binds.
CREATE TABLE IF NOT EXISTS call_sites (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_name  TEXT NOT NULL,
    file_path  TEXT NOT NULL,
    line       INTEGER NOT NULL,
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL,
    qualifier  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_call_sites_file ON call_sites(repo_name, file_path);
CREATE INDEX IF NOT EXISTS idx_call_sites_name ON call_sites(repo_name, name);

-- Files whose outgoing edges must be resolved again. A row is written in the
-- same transaction that detaches edges into a changed or deleted file, and
-- deleted in the transaction that writes the re-resolved edges, so an
-- interrupted run leaves it for the next one.
CREATE TABLE IF NOT EXISTS pending_resolution (
    repo_name  TEXT NOT NULL,
    file_path  TEXT NOT NULL,
    PRIMARY KEY (repo_name, file_path)
);

-- Edges written before this version were resolved by bare name across the
-- whole repo, and no file has call sites yet. Same as 007/009: drop every edge
-- and blank every file hash, so the next normal index is a full re-index.
DELETE FROM dependencies;
UPDATE files SET content_hash = '';
