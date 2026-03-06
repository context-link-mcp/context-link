# context-link — Architecture

## Overview

context-link is a local MCP (Model Context Protocol) server that acts as a structural intermediary between AI coding agents and codebases. Instead of agents reading entire files, context-link serves only the relevant code symbols, dependency graphs, and historical notes needed for a given task.

## Context Funnel Design

The system implements a three-stage pipeline that progressively narrows the information delivered to the LLM:

### Stage 1 — Semantic Scout (Discovery) [Implemented — Phase 3]

Accepts natural-language queries from the AI agent. Uses local vector embeddings (`all-MiniLM-L6-v2` via OnnxRuntime) stored as BLOBs in the `vec_symbols` table. Go-side KNN search (dot-product over L2-normalized float32 vectors) returns matching symbol names **without reading file contents**, dramatically narrowing the search space.

**Key tool:** `semantic_search_symbols`

### Stage 2 — Structural Surgeon (Extraction) [Implemented]

Given a symbol name, Tree-sitter parses the AST to extract the **exact function or class body**, its transitive call-graph dependencies (BFS up to depth 3), and required import statements from the SQLite dependency graph. No unnecessary code is returned.

**Key tool:** `get_code_by_symbol`

### Stage 3 — The Historian (Persistence) [Phase 4]

Injects developer-written or agent-written memory notes linked to specific AST nodes. Memories are automatically flagged stale when the underlying code hash changes during re-indexing. This enables persistent cross-session knowledge accumulation.

**Key tools:** `save_symbol_memory`, `get_symbol_memories`

## Component Map

```
cmd/context-link/               # CLI entry point (cobra): serve, index, version
internal/
  server/
    server.go                   # MCP server setup, tool registration, stdio transport
  indexer/
    language.go                 # LanguageAdapter interface + LanguageRegistry (RWMutex)
    walker.go                   # File walker (.gitignore, SHA-256 hashing, 1MB limit)
    parser.go                   # ParserPool (sync.Pool) + ParserPoolManager (lazy, per-grammar)
    extractor.go                # Query-based symbol + dependency extraction with dedup
    indexer.go                  # Pipeline orchestrator: walk → filter → parse → extract → store → resolve
    snapshot_test.go            # Golden JSON snapshot tests (-update-golden flag)
    crosslang_test.go           # Cross-language schema consistency + dependency edge tests
    adapters/
      typescript.go             # .ts adapter (smacker/go-tree-sitter/typescript)
      tsx.go                    # .tsx/.jsx adapter (smacker/go-tree-sitter/tsx)
      go.go                     # .go adapter (smacker/go-tree-sitter/golang)
      queries/
        ts_symbols.scm          # TS symbol extraction (functions, classes, methods, interfaces, types)
        ts_deps.scm             # TS dependency extraction (imports, calls, extends, implements)
        go_symbols.scm          # Go symbol extraction (funcs, methods, structs, interfaces, types, vars, consts)
        go_deps.scm             # Go dependency extraction (imports, calls)
  store/
    db.go                       # SQLite connection (WAL mode, 0600 perms, single-writer pool)
    migrate.go                  # Forward-only embedded migration runner
    files.go                    # File registry CRUD (upsert, get, list, delete)
    symbols.go                  # Symbol CRUD (batch insert, search, fuzzy match, BFS transitive deps)
    dependencies.go             # Dependency edge CRUD (insert, batch, reverse lookup, file-scoped delete)
    migrations/
      001_initial.sql           # Schema: files, symbols, dependencies, memories, indexes
      002_vec_symbols.sql       # Schema: vec_symbols (float32 BLOB embeddings for KNN search)
  vectorstore/                  # Phase 3: embedding generation + vector search
    embedder.go                 # Embedder interface, MockEmbedder, SymbolEmbedText helper
    tokenizer.go                # BERT WordPiece tokenizer (lowercase, subword, padding)
    onnx.go                     # ONNXEmbedder (lazy ORT session, pre-alloc tensors, mean-pool + L2-norm)
    vecstore.go                 # UpsertEmbedding, KNNSearch (Go-side dot-product), encode/decode BLOBs
  tools/
    ping.go                     # Health-check tool (status, version, uptime, runtime info)
    architecture.go             # read_architecture_rules tool (parses ARCHITECTURE.md → JSON sections)
    get_code.go                 # get_code_by_symbol tool (symbol lookup + transitive deps, depth 0-3)
    semantic_search.go          # semantic_search_symbols tool (embed query → KNN → join + filter symbols)
  config/
    config.go                   # Viper-based config loading (.context-link.yaml, env vars, model paths)
pkg/models/
  models.go                     # Shared types: Symbol, Memory, File, Dependency, ToolMetadata
testdata/
  langs/ts/auth.ts              # TypeScript fixture (class, interface, type, methods, arrow fns)
  langs/tsx/Button.tsx           # TSX fixture (React component, interface, class)
  langs/go/sample.go            # Go fixture (struct, interface, methods, functions, const, var)
  golden/                       # Snapshot golden JSON files for regression testing
```

## Data Flow

```
Agent Query
    |
    v
semantic_search_symbols ──> embed query (ONNX) ──> Go KNN over vec_symbols ──> [symbol names]
    |
    v
get_code_by_symbol ──> SQLite BFS dependency graph ──> [code + deps]
    |
    v
Agent Task Completion
    |
    v
save_symbol_memory ──> memories table ──> persisted across sessions  (Phase 4)
```

## Database Schema

The unified SQLite database (`.context-link.db`) contains:

- **files** — File registry with content hashes for incremental indexing
- **symbols** — Every parsed code symbol with source location, code block, and content hash
- **dependencies** — Directed call/extends/implements graph between symbols
- **memories** — Agent/developer notes linked to symbols (Phase 4, schema exists)
- **vec_symbols** — 384-dimensional L2-normalized float32 embeddings as BLOBs; KNN search performed in Go via dot-product

Key design decisions:
- WAL mode for concurrent read performance
- Single-writer connection pool to avoid `SQLITE_BUSY`
- `ON DELETE SET NULL` for memories (orphaned, not cascaded)
- All queries scoped by `repo_name` for multi-repo namespacing

## Indexing Pipeline

The indexer runs a six-phase pipeline via `errgroup` bounded worker pools:

```
1. Walk      →  Discover source files (respects .gitignore, skips >1MB)
2. Filter    →  Compare SHA-256 hashes against files table (incremental)
3. Parse     →  Tree-sitter AST parsing via adapter-specific grammar pools
4. Extract   →  Run .scm queries to capture symbols + dependencies
5. Store     →  Batch insert into SQLite (single-writer, transactions)
6. Resolve   →  BFS dependency resolution against symbol name index
7. Embed     →  Generate ONNX embeddings + upsert into vec_symbols (skipped if no model configured)
```

### Language-Agnostic Design

All parsing is driven by the `LanguageAdapter` interface. Adding a new language requires only:

1. A Tree-sitter grammar import
2. Two `.scm` query files (symbols + dependencies)
3. An adapter struct satisfying the interface
4. Registration in `buildLanguageRegistry()`

The core pipeline (walker, parser pool, extractor, store) is fully generic.

### Dependency Resolution

Dependencies are resolved via BFS traversal with configurable max depth (0-3):
- **Depth 0:** Symbol only, no dependencies
- **Depth 1:** Direct dependencies (1-hop callees)
- **Depth 2-3:** Transitive dependencies with deduplication

The resolver matches extracted call/extends/implements references against the symbol name index in the database.

## Design Principles

1. **Zero external network calls** — Fully air-gapped operation
2. **Single binary** — All dependencies compiled in (requires CGo for Tree-sitter)
3. **Incremental indexing** — Only re-parse changed files using SHA-256 hashing
4. **Language-agnostic** — `LanguageAdapter` registry pattern; new languages via query files
5. **Memory durability** — Orphaned memories survive symbol renames via `ON DELETE SET NULL`
6. **MCP-first** — All capabilities exposed as structured MCP tools with JSON responses and timing metadata
7. **Symbol deduplication** — Overlapping Tree-sitter query patterns are deduplicated by (qualified_name, start_line)

## Performance Targets

| Metric | Target | Current |
|--------|--------|---------|
| Token reduction vs. blind file read | >80% | Not yet measured (Phase 5) |
| Indexing throughput | >1,000 files/sec | ~23 files/sec (34 files/1.5s, includes dep resolution) |
| Semantic search latency (P95) | <200ms | Go-side KNN; not yet benchmarked at scale |
| Cold start to first tool call | <3 seconds | ~1s (MCP handshake, lazy ONNX init on first query) |
| Binary size (incl. ONNX model) | <50 MB | ~31 MB (ONNX model is a runtime dependency, not embedded) |
