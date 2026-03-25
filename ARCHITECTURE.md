# context-link — Architecture

## Overview

context-link is a local MCP (Model Context Protocol) server that acts as a structural intermediary between AI coding agents and codebases. Instead of agents reading entire files, context-link serves only the relevant code symbols, dependency graphs, and historical notes needed for a given task.

## Context Funnel Design

The system implements a three-stage pipeline that progressively narrows the information delivered to the LLM:

### Stage 1 — Semantic Scout (Discovery) [Implemented — Phase 3]

Accepts natural-language queries from the AI agent. Uses local vector embeddings stored as BLOBs in the `vec_symbols` table. The default embedder is `potion-base-4M` (Model2Vec, 128-dim, embedded in the binary — zero-config). An optional ONNX override (`all-MiniLM-L6-v2`, 384-dim) is available via `--model-path`. Go-side KNN search (dot-product over L2-normalized float32 vectors) returns matching symbol names **without reading file contents**, dramatically narrowing the search space.

**Key tool:** `semantic_search_symbols`

### Stage 2 — Structural Surgeon (Extraction) [Implemented]

Given a symbol name, Tree-sitter parses the AST to extract the **exact function or class body**, its transitive call-graph dependencies (BFS up to depth 3), and required import statements from the SQLite dependency graph. No unnecessary code is returned.

**Key tool:** `get_code_by_symbol`

### Stage 3 — The Historian (Persistence) [Implemented — Phase 4]

Injects developer-written or agent-written memory notes linked to specific AST nodes. Memories are automatically flagged stale when the underlying code hash changes during re-indexing, and orphaned memories (symbol deleted) are recovered automatically when a matching symbol reappears. This enables persistent cross-session knowledge accumulation.

**Key tools:** `save_symbol_memory`, `get_symbol_memories`, `purge_stale_memories`

## Component Map

```
cmd/context-link/               # CLI entry point (cobra): serve, index, version
internal/
  server/
    server.go                   # MCP server setup, config-driven tool registry, stdio transport
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
      python.go                 # .py adapter (smacker/go-tree-sitter/python)
      javascript.go             # .js/.mjs adapter (smacker/go-tree-sitter/javascript)
      rust.go                   # .rs adapter (smacker/go-tree-sitter/rust)
      java.go                   # .java adapter (smacker/go-tree-sitter/java)
      c_lang.go                 # .c/.h adapter (smacker/go-tree-sitter/c)
      cpp.go                    # .cpp/.hpp/.cc/.cxx/.hxx/.hh adapter (smacker/go-tree-sitter/cpp)
      csharp.go                 # .cs adapter (smacker/go-tree-sitter/csharp)
      queries/
        ts_symbols.scm          # TS symbol extraction (functions, classes, methods, interfaces, types)
        ts_deps.scm             # TS dependency extraction (imports, calls, extends, implements)
        go_symbols.scm          # Go symbol extraction (funcs, methods, structs, interfaces, types, vars, consts)
        go_deps.scm             # Go dependency extraction (imports, calls)
        python_symbols.scm      # Python symbol extraction (functions, classes, assignments)
        python_deps.scm         # Python dependency extraction (imports, calls)
        js_symbols.scm          # JavaScript symbol extraction (functions, classes, methods, variables)
        js_deps.scm             # JavaScript dependency extraction (imports, calls)
        rust_symbols.scm        # Rust symbol extraction (functions, structs, enums, traits, impls)
        rust_deps.scm           # Rust dependency extraction (use statements, calls)
        java_symbols.scm        # Java symbol extraction (classes, interfaces, methods, enums)
        java_deps.scm           # Java dependency extraction (imports, calls)
        c_symbols.scm           # C symbol extraction (functions, structs, enums, typedefs)
        c_deps.scm              # C dependency extraction (includes, calls)
        cpp_symbols.scm         # C++ symbol extraction (classes, functions, templates, structs, enums)
        cpp_deps.scm            # C++ dependency extraction (includes, calls)
        csharp_symbols.scm      # C# symbol extraction (classes, interfaces, methods, structs, enums)
        csharp_deps.scm         # C# dependency extraction (using, calls)
  store/
    db.go                       # SQLite connection (WAL mode, 0600 perms, single-writer pool)
    migrate.go                  # Forward-only embedded migration runner
    files.go                    # File registry CRUD (upsert, get, list, delete)
    symbols.go                  # Symbol CRUD (batch insert, search, fuzzy match, BFS transitive deps, dead code detection)
    dependencies.go             # Dependency edge CRUD (insert, batch, reverse lookup, file-scoped delete)
    routes.go                   # Route CRUD (batch insert, filtering, path normalization, confidence matching)
    migrations/
      001_initial.sql           # Schema: files, symbols, dependencies, memories, indexes
      002_vec_symbols.sql       # Schema: vec_symbols (float32 BLOB embeddings for KNN search)
      003_vec_dimension_meta.sql # Schema: vec_meta (embedding dimension tracking)
      005_routes.sql            # Schema: routes (HTTP route definitions and call sites)
  vectorstore/                  # Phase 3: embedding generation + vector search
    embedder.go                 # Embedder interface, MockEmbedder, SymbolEmbedText helper
    tokenizer.go                # BERT WordPiece tokenizer (lowercase, subword, padding, dynamic special IDs)
    hf_tokenizer.go             # HuggingFace tokenizer.json parser (WordPiece subset)
    model2vec.go                # Model2VecEmbedder (built-in, zero-config, 128-dim potion-base-4M)
    model2vec_data.go           # //go:embed directives for model2vec/ model files
    safetensors.go              # Pure Go safetensors parser (F32/F16 tensor extraction)
    onnx.go                     # ONNXEmbedder (optional, lazy ORT session, 384-dim all-MiniLM-L6-v2)
    vecstore.go                 # UpsertEmbedding, KNNSearch (Go-side dot-product), dimension validation
  tools/
    ping.go                     # Health-check tool (status, version, uptime, runtime info)
    architecture.go             # read_architecture_rules tool (parses ARCHITECTURE.md → JSON sections)
    get_code.go                 # get_code_by_symbol tool (symbol lookup + transitive deps + memories, depth 0-3)
    semantic_search.go          # semantic_search_symbols tool (embed query → KNN → join + filter + memory_count)
    memory.go                   # save_symbol_memory, get_symbol_memories, purge_stale_memories tools
    skeleton.go                 # get_file_skeleton tool (structural outline, signatures only)
    usages.go                   # get_symbol_usages tool (reverse dependency lookup — who calls this?)
    calltree.go                 # get_call_tree tool (BFS call hierarchy, callees or callers, depth 1-3)
    deadcode.go                 # find_dead_code tool (symbols with zero inbound dependency edges)
    blastradius.go              # get_blast_radius tool (BFS callers, file grouping, depth summary)
    routes.go                   # find_http_routes tool (route discovery + definition/call-site matching)
    tokens.go                   # Token savings estimation, SessionTokenTracker (atomic int64 accumulator)
    timeout.go                  # WithTimeout middleware for tool handlers
  config/
    config.go                   # Viper-based config loading (.context-link.yaml, env vars, model paths)
  watcher/
    watcher.go                  # fsnotify file watcher (500ms debounce, incremental re-index on changes)
pkg/models/
  models.go                     # Shared types: Symbol, Memory, File, Dependency, Route, ToolMetadata
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
semantic_search_symbols ──> embed query (Model2Vec/ONNX) ──> Go KNN over vec_symbols ──> [symbol names]
    |
    ├──> get_file_skeleton ──> structural outline (signatures only, no code bodies)
    |
    v
get_code_by_symbol ──> SQLite BFS dependency graph ──> [code + deps]
    |
    ├──> get_symbol_usages ──> reverse dependency lookup ──> [all callers]
    ├──> get_call_tree ──> BFS call hierarchy (callees/callers, depth 1-3)
    ├──> find_dead_code ──> symbols with zero inbound edges ──> [unused symbols]
    ├──> get_blast_radius ──> BFS callers grouped by file/depth ──> [change impact]
    ├──> find_http_routes ──> route definitions + call-site matching ──> [routes + matches]
    |
    v
Agent Task Completion
    |
    v
save_symbol_memory ──> memories table ──> persisted across sessions
    |
    v
re-index event ──> SnapshotAndMarkStale ──> orphan recovery (RelinkMemory)
```

## Database Schema

The unified SQLite database (`.context-link.db`) contains:

- **files** — File registry with content hashes for incremental indexing
- **symbols** — Every parsed code symbol with source location, code block, and content hash
- **dependencies** — Directed call/extends/implements graph between symbols
- **memories** — Agent/developer notes linked to symbols; stale-flagged on hash change, orphaned on deletion, recovered on re-index
- **vec_symbols** — L2-normalized float32 embeddings as BLOBs (128-dim Model2Vec default, 384-dim ONNX optional); KNN search performed in Go via dot-product
- **vec_meta** — Key-value metadata (tracks embedding dimension for mismatch detection on embedder switch)
- **routes** — HTTP route definitions and call sites with method, path pattern, normalized path (for cross-framework matching), handler symbol reference, framework, and kind (definition/call_site)

Key design decisions:
- WAL mode for concurrent read performance
- Single-writer connection pool to avoid `SQLITE_BUSY`
- `ON DELETE SET NULL` for memories (orphaned, not cascaded)
- All queries scoped by `repo_name` for multi-repo namespacing

## Indexing Pipeline

The indexer runs a six-phase pipeline via `errgroup` bounded worker pools:

```
1. Walk      →  Discover source files (respects .gitignore, skips >1MB)
2. Filter    →  Compare SHA-256 hashes against files table (incremental); detect + remove deleted files;
                snapshot memories as stale before overwriting changed symbols; --force bypasses hash check
3. Parse     →  Tree-sitter AST parsing via adapter-specific grammar pools
4. Extract   →  Run .scm queries to capture symbols + dependencies
5. Store     →  Batch insert into SQLite (single-writer, transactions)
6. Resolve   →  BFS dependency resolution against symbol name index
7. Embed     →  Generate embeddings (built-in Model2Vec or ONNX) + upsert into vec_symbols
8. Recover   →  Orphan recovery: re-link orphaned memories to newly indexed symbols by (qualified_name, file_path)
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

## Tool Registry

The server uses a config-driven tool registry (`server.go:registerTools`). Each tool is a `toolRegistration{name, register}` pair. If `config.Tools` is non-empty, only listed tools are registered — this controls prompt token budget by hiding unused tools from agents. The memory group (`save_symbol_memory`, `get_symbol_memories`, `purge_stale_memories`) is registered as a block via the `memory` key.

All tool responses include token savings metadata (`tokens_saved_est`, `cost_avoided_est`, `session_tokens_saved`, `session_cost_avoided`) via a shared `SessionTokenTracker`. Heuristic: 1 token ≈ 4 bytes, $3/MTok Sonnet pricing.

Version is injected at build time via `-X main.version=<tag>` ldflags, passed through `server.New()` to the ping tool.

### File Watcher

The `--watch` flag on the `serve` command starts an fsnotify-based file watcher alongside the MCP server. It recursively watches the project directory, debounces changes at 500ms, then triggers a full incremental `IndexRepo()` (already fast due to content-hash skipping). Directories like `node_modules`, `.git`, `vendor`, `__pycache__`, and `target` are excluded.

## Design Principles

1. **Zero external network calls** — Fully air-gapped operation
2. **Single binary** — All dependencies compiled in (requires CGo for Tree-sitter)
3. **Incremental indexing** — Only re-parse changed files using SHA-256 hashing
4. **Language-agnostic** — `LanguageAdapter` registry pattern; new languages via query files
5. **Memory durability** — Orphaned memories survive symbol renames via `ON DELETE SET NULL`
6. **MCP-first** — All capabilities exposed as structured MCP tools with JSON responses and timing metadata
7. **Symbol deduplication** — Overlapping Tree-sitter query patterns are deduplicated by (qualified_name, start_line)
8. **Config-driven tool registry** — Selectively enable/disable tools via config to control agent prompt token budget

## Performance Targets

| Metric | Target | Current |
|--------|--------|---------|
| Token reduction vs. blind file read | >80% | ~78% avg (95.9% best, depth=1) |
| Indexing throughput | >1,000 files/sec | ~12 files/sec (59 files/5.1s, includes dep resolution + embedding) |
| Semantic search latency (P95) | <200ms | 23ms P95, 21ms P50 (530 symbols, Model2Vec) |
| Cold start to first tool call | <3 seconds | 44ms (MCP init + ping) |
| Binary size (incl. embedded model) | <50 MB | ~47 MB (Model2Vec potion-base-4M embedded in binary) |

### Optimization Impact

**Round 1** — Eliminated redundant work in the indexing pipeline:

| Optimization | Change | Impact |
|--------------|--------|--------|
| Eliminate double-parsing | Removed full re-parse pass during dependency resolution | ~2x faster indexing |
| Batch embedding upserts | Transaction + prepared statement instead of per-row inserts | ~5-10x faster embedding storage |
| Batch dependency inserts | Used existing `BatchInsertDependencies` | Fewer DB round-trips |
| `vec_symbols` repo index | Added `idx_vec_symbols_repo` | Faster KNN scans on multi-repo DBs |

**Round 2** — Architecture, function, and code-level optimizations:

| Optimization | Change | Impact |
|--------------|--------|--------|
| In-memory vector cache (O1) | Pre-loads embeddings on first query, pure dot-product KNN | Search: 1,880µs → 197µs (10x) |
| Batch file hash lookup (O2) | Single `GetFileHashIndex` query replaces N per-file lookups | Incremental check: near-instant |
| Pre-computed symbol map (O3) | One `GetSymbolsByRepo` call replaces 3×N per-file queries | Eliminated ~3N DB round-trips |
| Batch semantic search lookup (O4) | `GetSymbolsByIDs` replaces per-hit query loop | 30 queries → 1 query |
| Batch BFS dependencies (O5) | `WHERE caller_id IN (...)` replaces per-node queries | N queries → 1 per depth level |
| Parallel file hashing (O6) | `errgroup` with 8 goroutines for walker I/O | 2-4x faster walk on large repos |
| Batch orphan relinking (O7) | `GetAllOrphanedMemories` replaces per-symbol queries | N queries → 1 query |
| SQLite PRAGMA tuning (O8) | `synchronous=NORMAL`, 8 MB cache, memory temp store | 10-20% faster writes |
| Heap-based top-k (O10) | `container/heap` replaces `sort.Slice` for KNN | O(n log k) vs O(n log n) |

**Cumulative: 4.9x indexing speedup** (5.1s → 1.05s), **150x search speedup** (30ms → 0.2ms), **9ms incremental re-index** (no changes).