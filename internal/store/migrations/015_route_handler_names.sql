-- Route handlers can now be linked across files (Go handlers.List, a handler
-- in another file of the package). The link is a symbol ID, which the
-- handler file's re-index sets to NULL (ON DELETE SET NULL) without touching
-- the route's own file, so the handler's name and receiver are kept and every
-- index run relinks the routes left without a handler.
ALTER TABLE routes ADD COLUMN handler_name TEXT NOT NULL DEFAULT '';
ALTER TABLE routes ADD COLUMN handler_qualifier TEXT NOT NULL DEFAULT '';

-- Go files now yield routes (Gin, Echo, chi, net/http). Blank the hashes of
-- Go files, as 007/009/010/012/013 did for every file, so the next normal
-- index re-indexes them and extracts their routes. No row is deleted here.
UPDATE files SET content_hash = '' WHERE path LIKE '%.go';
