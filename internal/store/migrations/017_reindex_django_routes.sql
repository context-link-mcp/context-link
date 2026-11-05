-- Python files now also yield Django routes (path/re_path/url in
-- urlpatterns) and DRF router routes, and Django/DRF route handlers can link
-- to classes (class-based views, viewsets). Blank the hashes of Python files,
-- as 015 did for Go files, so the next normal index re-indexes them and
-- extracts their routes. The files table has no language column; .py is the
-- Python adapter's only extension. No row is deleted here.
UPDATE files SET content_hash = '' WHERE path LIKE '%.py';
