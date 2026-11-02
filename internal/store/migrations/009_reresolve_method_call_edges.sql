-- Method and selector calls (s.Load(), pkg.Func(), self.helper(), this.helper())
-- produced no edges before this version, and incremental runs left edges from
-- unchanged files pointing at deleted symbol IDs. Same as 007: drop every edge
-- and blank every file hash, so the next normal index is a full re-index that
-- rebuilds all edges. Memories are detached and relinked by name and file.
-- When upgrading from a version without 007, both run before that one index.
DELETE FROM dependencies;
UPDATE files SET content_hash = '';
