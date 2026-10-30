# Changelog

All notable changes to context-link. Dates are release-tag dates. Versions before 1.0.0 were reconstructed from git history.

## [Unreleased]

### Changed
- **Breaking:** `extract` output is now `schema_version: 2`. `files[].symbols` is a list of objects instead of names: `name`, `qualified_name`, `kind`, `start_line`, `end_line`, `offset` (byte offset in `content`), `role` (`match`, `dependency` or `outline`) and `dependencies` (resolved direct callees with file and lines).

### Added
- `extract` packs the code of each match's direct dependencies after the matches and before skeletons, within the budget; dependencies that did not fit stay listed as references and are counted in a warning. `--dependency-depth 0` lists dependencies without packing their code.
- `store.GetDirectDependencies`: batch caller → direct callees lookup scoped by `repo_name`.

### Fixed
- `testdata/extract` fixtures are checked out with LF line endings (`.gitattributes`), so `budget.used_bytes` in the golden files no longer depends on `core.autocrlf`.
- Dependency edges were all attributed to the first symbol in the caller's file (often a module-level variable such as `logger` or `router`). Each call now belongs to the innermost symbol whose lines contain it, and calls in module-level statements produce no edge. On paranoid: 2,098 → 5,896 edges, 271 → 2,695 distinct callers. Affects `get_code_by_symbol`, `get_call_tree`, `get_symbol_usages`, `get_blast_radius`, `find_dead_code` and search enrichment. Migration `007_reresolve_dependencies.sql` drops the old edges, so the first index after upgrading is a full re-index; memories are relinked to their symbols.

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
