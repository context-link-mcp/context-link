-- C++ methods defined outside their class (int Foo::bar() {}) now get the
-- class prefix from the qualified name's scope (Foo.bar, was bar), member
-- functions defined in the class body are symbols, and Foo::bar() call sites
-- are stored as Foo.bar. Those names only reach a file when it is indexed
-- again, so blank the hash of every C and C++ file: the ones whose symbols
-- are C/C++, plus any file with a C/C++ extension (a file without symbols
-- still has call sites). Other languages keep their hashes and are not
-- re-indexed. No row is deleted here: each file's re-index replaces its own
-- symbols and edges, and memories are detached and relinked by qualified name
-- and file on the way, as for 012. A memory on a method whose name changed
-- (bar -> Foo.bar) has no symbol of its old name to relink to and stays an
-- orphan (kept, not deleted).
UPDATE files SET content_hash = ''
WHERE EXISTS (
        SELECT 1 FROM symbols s
        WHERE s.repo_name = files.repo_name
          AND s.file_path = files.path
          AND s.language IN ('c', 'cpp')
    )
    OR path LIKE '%.c'
    OR path LIKE '%.h'
    OR path LIKE '%.cpp'
    OR path LIKE '%.hpp'
    OR path LIKE '%.cc'
    OR path LIKE '%.cxx'
    OR path LIKE '%.hxx'
    OR path LIKE '%.hh';
