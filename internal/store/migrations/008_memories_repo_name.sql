-- Migration 008: scope memories by repo.
-- memories had no repo_name, so purge and orphan relinking matched rows from
-- every repo sharing the DB. Add the column and backfill it.

ALTER TABLE memories ADD COLUMN repo_name TEXT;

-- Linked memories take the repo of their symbol.
UPDATE memories
SET repo_name = (SELECT s.repo_name FROM symbols s WHERE s.id = memories.symbol_id)
WHERE repo_name IS NULL AND symbol_id IS NOT NULL;

-- Rows still unattributed (orphans, or links to a symbol that no longer exists)
-- can only be attributed when the DB has ever held exactly one repo. In a
-- shared DB they stay NULL: no repo purges, relinks or reads them, so they are
-- kept rather than risk deleting or relinking another repo's note.
UPDATE memories
SET repo_name = (SELECT repo_name FROM files UNION SELECT repo_name FROM symbols)
WHERE repo_name IS NULL
  AND (SELECT COUNT(*) FROM (SELECT repo_name FROM files UNION SELECT repo_name FROM symbols)) = 1;

CREATE INDEX IF NOT EXISTS idx_memories_repo_orphaned
    ON memories(repo_name, last_known_symbol, last_known_file) WHERE symbol_id IS NULL;
