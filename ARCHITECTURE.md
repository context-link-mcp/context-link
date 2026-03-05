# context-link — Architecture

## Overview

context-link is a local MCP (Model Context Protocol) server that acts as a structural intermediary between AI coding agents and codebases. Instead of agents reading entire files, context-link serves only the relevant code symbols, dependency graphs, and historical notes needed for a given task.

## Context Funnel Design

The system implements a three-stage pipeline:

### Stage 1 — Semantic Scout (Discovery)

Accepts natural-language queries from the AI agent. Uses local vector embeddings (all-MiniLM-L6-v2 via ONNX Runtime) stored in sqlite-vec to return matching symbol names **without reading file contents**. This dramatically reduces the search space before any code is fetched.

**Key tool:** `semantic_search_symbols`

### Stage 2 — Structural Surgeon (Extraction)

Given a symbol name returned by the Scout, Tree-sitter parses the AST to extract the **exact function or class body**, its direct call-graph dependencies, and required import statements from the SQLite dependency graph. No unnecessary code is returned.

**Key tool:** `get_code_by_symbol`

### Stage 3 — The Historian (Persistence)

Injects developer-written or agent-written memory notes linked to specific AST nodes. Memories are automatically flagged stale when the underlying code hash changes during re-indexing. This enables persistent cross-session knowledge accumulation.

**Key tools:** `save_symbol_memory`, `get_symbol_memories`

## Component Map

```
cmd/context-link/       # CLI entry point (cobra)
internal/
  server/                   # MCP server, JSON-RPC handler (mcp-go)
  indexer/                  # Tree-sitter AST parsing, file walker
  store/                    # SQLite schema, migrations, CRUD
  vectorstore/              # sqlite-vec integration, ONNX embeddings
  memory/                   # Memory CRUD, stale detection
  tools/                    # MCP tool definitions and handlers
  config/                   # Configuration loading (viper)
pkg/models/                 # Shared types: Symbol, Memory, File, Dependency
```

## Data Flow

```
Agent Query
    │
    ▼
semantic_search_symbols ──► sqlite-vec KNN search ──► [symbol names]
    │
    ▼
get_code_by_symbol ──► SQLite dependency graph ──► [code + deps + memories]
    │
    ▼
Agent Task Completion
    │
    ▼
save_symbol_memory ──► memories table ──► persisted across sessions
```

## Database Schema

The unified SQLite database (`context-link.db`) contains:

- **symbols** — Every parsed code symbol with source location and content hash
- **dependencies** — Directed call graph between symbols
- **vec_symbols** — 384-dimensional embeddings for semantic search (Phase 3)
- **memories** — Agent/developer notes linked to symbols (Phase 4)
- **files** — File registry with content hashes for incremental indexing

## Design Principles

1. **Zero external network calls** — Fully air-gapped operation
2. **Single binary** — All dependencies compiled in (except CGo toolchain)
3. **Incremental indexing** — Only re-parse changed files using SHA-256 hashing
4. **Memory durability** — Orphaned memories (from renamed symbols) survive via `ON DELETE SET NULL`
5. **MCP-first** — All capabilities exposed as structured MCP tools with JSON responses

## Performance Targets

| Metric | Target |
|--------|--------|
| Token reduction vs. blind file read | >80% |
| Indexing throughput | >1,000 files/sec |
| Semantic search latency (P95) | <200ms |
| Cold start to first tool call | <3 seconds |
| Binary size (incl. ONNX model) | <50 MB |
