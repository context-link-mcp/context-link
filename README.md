# context-link

**Local MCP Context Gateway for AI Coding Agents**

context-link is a local MCP server that serves structured code context to AI agents, dramatically reducing token consumption compared to reading entire files. It indexes codebases using a language-agnostic Tree-sitter adapter system, builds a symbol + dependency graph, and exposes tools over the Model Context Protocol.

**Supported languages:** TypeScript (`.ts`), TSX/JSX (`.tsx`, `.jsx`), Go (`.go`), Python (`.py`), JavaScript (`.js`, `.mjs`), Rust (`.rs`), Java (`.java`), C (`.c`, `.h`), C++ (`.cpp`, `.hpp`, `.cc`, `.cxx`, `.hxx`, `.hh`), C# (`.cs`) — extensible via the `LanguageAdapter` interface.

## The Problem

AI coding agents read entire files to understand context. This brute-force approach is expensive, slow, and prone to context-window overflow. 

## The Solution
context-link acts as a structural intermediary that extracts and serves only the relevant code symbols, dependencies, and historical notes an agent needs. By eliminating blind file reads, it reduces token consumption by over 85%.

| Scenario | Avg Token Reduction | Target |
|----------|---------------------|--------|
| Single symbol lookup (depth=0) | **91.0%** | >80% |
| Aggregate (all symbols vs all files) | **92.7%** | >80% |
| Symbol + dependencies (depth=1) | **85.7%** | >80% |

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

## Installation

### Homebrew (macOS / Linux)

```bash
brew install context-link-mcp/tap/context-link
```

### Download Binary

Pre-built binaries for Linux, macOS, and Windows (amd64) are available on the [GitHub Releases](https://github.com/context-link-mcp/context-link/releases) page.

### Build from Source

See the [Building](#building) section below.


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

# Release build (stripped binary, with version)
CGO_ENABLED=1 go build -ldflags="-s -w -X main.version=v0.3.0" -o ./bin/context-link ./cmd/context-link

# Or use the Makefile (auto-detects version from git tags)
make build
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

## Quick Start

The fastest way to get started is using a pre-built binary. (If you prefer to compile from source, see the Building section).

### Step 1: Install context-link
**macOS / Linux:**

```bash
brew install context-link-mcp/tap/context-link
```
**Windows & Others:**
Download the latest executable for your OS from the GitHub Releases page and place it in your system's PATH.

### Step 2: Index Your Project
Navigate to your codebase and run the indexer. This creates a local `.context-link.db` file containing your vector embeddings and AST mappings.

```bash
cd /path/to/your/project
context-link index --project-root .
```
*(Note: You can re-run this anytime; it incrementally processes only changed files.)*

### Step 3: Connect Your AI IDE
context-link communicates via the Model Context Protocol (MCP) over stdio. Configure your preferred AI agent to launch the server:

**For Claude Code:**
Create or edit `.mcp.json` in your project root:

```json
{
  "mcpServers": {
    "context-link": {
      "command": "context-link",
      "args": ["serve", "--project-root", "."]
    }
  }
}
```
**For Cursor:**

Go to Settings > Features > MCP.

Click + Add New MCP Server.

Name: `context-link`

Type: `command`

Command: `context-link serve --project-root /absolute/path/to/your/project`

**For Windsurf:**
Add to your `mcp_config.json`:

```json
{
  "mcpServers": {
    "context-link": {
      "command": "context-link",
      "args": ["serve", "--project-root", "/absolute/path/to/your/project"]
    }
  }
}
```

### Step 4: Start Coding
Once connected, your AI agent now has access to the full suite of context-link tools. You can instruct your agent to use the recommended workflow by adding this to your prompt or `.cursorrules` file:

> The `explore_codebase` MCP prompt encodes this protocol and can be invoked directly in supported clients.

Once connected, your AI agent has access to the full suite of context-link tools. To ensure your agent uses them efficiently, apply the **Recommended Agent Workflow** (see below) to your `.cursorrules` or system prompt.


## Recommended Agent Workflow

To achieve maximum token efficiency, your AI needs to know how to use the `context-link` tools.

You can either invoke the built-in `explore_codebase` MCP prompt directly in supported clients, or copy and paste the following block into your `.cursorrules`, agent custom instructions, or system prompt:

``` plaintext
When exploring and modifying this codebase, you must prioritize the context-link MCP tools to minimize token consumption. Do not read raw files directly unless absolutely necessary. Follow this structural workflow:

1. Call `read_architecture_rules` at the start of a session to understand the project's constraints.
2. Use `semantic_search_symbols` to discover relevant symbols by intent.
3. Use `get_file_skeleton` to understand a file's structure before diving in.
4. Use `get_code_by_symbol` to retrieve only the specific code you need, along with its dependencies.
5. Use `get_symbol_usages` and `get_call_tree` to explore call hierarchies and reverse dependencies.
6. Use `find_dead_code` to discover unused symbols and `get_blast_radius` to assess the impact of your planned changes.
7. Use `find_http_routes` to discover REST route definitions and match them to call sites.
8. Always check the `memories` array in tool responses for prior human or agent findings about a symbol.
9. After completing a significant feature or fix, call `save_symbol_memory` to persist your architectural findings for future sessions.
```

---

## Advanced Usage

While the Quick Start covers standard setups, `context-link` supports advanced workflows for complex environments.

### Advanced Indexing

The index is stored in `.context-link.db` in your current directory. Re-run anytime — only changed files are re-processed. Semantic search embeddings are generated automatically.
```bash
./bin/context-link index --project-root /path/to/your/project
```

**Force a full re-index (e.g., after switching embedder):**

If you switch embedding models or want to cleanly rebuild the database (bypassing the incremental file hash check), use the `--force` flag:
```bash
./bin/context-link index --project-root /path/to/your/project --force
```

**Index multiple repos into the same database:**
```bash
./bin/context-link index --project-root /path/to/repo-a --repo-name repo-a
./bin/context-link index --project-root /path/to/repo-b --repo-name repo-b
```

### Manual Server Testing

The server communicates via the MCP protocol over `stdio`. While it is meant to be launched by your IDE, you can start the server manually for debugging purposes or to verify initialization:
```bash
./bin/context-link serve --project-root /path/to/your/project
```

### Custom ONNX Model (Optional)

The built-in Model2Vec embedder works out of the box. For higher-quality embeddings (`all-MiniLM-L6-v2`, 384-dim), override with ONNX:

1. Download `all-MiniLM-L6-v2.onnx` and `vocab.txt` from Hugging Face
2. Download OnnxRuntime from [ONNX Runtime releases](https://github.com/microsoft/onnxruntime/releases)
3. Pass `--model-path` and `--vocab-path` to both `index` and `serve`

Switching between Model2Vec and ONNX requires `--force` re-indexing (128 vs 384 dimensions).

---

## Maintenance & Uninstallation

`context-link` is entirely self-contained. It does not run background telemetry or scatter hidden configuration files across your system.

### To clear a project's index:
Simply delete the local SQLite database in the root of your project. This will completely remove all vector embeddings and AST mappings for that repository.
```bash
rm .context-link.db
```

### To completely uninstall:

1. Delete the context-link binary from your system path (or run `brew uninstall context-link`).
2. Remove the configuration entry from your IDE's MCP settings (e.g., `mcp_config.json` or `.mcp.json`).
---

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

## CLI Reference

**Global flags** (all subcommands):

| Flag | Default | Description |
|------|---------|-------------|
| `--db-path` | `.context-link.db` | Path to SQLite database |
| `--project-root` | Current directory | Root directory of the project |
| `--log-level` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `--config` | `.context-link.yaml` | Path to config file |

**`index` subcommand flags:**

| Category | Flag | Default | Description |
|---|---|---|---|
| **Execution** | `--force` | `false` | Force full re-index, bypassing incremental file hash checks |
|  | `--repo-name` | Directory name | Repository name for multi-repo namespacing |
| **Performance** | `--workers` | `4` | Number of parallel worker goroutines for parsing |
| **Embeddings** _(Advanced)_ | `--model-path` | _(built-in)_ | Path to custom ONNX model (overrides built-in Model2Vec) |
|  | `--vocab-path` | _(built-in)_ | Path to `vocab.txt` for the ONNX tokenizer |
|  | `--ort-lib-path` | _(system)_ | Path to OnnxRuntime shared library |


**`serve` subcommand flags:**

| Category | Flag | Default | Description |
|---|---|---|---|
| **Behavior** | `--watch` | `false` | Auto re-index on file changes (fsnotify with 500ms debounce) |
|  | `--tools` | _(all)_ | Comma-separated list of MCP tools to enable (e.g., `ping,get_code_by_symbol`) |
| **Embeddings** _(Advanced)_ | `--model-path` | _(built-in)_ | Path to custom ONNX model (overrides built-in Model2Vec) |
|  | `--vocab-path` | _(built-in)_ | Path to `vocab.txt` for the ONNX tokenizer |
|  | `--ort-lib-path` | _(system)_ | Path to OnnxRuntime shared library |

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

# Control which MCP tools are exposed (default: all).
# Use this to reduce prompt token budget by disabling unused tools.
# tools:
#   - ping
#   - semantic_search_symbols
#   - get_code_by_symbol
#   - get_file_skeleton
#   - get_symbol_usages
#   - get_call_tree
#   - read_architecture_rules
#   - memory  # registers save_symbol_memory, get_symbol_memories, purge_stale_memories
#   - find_dead_code
#   - get_blast_radius
#   - find_http_routes
```

Environment variables with the `CONTEXT_LINK_` prefix also work (e.g., `CONTEXT_LINK_LOG_LEVEL=debug`).

---

## MCP Tool Reference

All tools return structured JSON with a `metadata` object including `timing_ms` for observability, plus `tokens_saved_est`, `cost_avoided_est`, `session_tokens_saved`, and `session_cost_avoided` for token savings tracking.

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
  "metadata": { "timing_ms": 0, "total_results": 1, "query": "token validation" }
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

### `get_file_skeleton`

Returns a structural outline of a file — symbol names, kinds, and signatures (first line of code block only). No full code bodies. Use to understand a file's structure before extracting specific symbols.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `file_path` | string | yes | — | Relative file path (e.g. `internal/store/symbols.go`) |

```json
{
  "file_path": "internal/store/symbols.go",
  "symbols": [
    { "name": "GetSymbolByName", "kind": "function", "signature": "func GetSymbolByName(ctx context.Context, db *DB, repoName, name string) (*models.Symbol, error) {", "start_line": 42 }
  ],
  "symbol_count": 15,
  "metadata": { "timing_ms": 2 }
}
```

### `get_symbol_usages`

Reverse dependency lookup — finds all callers/references of a symbol.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `symbol_name` | string | yes | — | Name or qualified name of the symbol |

```json
{
  "symbol": { "name": "hashFile", "qualified_name": "walker.hashFile", "kind": "function", "file_path": "internal/indexer/walker.go" },
  "usages": [
    { "caller_name": "Walk", "caller_qualified_name": "Walker.Walk", "caller_kind": "method", "file_path": "internal/indexer/walker.go", "start_line": 55, "dep_kind": "call" }
  ],
  "usage_count": 3,
  "metadata": { "timing_ms": 4 }
}
```

### `get_call_tree`

Traverses the dependency graph to show a call hierarchy. Use `direction='callees'` to see what a symbol calls, or `direction='callers'` to see what calls it.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `symbol_name` | string | yes | — | Root symbol name or qualified name |
| `direction` | string | no | `callees` | `callees` or `callers` |
| `depth` | number | no | `1` | Max traversal depth (max `3`) |

```json
{
  "root": { "name": "Walk", "qualified_name": "Walker.Walk", "kind": "method", "file_path": "internal/indexer/walker.go" },
  "edges": [
    { "depth": 1, "name": "hashFile", "qualified_name": "walker.hashFile", "kind": "function", "file_path": "internal/indexer/walker.go", "start_line": 120, "dep_kind": "call" }
  ],
  "edge_count": 5,
  "direction": "callees",
  "metadata": { "timing_ms": 3 }
}
```

### `find_dead_code`

Discovers symbols with zero inbound dependency edges (no callers). Entry points (`main`, `init`) and variables are excluded by default.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `file_path` | string | no | _(all)_ | Limit search to a specific file |
| `kind` | string | no | _(all)_ | Filter: `function`, `class`, `method`, `interface`, `type` |
| `exclude_exported` | boolean | no | `true` | Exclude exported symbols (uppercase-initial in Go) |
| `limit` | number | no | `50` | Max results (max `200`) |

```json
{
  "dead_symbols": [
    { "name": "unusedHelper", "qualified_name": "pkg.unusedHelper", "kind": "function",
      "file_path": "internal/pkg/helper.go", "start_line": 42, "language": "go" }
  ],
  "count": 1,
  "metadata": { "timing_ms": 5, "tokens_saved_est": 1200 }
}
```

### `get_blast_radius`

BFS through callers to show everything affected by changing a symbol. Groups results by file and depth.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `symbol_name` | string | yes | — | Name or qualified name of the symbol to analyze |
| `depth` | number | no | `2` | Max traversal depth (max `5`) |

```json
{
  "root": { "name": "hashFile", "kind": "function", "file_path": "internal/indexer/walker.go" },
  "affected_files": {
    "internal/indexer/walker.go": [
      { "name": "Walk", "kind": "method", "depth": 1, "dep_kind": "call" }
    ]
  },
  "total_affected": 3,
  "files_affected": 2,
  "by_depth": { "1": 2, "2": 1 },
  "metadata": { "timing_ms": 8, "tokens_saved_est": 2400 }
}
```

### `find_http_routes`

Discovers HTTP route definitions and call sites in the codebase. Supports Express, Gin, FastAPI, Flask, and similar frameworks. Matches route definitions to their call sites with confidence scoring.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `method` | string | no | _(all)_ | Filter by HTTP method (GET, POST, PUT, DELETE, PATCH) |
| `path` | string | no | _(all)_ | Filter routes by path substring (e.g., `/api/users`) |
| `file_path` | string | no | _(all)_ | Limit search to a specific file path |

```json
{
  "routes": [
    { "method": "GET", "path_pattern": "/api/users/:id", "handler": "getUser",
      "file_path": "src/routes/users.ts", "start_line": 15, "framework": "express", "kind": "definition" }
  ],
  "route_count": 1,
  "matches": [
    { "definition": { "method": "GET", "path_pattern": "/api/users/:id", "file_path": "src/routes/users.ts" },
      "call_site": { "method": "GET", "path_pattern": "/api/users/123", "file_path": "src/client/api.ts" },
      "confidence": 0.9 }
  ],
  "match_count": 1,
  "metadata": { "timing_ms": 12 }
}
```

---

## Adding a New Language

1. **Import the grammar** — add the Tree-sitter C-binding (e.g., `smacker/go-tree-sitter/python`)
2. **Write query files** — create `.scm` queries in `internal/indexer/adapters/queries/`
3. **Implement the adapter** — satisfy `LanguageAdapter` interface in `internal/indexer/adapters/`
4. **Register it** — call `registry.Register(adapter)` in `buildLanguageRegistry()` in `cmd/context-link/main.go`
5. **Add fixtures** — create `testdata/langs/<lang>/` and update golden snapshots

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full component map and project structure.

## Benchmarks

Measured after Phase 5 optimizations (batch DB operations, eliminated double-parsing). All benchmarks run on a single machine with SQLite WAL mode.

### Indexing Performance

| Repository | Language | Files | Symbols | Embeddings | Index Time | DB Size |
|------------|----------|-------|---------|------------|------------|---------|
| [context-link](https://github.com/context-link-mcp/context-link) (self) | Go | 59 | 539 | 528 | 1.05s | 1.5 MB |
| [echo](https://github.com/labstack/echo) | Go | 90 | 1,901 | 1,576 | 1.85s | 3.1 MB |
| [gin](https://github.com/gin-gonic/gin) | Go | 99 | 1,892 | 1,722 | 3.54s | 2.4 MB |
| [tRPC](https://github.com/trpc/trpc) | TypeScript | 381 | 772 | 767 | 6.82s | — |

### Semantic Search Latency

Measured on context-link (560 symbols, 545 embeddings), 100 iterations per query, 10 diverse queries.

| Mode | P50 | Avg | Description |
|------|-----|-----|-------------|
| Uncached (DB scan) | 1,880µs | 1,914µs | Full SQLite BLOB read + decode per query |
| Cached cold | 2,180µs | 2,159µs | First call loads vectors into memory |
| **Cached warm** | **197µs** | **187µs** | In-memory KNN dot-product scan |
| **End-to-end** | **202µs** | **196µs** | Embed query + cached KNN search |

**150x improvement** over pre-optimization baseline (~30ms → 0.2ms). The in-memory vector cache eliminates all SQLite I/O after the first query.

### Semantic Search Quality

Example queries and top results:

| Repo | Query | Top Results |
|------|-------|-------------|
| echo | "static file serving" | `Static`, `StaticFileHandler`, `serveFile` |
| gin | "route parameter binding" | `Param`, `Params`, `ShouldBindUri` |
| tRPC | "middleware pipeline" | `createMiddlewareFactory`, `createBuilder` |
| context-link | "tree-sitter parsing" | `processFile`, `extractSymbolsAndDeps` |

### Optimization Impact

`context-link` is aggressively optimized for speed and low latency, ensuring it never bottlenecks your AI agent. 

**Core Benchmarks:**
* **150x Search Speedup:** Semantic search latency reduced from ~30ms to **0.2ms** via in-memory vector caching and heap-based Top-K selection.
* **4.9x Indexing Speedup:** Parallel file hashing and SQLite PRAGMA tuning process large repositories in ~1 second.
* **Instant Incremental Updates:** Re-indexing detects changes and updates the graph in **<10ms**.

See [ARCHITECTURE.md](ARCHITECTURE.md#optimization-impact) for detailed breakdown.



### Semantic Search Token Efficiency

A `semantic_search_symbols` call returning 10 results averages **~388 tokens** — this is a metadata-only listing (symbol names, kinds, file paths, similarity scores) with no code. The full end-to-end flow (search → extract top result with dependencies) compares favorably to reading source files directly:

| Scenario | Avg Tokens | vs. Full File |
|----------|-----------|---------------|
| Search only (10 results) | ~388 | Discovery without reading any file |
| Search + `get_code_by_symbol` (depth=0) | ~615 | Top result extraction |
| Search + `get_code_by_symbol` (depth=1) | ~691 | Top result with dependencies |
| Reading the full source file | ~867 | Baseline comparison |

Measured on context-link itself (59 files, 1,056 symbols). A typical `get_code_by_symbol` call returns ~9% of what reading the full file would require.

For large files the savings are dramatic (86%+ reduction for a 3,200-token file). The comparison is conservative — in practice, an agent without semantic search would read multiple files to find the right symbol, making real-world savings significantly higher.

## Security

- **Zero external network calls.** The binary functions fully air-gapped.
- All file paths are validated against the project root to prevent traversal attacks.
- SQLite database is created with `0600` permissions (owner read/write only).
- All SQL queries use parameterized statements.

## License

Apache-2.0 — see [LICENSE](LICENSE).
