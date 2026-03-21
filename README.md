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

## Tech Stack

| Component | Technology |
|-----------|------------|
| Runtime | Go 1.22+ |
| Protocol | MCP via stdio (`mcp-go` v0.44.1) |
| AST Parser | `go-tree-sitter` (language-agnostic via `LanguageAdapter` registry) |
| Database | SQLite 3 (WAL mode, pure-Go driver via `modernc.org/sqlite`) |
| Vector Search | Go-side KNN over L2-normalized float32 BLOBs in `vec_symbols` table |
| Embeddings | Built-in `potion-base-4M` Model2Vec (128-dim, zero-config); optional ONNX override (`all-MiniLM-L6-v2`) |

---

## Building

> Semantic search works out of the box using the built-in Model2Vec embedder (embedded in the binary). ONNX Runtime is only needed if you want to use a custom ONNX model as an override.

### Prerequisites

| Dependency | Version | Purpose |
|-----------|---------|---------|
| Go | 1.22+ | Runtime and build toolchain |
| GCC (C compiler) | Any recent | Required by `smacker/go-tree-sitter` CGo bindings |
| Git | Any | Dependency fetching |

The SQLite driver (`modernc.org/sqlite`) is pure Go and does **not** require CGo.

### Platform-Specific Setup

**Windows**
```powershell
winget install -e --id niXman.mingw-w64-ucrt
# Restart terminal, then verify:
gcc --version
```

**macOS**
```bash
xcode-select --install
```

**Linux (Debian/Ubuntu)**
```bash
sudo apt-get install build-essential
```

**Linux (Fedora/RHEL)**
```bash
sudo dnf install gcc gcc-c++ make
```

### Build Commands

```bash
# Development build
CGO_ENABLED=1 go build -o ./bin/context-link ./cmd/context-link

# Release build (stripped binary)
CGO_ENABLED=1 go build -ldflags="-s -w" -o ./bin/context-link ./cmd/context-link
```

On Windows, the output binary will be `context-link.exe`.

### Running Tests

```bash
CGO_ENABLED=1 go test ./... -count=1
```

Update snapshot golden files:

```bash
CGO_ENABLED=1 go test ./internal/indexer/ -args -update-golden
```

### Troubleshooting

**`CGO_ENABLED=0` or "gcc not found"** — Tree-sitter grammars are C libraries compiled via CGo. Ensure a C compiler is installed and on PATH.

**Windows: "cc1.exe: sorry, unimplemented: 64-bit mode not compiled in"** — Use the 64-bit UCRT variant: `winget install -e --id niXman.mingw-w64-ucrt`

**Slow first build** — The first build compiles all Tree-sitter C sources. Subsequent builds use the Go build cache.

---

## Usage Guide

### Step 1: Build the Binary

```bash
git clone https://github.com/context-link/context-link.git
cd context-link
CGO_ENABLED=1 go build -o ./bin/context-link ./cmd/context-link
```

### Step 2: Index Your Project

```bash
./bin/context-link index --project-root /path/to/your/project
```

The index is stored in `.context-link.db` in your current directory. Re-run anytime — only changed files are re-processed. Semantic search embeddings are generated automatically.

**Force a full re-index** (e.g., after switching embedder):
```bash
./bin/context-link index --project-root /path/to/your/project --force
```

**Index multiple repos into the same database:**
```bash
./bin/context-link index --project-root /path/to/repo-a --repo-name repo-a
./bin/context-link index --project-root /path/to/repo-b --repo-name repo-b
```

### Step 3: Start the MCP Server

```bash
./bin/context-link serve --project-root /path/to/your/project
```

The server reads from `stdin` and writes to `stdout` using the MCP protocol. It is meant to be launched by your IDE, not run directly.

### Step 4: Connect Your IDE

**Claude Code (recommended)** — create or edit `.mcp.json` in your project root:

```json
{
  "mcpServers": {
    "context-link": {
      "command": "/absolute/path/to/bin/context-link",
      "args": ["serve", "--project-root", "/absolute/path/to/your/project"]
    }
  }
}
```

Then reload Claude Code (run `/doctor` to confirm the server connected).

**VS Code / other MCP clients** — follow your client's MCP server configuration. The entry point is the same `serve` subcommand.

### Step 5: Custom ONNX Model (Optional)

The built-in Model2Vec embedder works out of the box. For higher-quality embeddings (`all-MiniLM-L6-v2`, 384-dim), override with ONNX:

1. Download `all-MiniLM-L6-v2.onnx` and `vocab.txt` from Hugging Face
2. Download OnnxRuntime from [ONNX Runtime releases](https://github.com/microsoft/onnxruntime/releases)
3. Pass `--model-path` and `--vocab-path` to both `index` and `serve`

Switching between Model2Vec and ONNX requires `--force` re-indexing (128 vs 384 dimensions).

### Step 6: Recommended Agent Workflow

For maximum token efficiency:

1. Call `read_architecture_rules` at the start of a session.
2. Use `semantic_search_symbols` to discover relevant symbols by intent — do not read files directly.
3. Use `get_code_by_symbol` to retrieve only the code you need, with dependencies.
4. Check `memories` in the response for prior findings about the symbol.
5. After completing work, call `save_symbol_memory` to persist findings for future sessions.

The `explore_codebase` MCP prompt encodes this protocol and can be invoked directly in supported clients.

---

## CLI Reference

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
| `--force` | `false` | Force full re-index, bypassing incremental hash check |
| `--model-path` | _(built-in)_ | Path to custom ONNX model — overrides built-in Model2Vec |
| `--vocab-path` | _(built-in)_ | Path to `vocab.txt` for the ONNX tokenizer |
| `--ort-lib-path` | _(system)_ | Path to OnnxRuntime shared library |

**`serve` subcommand flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `--model-path` | _(built-in)_ | Path to custom ONNX model — overrides built-in Model2Vec |
| `--vocab-path` | _(built-in)_ | Path to `vocab.txt` for the ONNX tokenizer |
| `--ort-lib-path` | _(system)_ | Path to OnnxRuntime shared library |

### Configuration File

```yaml
db_path: .context-link.db
project_root: .
log_level: info

# Semantic search uses built-in Model2Vec by default (zero-config).
# Uncomment below to override with a custom ONNX model:
# model_path: /path/to/all-MiniLM-L6-v2.onnx
# vocab_path: /path/to/vocab.txt
# ort_lib_path: ""
```

Environment variables with the `CONTEXT_LINK_` prefix also work (e.g., `CONTEXT_LINK_LOG_LEVEL=debug`).

---

## MCP Tool Reference

All tools return structured JSON with a `metadata.timing_ms` field for observability.

### `ping`

Health-check tool.

**Parameters:** none

```json
{ "status": "ok", "metadata": { "timing_ms": 1 } }
```

### `read_architecture_rules`

Returns `ARCHITECTURE.md` as structured sections. Use at session start.

**Parameters:** none

```json
{
  "sections": [
    { "title": "Overview", "content": "..." }
  ],
  "metadata": { "timing_ms": 3, "source": "/path/to/ARCHITECTURE.md" }
}
```

### `get_code_by_symbol`

Extracts exact source code of a named symbol with transitive dependencies and import statements.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `symbol_name` | string | yes | — | Name or qualified name (e.g. `validateToken` or `UserAuth.validateToken`) |
| `depth` | number | no | `1` | Dependency depth: `0` = symbol only, `1` = direct deps, max `3` |

```json
{
  "symbol": {
    "name": "validateToken", "qualified_name": "auth.validateToken",
    "kind": "function", "file_path": "internal/auth/token.go",
    "code_block": "func validateToken(tok string) error { ... }",
    "start_line": 42, "end_line": 61, "language": "go"
  },
  "dependencies": [ { "name": "parseJWT", "code_block": "..." } ],
  "dependency_count": 1,
  "memories": [ { "id": 7, "note": "Uses RS256.", "is_stale": false } ],
  "memory_count": 1,
  "metadata": { "timing_ms": 18 }
}
```

### `semantic_search_symbols`

Discovers symbols by natural-language intent via vector embeddings. Does **not** return code — call `get_code_by_symbol` next.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `query` | string | yes | — | Natural-language description of what you're looking for |
| `top_k` | number | no | `10` | Max results (max `50`) |
| `kind` | string | no | _(all)_ | Filter: `function`, `class`, `interface`, `type`, `variable` |
| `file_path_prefix` | string | no | _(all)_ | Filter by file path prefix |
| `min_similarity` | number | no | `0.3` | Minimum cosine similarity (0.0–1.0) |

```json
{
  "results": [
    {
      "symbol_name": "validateToken", "kind": "function",
      "file_path": "internal/auth/token.go",
      "similarity_score": 0.87, "memory_count": 1
    }
  ],
  "metadata": { "timing_ms": 45, "total_results": 1, "query": "token validation" }
}
```

### `save_symbol_memory`

Attaches a persistent note to a symbol. Survives re-indexing; auto-flagged stale on changes. Duplicates are deduplicated.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `symbol_name` | string | yes | — | Symbol to annotate |
| `note` | string | yes | — | The note (max 2000 chars) |
| `author` | string | no | `"agent"` | `"agent"` or `"developer"` |

```json
{
  "memory_id": 12, "symbol_name": "auth.validateToken",
  "file_path": "internal/auth/token.go",
  "metadata": { "timing_ms": 5 }
}
```

### `get_symbol_memories`

Retrieves notes for a symbol or file. At least one of `symbol_name` or `file_path` required.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `symbol_name` | string | no* | — | Symbol to look up |
| `file_path` | string | no* | — | File path for all symbols |
| `offset` | number | no | `0` | Pagination offset |
| `limit` | number | no | `20` | Max results (max `100`) |

When `is_stale` is `true`: `stale_reason` explains why, `last_known_symbol` / `last_known_file` show the symbol's location at time of staling.

### `purge_stale_memories`

Deletes stale memories. Use `orphaned_only=true` to only delete memories with no linked symbol.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `orphaned_only` | boolean | no | `false` | Only delete orphaned memories |

---

## Adding a New Language

1. **Import the grammar** — add the Tree-sitter C-binding (e.g., `smacker/go-tree-sitter/python`)
2. **Write query files** — create `.scm` queries in `internal/indexer/adapters/queries/`
3. **Implement the adapter** — satisfy `LanguageAdapter` interface in `internal/indexer/adapters/`
4. **Register it** — call `registry.Register(adapter)` in `buildLanguageRegistry()` in `cmd/context-link/main.go`
5. **Add fixtures** — create `testdata/langs/<lang>/` and update golden snapshots

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full component map and project structure.

## Security

- **Zero external network calls.** The binary functions fully air-gapped.
- All file paths are validated against the project root to prevent traversal attacks.
- SQLite database is created with `0600` permissions (owner read/write only).
- All SQL queries use parameterized statements.

## License

TBD
