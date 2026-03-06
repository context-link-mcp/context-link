# CLAUDE.md — context-link Agent Instructions

This file is read automatically by Claude Code at the start of every session.

## Project Overview

context-link is a local MCP server (Go) that serves structured code context to AI agents. It indexes codebases with Tree-sitter, builds a symbol + dependency graph in SQLite, and exposes tools over MCP/stdio.

## Key Files to Read First

| File | Purpose |
|------|---------|
| `ARCHITECTURE.md` | System design, component map, data flow, design principles |
| `BUILDING.md` | CGo dependencies, platform-specific build instructions |
| `.claude/plans/task.md` | Phase-by-phase task checklist with validation notes |
| `.claude/plans/Context.md` | Full implementation plan (problem, architecture, metrics) |
| `.claude/rules/coding-standards.md` | Non-negotiable coding conventions |
| `internal/store/migrations/001_initial.sql` | Database schema (source of truth for tables/indexes) |
| `pkg/models/models.go` | Shared types: Symbol, Memory, File, Dependency |

## Build & Test

```bash
# Build (CGo required for Tree-sitter)
CGO_ENABLED=1 go build -o ./bin/context-link.exe ./cmd/context-link

# Run all tests
CGO_ENABLED=1 go test ./... -count=1

# Index this repo
./bin/context-link.exe index --project-root .

# Start MCP server
./bin/context-link.exe serve --project-root .

# Update snapshot golden files after changing .scm queries or extractors
CGO_ENABLED=1 go test ./internal/indexer/ -args -update-golden
```

## Coding Standards (Summary)

These are enforced — see `.claude/rules/coding-standards.md` for full details.

### Error Handling
- Return errors as last value: `func Foo() (Result, error)`
- Wrap with context: `fmt.Errorf("pkg: failed to do X: %w", err)`
- Use `errors.Is()`/`errors.As()`, never string comparison
- Define sentinels: `var ErrNotFound = errors.New("not found")`
- Never `panic()` in library code

### Concurrency
- `context.Context` as first parameter on all cancellable functions
- `errgroup.Group` with `SetLimit` for bounded parallelism
- `sync.RWMutex` for read-heavy shared state
- Single SQLite writer (connection pool max=1)

### Database
- Parameterized queries only — no string interpolation in SQL
- All queries scoped by `repo_name` (multi-repo namespacing)
- Versioned migrations in `internal/store/migrations/`
- Transactions for multi-statement writes
- `ON DELETE SET NULL` for memories (not CASCADE)

### MCP Tools
- Structured JSON responses, never plain text
- Input validation before processing
- `metadata.timing_ms` field on every response
- Standard MCP error codes

### Testing
- 80% coverage target for `internal/` packages
- Table-driven tests for pure functions
- `t.Parallel()` on all tests without shared mutable state
- `testify/assert` for assertions, `testify/require` for fatal preconditions
- Snapshot tests with golden JSON in `testdata/golden/`
- Integration tests use fixtures in `testdata/langs/`

### Tree-sitter
- Separate grammars: TS and TSX are distinct C bindings
- One parser pool per grammar (reuse via `sync.Pool`)
- `.scm` query files embedded via `//go:embed`, relative to adapter package
- Symbol deduplication by (qualified_name, start_line) in extractor

### Style
- Follow Effective Go
- GoDoc comments on all exported symbols
- No `init()` functions — explicit initialization
- Minimize external dependencies, prefer stdlib

## Architecture Quick Reference

```
cmd/context-link/main.go          Entry point, CLI (cobra), adapter registration
internal/server/server.go         MCP server setup, tool registration
internal/indexer/indexer.go        Pipeline orchestrator: walk → parse → extract → store → resolve
internal/indexer/language.go       LanguageAdapter interface + registry
internal/indexer/extractor.go      Tree-sitter query-based symbol/dep extraction
internal/indexer/adapters/         TS, TSX, Go adapters + .scm query files
internal/store/symbols.go          Symbol CRUD + BFS transitive dependency resolution
internal/store/dependencies.go     Dependency edge CRUD
internal/tools/get_code.go         get_code_by_symbol MCP tool handler
internal/tools/ping.go             Health-check tool
internal/tools/architecture.go     read_architecture_rules tool
```

## Adding a New Language

1. Import grammar: `smacker/go-tree-sitter/<lang>`
2. Write `queries/<lang>_symbols.scm` and `queries/<lang>_deps.scm`
3. Create adapter in `internal/indexer/adapters/<lang>.go` implementing `LanguageAdapter`
4. Register in `buildLanguageRegistry()` in `cmd/context-link/main.go`
5. Add fixtures in `testdata/langs/<lang>/` and update golden snapshots

## Current Phase Status

- **Phase 1 (Foundation):** Complete — CLI, SQLite, MCP server, ping, architecture tool
- **Phase 2 (Indexer):** Complete — Tree-sitter parsing, symbol graph, BFS deps, get_code_by_symbol, 3 language adapters, 65 tests passing
- **Phase 3 (Semantic Search):** Next — ONNX embeddings, sqlite-vec, semantic_search_symbols tool
- **Phase 4 (Memory):** Planned — save/get memories, stale detection, orphan recovery
- **Phase 5 (Polish):** Planned — performance optimization, integration testing, documentation
