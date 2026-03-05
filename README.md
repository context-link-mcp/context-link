# context-link

**Local MCP Context Gateway for AI Coding Agents**

context-link is a local MCP server that serves structured code context to AI agents, dramatically reducing token consumption compared to reading entire files. It indexes codebases using a language-agnostic adapter system, builds a symbol graph, and provides semantic search via fully-local embeddings.

**Supported languages:** TypeScript, TSX, Go — with more addable via the `LanguageAdapter` interface.

## The Problem

AI coding agents read entire files to understand context. This brute-force approach is expensive, slow, and prone to context-window overflow. context-link acts as a structural intermediary that extracts and serves only the relevant code symbols, dependencies, and historical notes an agent needs.

## How It Works: The Context Funnel

context-link implements a three-stage pipeline that progressively narrows the information delivered to the LLM:

```
  Natural Language Query
         |
         v
  +-----------------------+
  | Stage 1: Semantic Scout |  Discovers matching symbol names via local
  | (Discovery)            |  vector embeddings — no file contents read
  +-----------------------+
         |
         v
  +-----------------------+
  | Stage 2: Structural    |  Extracts exact function/class body, call-graph
  | Surgeon (Extraction)   |  dependencies, and import statements via AST
  +-----------------------+
         |
         v
  +-----------------------+
  | Stage 3: The Historian |  Injects developer/agent memory notes linked
  | (Persistence)          |  to AST nodes, auto-flagged stale on changes
  +-----------------------+
         |
         v
  Minimal, precise context → LLM
```

## Target Metrics

| Metric | Target |
|--------|--------|
| Token reduction vs. blind file read | >80% fewer tokens |
| Indexing throughput | >1,000 files/sec |
| Semantic search latency (P95) | <200ms per query |
| Cold start to first tool call | <3 seconds |
| Binary size (incl. ONNX model) | <50 MB |

## MCP Tools

| Tool | Description | Funnel Stage |
|------|-------------|--------------|
| `ping` | Health-check for connectivity validation | Setup |
| `read_architecture_rules` | Returns parsed ARCHITECTURE.md sections | Setup |
| `semantic_search_symbols` | Natural-language symbol discovery (no code returned) | Scout |
| `get_code_by_symbol` | Extracts exact code, dependencies, and imports for a symbol | Surgeon |
| `save_symbol_memory` | Attaches persistent notes to symbols | Historian |
| `get_symbol_memories` | Retrieves notes for a symbol or file | Historian |
| `purge_stale_memories` | Cleans up stale or orphaned memory notes | Historian |

## Tech Stack

| Component | Technology |
|-----------|------------|
| Runtime | Go 1.22+ |
| Protocol | MCP via stdio |
| AST Parser | go-tree-sitter (language-agnostic via `LanguageAdapter` registry) |
| Database | SQLite 3 (WAL mode, pure-Go driver) |
| Vector Engine | sqlite-vec |
| Embeddings | all-MiniLM-L6-v2 via ONNX Runtime (fully local, no API calls) |

## Quickstart

### Build

```bash
go build -o context-link ./cmd/antigravity-link
```

Or use the Makefile:

```bash
make build
```

### Run the MCP Server

```bash
./context-link serve --project-root /path/to/your/project
```

The server communicates over stdio using the MCP protocol.

### Index a Project (Phase 2+)

```bash
./context-link index /path/to/your/project
```

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--db-path` | `.context-link.db` | Path to SQLite database |
| `--project-root` | Current directory | Root directory of the project to index |
| `--log-level` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `--config` | `.context-link.yaml` | Path to config file |

### IDE Configuration

Add to your IDE's MCP server settings:

```json
{
  "mcpServers": {
    "context-link": {
      "command": "/path/to/context-link",
      "args": ["serve", "--project-root", "/path/to/your/project"]
    }
  }
}
```

## Configuration

context-link can be configured via a `.context-link.yaml` file in the project root:

```yaml
db_path: .context-link.db
project_root: .
log_level: info
```

Environment variables with the `CONTEXT_LINK_` prefix also work (e.g., `CONTEXT_LINK_LOG_LEVEL=debug`).

## Project Structure

```
context-link/
  cmd/
    antigravity-link/      # CLI entry point
  internal/
    server/                 # MCP server, JSON-RPC handler
    indexer/                # Tree-sitter AST parsing, file walker, language adapters
      adapters/             # LanguageAdapter implementations (TS, TSX, Go, ...)
    store/                  # SQLite schema, migrations, CRUD
    vectorstore/            # sqlite-vec integration, embeddings
    memory/                 # Memory CRUD, stale detection
    tools/                  # MCP tool definitions & handlers
    config/                 # Configuration loading
  pkg/
    models/                 # Shared types (Symbol, Memory, etc.)
  scripts/                  # Build, test, benchmark scripts
  testdata/                 # Sample repos for integration tests
```

## Development

### Prerequisites

- Go 1.22+

### Run Tests

```bash
make test
```

### Lint

```bash
make lint
```

### Test Coverage

```bash
make coverage
```

## Adding a New Language

context-link uses a `LanguageAdapter` interface backed by Tree-sitter. To add support for a new language:

1. **Import the grammar** — add the Tree-sitter C-binding (e.g., `smacker/go-tree-sitter/python`)
2. **Write query files** — create `.scm` Scheme queries for symbol extraction (`@symbol.name`, `@symbol.body`, `@symbol.function`, `@symbol.class`) and dependency extraction (`@dependency.import`, `@dependency.call`)
3. **Implement the adapter** — create a file in `internal/indexer/adapters/` that satisfies the `LanguageAdapter` interface:

```go
type LanguageAdapter interface {
    Name() string              // e.g. "python"
    Extensions() []string      // e.g. [".py"]
    GetLanguage() *sitter.Language
    GetSymbolQuery() []byte
    GetDependencyQuery() []byte
}
```

4. **Register it** — call `registry.Register(adapter)` at startup
5. **Add fixtures** — create `testdata/langs/<lang>/` with sample files and golden JSON snapshots

The file walker, parser, extractor, and embedding pipeline all work automatically for any registered adapter — no core code changes needed.

## Security

- **Zero external network calls.** The binary functions fully air-gapped.
- All file paths are validated against the project root to prevent traversal attacks.
- SQLite database is created with `0600` permissions (owner read/write only).
- All SQL queries use parameterized statements.

## License

TBD
