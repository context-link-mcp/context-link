-- Foreign keys were never enforced before this version: store.Open passed
-- mattn-style DSN parameters that modernc.org/sqlite ignores, so ON DELETE
-- CASCADE / SET NULL never ran. Clean up the rows left pointing at symbols
-- that no longer exist; from now on the schema's own actions keep it clean.

-- Callers that still exist lose an edge below: mark their files for
-- re-resolution, as every other edge drop does (see markEdgeCallers).
INSERT OR IGNORE INTO pending_resolution (repo_name, file_path)
SELECT DISTINCT caller.repo_name, caller.file_path
FROM dependencies d
JOIN symbols caller ON caller.id = d.caller_id
WHERE d.callee_id NOT IN (SELECT id FROM symbols);

DELETE FROM dependencies
WHERE caller_id NOT IN (SELECT id FROM symbols)
   OR callee_id NOT IN (SELECT id FROM symbols);

DELETE FROM vec_symbols WHERE symbol_id NOT IN (SELECT id FROM symbols);

UPDATE routes SET handler_symbol_id = NULL
WHERE handler_symbol_id IS NOT NULL
  AND handler_symbol_id NOT IN (SELECT id FROM symbols);

-- Memories linked to a missing symbol become stale orphans, like those of a
-- deleted symbol. Blank the hash of the file they were last seen in first,
-- so the next normal index re-processes it and relinks them by name and file.
UPDATE files SET content_hash = ''
WHERE EXISTS (
    SELECT 1 FROM memories m
    WHERE m.symbol_id IS NOT NULL
      AND m.symbol_id NOT IN (SELECT id FROM symbols)
      AND m.repo_name = files.repo_name
      AND m.last_known_file = files.path
);

UPDATE memories
SET symbol_id = NULL,
    is_stale = 1,
    stale_reason = COALESCE(stale_reason, 'symbol_deleted')
WHERE symbol_id IS NOT NULL
  AND symbol_id NOT IN (SELECT id FROM symbols);
