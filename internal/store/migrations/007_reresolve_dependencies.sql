-- Dependency edges written before this version were all attached to the first
-- symbol of the caller's file. Drop them and blank every file hash, so the next
-- normal index sees every file as changed and rebuilds all edges. Memories go
-- through the usual changed-file path: detached, then relinked by name and file.
DELETE FROM dependencies;
UPDATE files SET content_hash = '';
