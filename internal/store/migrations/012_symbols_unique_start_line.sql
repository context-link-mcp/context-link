-- Same-name symbols in one file: Go's multiple init() functions, overloads in
-- Java, C#, C++, Kotlin and Swift, a Rust struct and its impl blocks. The old
-- unique key (repo_name, qualified_name, file_path) made INSERT OR REPLACE keep
-- only the last of them; the extractor already tells them apart by start line.
--
-- Only the index changes, so no row is deleted and no ON DELETE action runs:
-- memories, edges, embeddings and routes keep their symbols. The old key was
-- stricter, so the existing rows satisfy the new one.
DROP INDEX IF EXISTS idx_symbols_qualified;
CREATE UNIQUE INDEX IF NOT EXISTS idx_symbols_qualified
    ON symbols(repo_name, qualified_name, file_path, start_line);

-- The symbol queries changed in the same version (function-local variables
-- are no longer symbols, methods keep their container prefix, dropped
-- overloads come back). Blank every file hash, as 007/009/010 did, so the next
-- normal index re-indexes every file. Edges are kept until then: each file's
-- re-index replaces its own, and memories are detached and relinked by name
-- and file on the way.
UPDATE files SET content_hash = '';
