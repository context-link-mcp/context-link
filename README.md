# context-link

**Local MCP Context Gateway for AI Coding Agents**

context-link is a local MCP server that serves structured code context to AI agents, dramatically reducing token consumption compared to reading entire files. It indexes codebases using a language-agnostic Tree-sitter adapter system, builds a symbol + dependency graph, and exposes tools over the Model Context Protocol.

**Supported languages:** TypeScript (`.ts`), TSX/JSX (`.tsx`, `.jsx`), Go (`.go`) — extensible via the `LanguageAdapter` interface.

## The Problem

AI coding agents read entire files to understand context. This brute-force approach is expensive, slow, and prone to context-window overflow. context-link acts as a structural intermediary that extracts and serves only the relevant code symbols, dependencies, and historical notes an agent needs.

## How It Works: The Context Funnel

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

## Current Status

| Phase | Description | Status |
|-------|-------------|--------|
| Phase 1 | Gateway Foundation (CLI, SQLite, MCP server) | Complete |
| Phase 2 | Structural Indexer (Tree-sitter, symbol graph, deps) | Complete |
| Phase 3 | Semantic Search (ONNX embeddings, sqlite-vec) | Planned |
| Phase 4 | Memory Persistence (notes, stale detection) | Planned |
| Phase 5 | Integration & Optimization | Planned |

## MCP Tools

| Tool | Description | Status |
|------|-------------|--------|
| `ping` | Health-check for connectivity validation | Implemented |
| `read_architecture_rules` | Returns parsed ARCHITECTURE.md sections | Implemented |
| `get_code_by_symbol` | Extracts exact code + transitive dependencies (BFS, depth 0-3) | Implemented |
| `semantic_search_symbols` | Natural-language symbol discovery | Phase 3 |
| `save_symbol_memory` | Attaches persistent notes to symbols | Phase 4 |
| `get_symbol_memories` | Retrieves notes for a symbol or file | Phase 4 |

## Tech Stack

| Component | Technology |
|-----------|------------|
| Runtime | Go 1.22+ |
| Protocol | MCP via stdio (`mcp-go` v0.44.1) |
| AST Parser | `go-tree-sitter` (language-agnostic via `LanguageAdapter` registry) |
| Database | SQLite 3 (WAL mode, pure-Go driver via `modernc.org/sqlite`) |
| Vector Engine | sqlite-vec (Phase 3) |
| Embeddings | all-MiniLM-L6-v2 via ONNX Runtime (Phase 3) |

## Quickstart

### Prerequisites

- Go 1.22+
- A C compiler (gcc) — required for Tree-sitter CGo bindings
  - **Windows:** `winget install -e --id niXman.mingw-w64-ucrt`
  - **macOS:** `xcode-select --install`
  - **Linux:** `apt install build-essential` (or equivalent)

See [BUILDING.md](BUILDING.md) for detailed platform-specific instructions.

### Build

```bash
CGO_ENABLED=1 go build -o ./bin/context-link ./cmd/context-link
```

### Index a Project

```bash
./bin/context-link index --project-root /path/to/project --repo-name myproject
```

Supports incremental re-indexing — only changed files are re-processed.

### Run the MCP Server

```bash
./bin/context-link serve --project-root /path/to/project
```

The server communicates over stdio using the MCP protocol.

### IDE Integration (Claude Code / VS Code)

Add a `.mcp.json` file to your project root:

```json
{
  "mcpServers": {
    "context-link": {
      "command": "/absolute/path/to/context-link",
      "args": ["serve", "--project-root", "/absolute/path/to/project"]
    }
  }
}
```

Then reload the VS Code window. The MCP tools will be available in Claude Code automatically.

### CLI Flags

**Global flags** (all subcommands):

| Flag | Default | Description |
|------|---------|-------------|
| `--db-path` | `.context-link.db` | Path to SQLite database |
| `--project-root` | Current directory | Root directory of the project |
| `--log-level` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `--config` | `.context-link.yaml` | Path to config file |

**`index` subcommand flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--repo-name` | Directory name | Repository name for multi-repo namespacing |
| `--workers` | `4` | Number of parallel worker goroutines |

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
  cmd/context-link/           # CLI entry point (cobra)
  internal/
    server/                   # MCP server, JSON-RPC handler (mcp-go)
    indexer/                  # Indexing pipeline orchestrator
      adapters/               # LanguageAdapter implementations (TS, TSX, Go)
        queries/              # .scm Tree-sitter Scheme query files
    store/                    # SQLite schema, migrations, CRUD
      migrations/             # Versioned SQL migration files
    tools/                    # MCP tool definitions & handlers
    config/                   # Configuration loading (viper)
  pkg/models/                 # Shared types (Symbol, Memory, File, Dependency)
  testdata/
    langs/{ts,tsx,go}/        # Language-specific test fixtures
    golden/                   # Snapshot test golden JSON files
```

## Adding a New Language

context-link uses a `LanguageAdapter` interface backed by Tree-sitter. To add support for a new language:

1. **Import the grammar** — add the Tree-sitter C-binding (e.g., `smacker/go-tree-sitter/python`)
2. **Write query files** — create `.scm` queries in `internal/indexer/adapters/queries/` for symbol extraction (`@symbol.name`, `@symbol.body`, `@symbol.function`, `@symbol.class`) and dependency extraction (`@dependency.import`, `@dependency.call`)
3. **Implement the adapter** — create a file in `internal/indexer/adapters/` satisfying:

```go
type LanguageAdapter interface {
    Name() string              // e.g. "python"
    Extensions() []string      // e.g. [".py"]
    GetLanguage() *sitter.Language
    GetSymbolQuery() []byte
    GetDependencyQuery() []byte
}
```

4. **Register it** — call `registry.Register(adapter)` in `buildLanguageRegistry()` in `cmd/context-link/main.go`
5. **Add fixtures** — create `testdata/langs/<lang>/` with sample files and run snapshot tests with `-update-golden`

The walker, parser, extractor, and store pipeline all work automatically for any registered adapter.

## Development

### Run Tests

```bash
CGO_ENABLED=1 go test ./... -count=1
```

### Update Snapshot Golden Files

```bash
CGO_ENABLED=1 go test ./internal/indexer/ -args -update-golden
```

### Lint

```bash
golangci-lint run
```

## Security

- **Zero external network calls.** The binary functions fully air-gapped.
- All file paths are validated against the project root to prevent traversal attacks.
- SQLite database is created with `0600` permissions (owner read/write only).
- All SQL queries use parameterized statements.

## License

TBD
