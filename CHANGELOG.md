# Changelog

All notable changes to context-link. Dates are release-tag dates. Versions before 1.0.0 were reconstructed from git history.

## [Unreleased]

### Added
- `extract --dependency-reserve` (default `25`): percent of the budget kept for the code of the matches' direct dependencies before lower-ranked matches are packed. What the dependencies leave unused goes back to the remaining matches, then skeletons. `0` restores the 1.3.0 packing. Library: `Options.DependencyReservePercent` (0 disables, clamped to 90).

### Fixed
- `extract` packed no dependency code on large repos: the matches filled the whole budget before the dependency pass ran. On paranoid (`--profile threat-model`, 50 KB) the default reserve packs 25 dependency symbols (was 0) and leaves 59 dependencies as references only (was 102); 41 matched symbols are packed instead of 46.

## [1.3.0] - 2026-11-03

### Changed
- **Breaking:** `extract` output is now `schema_version: 2`. `files[].symbols` is a list of objects instead of names: `name`, `qualified_name`, `kind`, `start_line`, `end_line`, `offset` (byte offset in `content`), `role` (`match`, `dependency` or `outline`) and `dependencies` (resolved direct callees with file and lines).

### Added
- `serve --repo-name`, with the same meaning as on `index`.
- `.gitignore` support for `!` negation (last match wins), anchored `/pattern` and `**`.
- Migration `008_memories_repo_name.sql`: memories record their repo.
- `extract` packs the code of each match's direct dependencies after the matches and before skeletons, within the budget; dependencies that did not fit stay listed as references and are counted in a warning. `--dependency-depth 0` lists dependencies without packing their code.
- `store.GetDirectDependencies`: batch caller → direct callees lookup scoped by `repo_name`.

### Fixed
- `testdata/extract` fixtures are checked out with LF line endings (`.gitattributes`), so `budget.used_bytes` in the golden files no longer depends on `core.autocrlf`.
- Dependency edges were all attributed to the first symbol in the caller's file (often a module-level variable such as `logger` or `router`). Each call now belongs to the innermost symbol whose lines contain it, and calls in module-level statements produce no edge. On paranoid: 2,098 → 5,896 edges, 271 → 2,695 distinct callers. Affects `get_code_by_symbol`, `get_call_tree`, `get_symbol_usages`, `get_blast_radius`, `find_dead_code` and search enrichment. Migration `007_reresolve_dependencies.sql` drops the old edges, so the first index after upgrading is a full re-index; memories are relinked to their symbols.
- **Security:** `get_modified_symbols` passed `base_ref` straight to `git diff`, so a value such as `--output=<path>` could write any file. Refs starting with `-` are now rejected, and refs are resolved with `git rev-parse --verify --end-of-options` before use.
- `get_modified_symbols` returned nothing when the project root was a subfolder of the git repo and ran no diff for `include_staged=false` with `HEAD`. It now reports untracked files as added, and renamed and non-ASCII paths by their new path.
- Method and selector calls (`s.Load()`, `pkg.Func()`, `self.helper()`, `this.helper()`) now produce call edges in Go, Python, JavaScript and TypeScript; before, every cross-package Go call was missing. On context-link: 1,331 → 2,116 edges; on paranoid: 5,896 → 8,241. Callees are still matched by bare name, so common method names (`get`, `Len`, `Open`) can link to the wrong symbol.
- Editing or deleting a file left edges from unchanged callers pointing at deleted symbols. They are now linked again by name after an incremental index, or left unresolved if the callee is gone. Migration `009_reresolve_method_call_edges.sql` makes the first index after upgrading a full re-index.
- Callees were matched by bare name across the whole repo and every language, and the last symbol with the name won, so common names linked to arbitrary symbols: every Python `dict.get()` to `ConnectionManager.get`, Go `x.Len()` to `candidateHeap.Len`, `os.Open`/`sql.Open` to `store.Open`, Python calls to Go functions. Calls now resolve within the same language (JS/TS/TSX and C/C++ count as one) in widening scopes: the caller's file, its directory, the files it imports, and the rest of the repo only for a name defined once (never for Go). Several candidates in a scope give no edge unless exactly one is in the caller's own class. A receiver bound by an import (Go package, Python `import a.b` or `from m import x`, JS/TS default or namespace import) resolves only in the imported repo files, and not at all for an external package; `self`/`this` prefer the caller's class; any other receiver stays in the caller's file (Go: also its package and methods of imported packages). Python relative imports are now extracted. On context-link: 2,468 → 2,104 edges, 9 → 0 cross-language; on paranoid: 8,468 → 7,396 edges, 123 → 0 cross-language, `ConnectionManager.get` 688 → 148 inbound (the remaining ones are calls on its `db` instance). Some dynamic method calls on objects of unknown type (`profile.category_set()` from another module) no longer get an edge.
- An index run interrupted or failed between replacing a changed file and resolving edges lost the edges from unchanged callers into that file for good: the changed file was retried, the callers weren't. Files whose edges must be resolved again are now recorded in the database (`pending_resolution`) in the same transaction that drops their edges, and every run resolves all recorded files. Each file's raw calls and imports are kept (`call_sites`) so this needs no re-parse. A call left unresolved because its callee was missing now gets its edge when a file adding that name is indexed, and a call that becomes ambiguous loses it. Migration `010_scoped_resolution.sql` makes the first index after upgrading a full re-index.
- If the repo's symbols failed to load after the store phase, dependency resolution ran against no symbols, replaced the edges of every file it resolved with nothing and cleared their re-resolution marks, so the edges were lost for good. The run now fails instead and is retried next time.
- Go standard library imports (`log`, `net/http`, `errors`) mapped to a repo directory with the same name, so `log.Printf` could link into a repo package `log`. Imports under the root `go.mod`'s module path now map to exactly the directory they name, and an import path whose first element has no dot is treated as the standard library.
- `find_dead_code` reported symbols as dead when their only callers were calls the resolver deliberately leaves unlinked (a method called on an object of unknown type from another module). A symbol is now alive if any call site in the repo names it, apart from calls inside its own body.
- Indexing found 0 files when the project folder was named `vendor`, `build` or `dist`, or when `.gitignore` contained `.*` or `*`. Ignore rules no longer apply to the root.
- An interrupted or failed index run could leave files with a current hash but no symbols, so later runs skipped them. Each file is now replaced in one transaction and hashes are committed only after the run finishes; an interrupted run returns an error and is retried next time.
- `serve --project-root .` queried repo `"."` and every tool returned nothing. `index`, `serve` and `extract` now resolve the project root and repo name the same way.
- Semantic search kept a stale vector cache after `reindex_project` or a `--watch` re-index, so new and changed code was missing from vector results. The cache is now refreshed after every re-index.
- `purge_stale_memories` deleted stale memories from every repo in a shared database, and orphaned memories could relink to another repo's symbol at the same path. Both are now scoped by repo. In a shared database, legacy orphaned memories whose repo can't be determined are kept but left out of purges, relinks and reads. `get_symbol_memories` now also returns the current repo's orphaned memories.
- SQLite settings never took effect: `store.Open` passed mattn-style DSN parameters (`_foreign_keys=on`, `_journal_mode=WAL`, `_busy_timeout=5000`, ...) that `modernc.org/sqlite` ignores, so foreign keys were off, WAL and the busy timeout were not set, and `ON DELETE CASCADE` / `SET NULL` never ran. Every setting now uses the driver's `_pragma=` form and a test checks them on a file database. Migration `011_drop_dangling_symbol_refs.sql` deletes dependency edges and embeddings that point at missing symbols, marks the callers that lose an edge for re-resolution, clears dangling route handlers, and turns memories linked to a missing symbol into stale orphans whose file is re-indexed next, so they relink by name and file.
- `index --force` never detached memories before replacing every symbol, so they kept pointing at deleted symbol IDs and were no longer found by symbol name. Forced files now go through the same snapshot, detach and relink path as changed files (`stale_reason` `reindexed` until relinked).

## [1.2.0] - 2026-10-17

### Added
- `extract` subcommand: one call indexes incrementally, ranks symbols against repeated `--query` flags, and prints one JSON document (`schema_version: 1`) with HTTP `entry_points`, budget-packed `files` (full code first, then signature-only skeletons), `index` health including `parse_failures`, and `warnings`. Tests, fixtures, vendored code and migrations are excluded unless `--include-tests`. `--profile threat-model` adds security-oriented queries and boosts entry-point handlers. Exit codes: 0 ok, 1 usage error, 2 index unusable.
- HTTP route extraction in the indexer for FastAPI and Flask (decorators, `methods=`, `APIRouter(prefix=)` / `Blueprint(url_prefix=)` in the same file) and Express-style registrations in JavaScript, TypeScript and TSX. Handlers are linked to their symbols. `find_http_routes` returned nothing before this because routes were never written to the index.
- `index` summary prints the number of HTTP routes; `IndexStats` reports the files that failed to index.

### Fixed
- Files were silently dropped during indexing ("operation limit was hit"): a pooled Tree-sitter parser could be reused after a stale cancellation flag was set. Each parse now uses a fresh parser. On a 364-file repo this removed 67–68 failed files (about 19% of symbols).
- Dependency `github.com/buger/jsonparser` 1.1.1 → 1.1.2 (CVE-2026-32285, denial of service).
- CI workflow now declares `permissions: contents: read`.

### Changed
- `HybridSearch` (vector KNN + FTS5, fused with RRF) is exported from `internal/tools` and shared by `semantic_search_symbols` and `extract`.

## [1.1.0] - 2026-04-26

### Added
- Kotlin adapter (`.kt`, `.kts`) and Swift adapter (`.swift`), with snapshot tests and golden fixtures.
- Comprehensive test coverage: seven previously untested MCP tools, watcher tests, store test refactor, and end-to-end CLI/MCP protocol tests.

## [1.0.0] - 2026-03-28

### Added
- `search_code_patterns` tool with FTS5 query sanitization.
- Batch query support with polymorphic parameters on the symbol and file tools.

## [0.4.0] - 2026-03-27

### Added
- `reindex_project`, `get_modified_symbols` (git-aware) and `get_tests_for_symbol` tools.
- FTS5 hybrid search (keyword + vector) with Reciprocal Rank Fusion, and enriched embedding input.

### Fixed
- Python qualified-name resolution and other search-quality issues.

## [0.3.0] - 2026-03-23

### Added
- Seven language adapters: Python, JavaScript, Rust, Java, C, C++, C#.
- `find_dead_code`, `get_blast_radius` and `find_http_routes` tools, a file watcher (`serve --watch`), and token-savings tracking on all tool responses.

## [0.2.0] - 2026-03-22

### Added
- Built-in Model2Vec embedder for zero-config semantic search, optional ONNX embedder.
- Memory persistence with stale detection and orphan recovery.
- `get_file_skeleton`, `get_symbol_usages` and `get_call_tree` tools; config-driven tool registry.
- Search performance work (about 150x faster search, in-memory vector cache).
- GoReleaser and CI/CD release tooling, Homebrew tap.

## Earlier (2026-03-06)

- Tree-sitter structural indexer with symbol graph, SQLite store, MCP server, `get_code_by_symbol`, and ONNX-based semantic search.
