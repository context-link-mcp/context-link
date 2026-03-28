# context-link

**Local MCP Context Gateway for AI Coding Agents**

context-link is a local MCP server that serves structured code context to AI agents, dramatically reducing token consumption compared to reading entire files. It indexes codebases using a language-agnostic Tree-sitter adapter system, builds a symbol + dependency graph, and exposes tools over the Model Context Protocol.

**Supported languages:** TypeScript (`.ts`), TSX/JSX (`.tsx`, `.jsx`), Go (`.go`), Python (`.py`), JavaScript (`.js`, `.mjs`), Rust (`.rs`), Java (`.java`), C (`.c`, `.h`), C++ (`.cpp`, `.hpp`, `.cc`, `.cxx`, `.hxx`, `.hh`), C# (`.cs`) — extensible via the `LanguageAdapter` interface.

## The Problem

AI coding agents read entire files to understand context. This brute-force approach is expensive, slow, and prone to context-window overflow. 

## The Solution
context-link acts as a structural intermediary that extracts and serves only the relevant code symbols, dependencies, and historical notes an agent needs. By eliminating blind file reads, it reduces token consumption by over 85%.

**Built-in token savings tracking:** Every tool response includes `tokens_saved_est`, `cost_avoided_est`, `session_tokens_saved`, and `session_cost_avoided` in the `metadata` field—giving you real-time visibility into your efficiency gains.

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
CGO_ENABLED=1 go build -ldflags="-s -w -X main.version=v0.4.0" -o ./bin/context-link ./cmd/context-link

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
2. Call `reindex_project` after modifying files to keep the symbol graph current (safe to call repeatedly).
3. Use `get_modified_symbols` to discover what code has changed in the working tree (git-aware context).
4. Use `semantic_search_symbols` to discover relevant symbols by intent.
5. Use `get_file_skeleton` to understand a file's structure before diving in.
6. Use `get_code_by_symbol` to retrieve only the specific code you need, along with its dependencies.
7. Use `get_symbol_usages` and `get_call_tree` to explore call hierarchies and reverse dependencies.
8. Use `get_tests_for_symbol` to find test functions for a symbol you're modifying.
9. Use `find_dead_code` to discover unused symbols and `get_blast_radius` to assess the impact of your planned changes.
10. Use `find_http_routes` to discover REST route definitions and match them to call sites.
11. Always check the `memories` array in tool responses for prior human or agent findings about a symbol.
12. After completing a significant feature or fix, call `save_symbol_memory` to persist your architectural findings for future sessions.
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
|---|---|
| Runtime | Go 1.22+ |
| Protocol | MCP via stdio (`mcp-go` v0.44.1) |
| AST Parser | `go-tree-sitter` (language-agnostic via `LanguageAdapter` registry) |
| Database | SQLite 3 (WAL mode, pure-Go driver via `modernc.org/sqlite`) |
| Search Engine | Hybrid search: SQLite FTS5 (BM25) + Go-side Vector KNN, merged via Reciprocal Rank Fusion (RRF) |
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
#   - reindex_project
#   - get_modified_symbols
#   - get_tests_for_symbol
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

### `reindex_project`

Triggers an incremental re-index of the project. Only re-parses files that changed since the last index. Call this after modifying files to ensure the symbol graph, dependencies, and search index are up to date.

**Parameters:** none

```json
{
  "files_scanned": 142,
  "files_changed": 3,
  "files_deleted": 0,
  "files_unchanged": 139,
  "symbols_added": 7,
  "symbols_updated": 7,
  "dependencies_updated": 12,
  "fts_updated": true,
  "embeddings_updated": true,
  "duration_ms": 1240,
  "metadata": { "timing_ms": 1242 }
}
```

**Note:** This operation is idempotent — calling it twice with no file changes returns `files_changed: 0` in ~10ms.

### `get_modified_symbols`

Returns symbols (functions, methods, classes) that overlap with locally modified lines in the git working tree. Use this to orient yourself at the start of a session — it shows exactly what's being actively worked on.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `base_ref` | string | no | `HEAD` | Git ref to diff against (use `main` for branch diff, or a commit SHA) |
| `include_staged` | boolean | no | `true` | Include staged (git add) changes in addition to unstaged |

```json
{
  "base_ref": "HEAD",
  "files_changed": 2,
  "symbols": [
    {
      "name": "ProcessOrder",
      "qualified_name": "OrderService.ProcessOrder",
      "kind": "method",
      "file_path": "internal/orders/service.go",
      "start_line": 42,
      "end_line": 78,
      "changed_lines": [45, 46, 47, 52],
      "change_type": "modified"
    }
  ],
  "metadata": { "timing_ms": 34 }
}
```

**Tip:** Call `reindex_project` first to ensure the index reflects the latest file state.

### `get_tests_for_symbol`

Finds test functions associated with a given symbol. Uses the dependency graph (tests that call the target) and naming conventions as fallback. Helps locate tests to update after modifying a function.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `symbol_name` | string | yes | — | Name or qualified name of the symbol to find tests for |
| `include_code` | boolean | no | `false` | Include full test function bodies (default: false saves tokens) |

```json
{
  "symbol": {
    "name": "ProcessOrder",
    "qualified_name": "OrderService.ProcessOrder",
    "kind": "method",
    "file_path": "internal/orders/service.go"
  },
  "tests": [
    {
      "name": "TestProcessOrder_Success",
      "qualified_name": "TestProcessOrder_Success",
      "file_path": "internal/orders/service_test.go",
      "start_line": 15,
      "end_line": 42,
      "match_reason": "calls_target"
    }
  ],
  "test_count": 1,
  "metadata": { "timing_ms": 8 }
}
```

**Match reasons:** `calls_target` (high confidence: proven call in dependency graph), `name_match` (lower confidence: naming convention only).

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

Extracts exact source code of one or more named symbols with transitive dependencies and import statements. **Supports batch operations** for multiple symbols.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `symbol_name` | string OR array | yes | — | Single symbol name OR array of symbol names (max 50). Supports qualified names (e.g. `UserAuth.validateToken`) |
| `depth` | number | no | `1` | Dependency depth: `0` = symbol only, `1` = direct deps, max `3`. Applies to all symbols in batch |

**Single symbol (backward compatible):**
```json
{ "symbol_name": "validateToken", "depth": 1 }
```

**Batch operation:**
```json
{ "symbol_name": ["validateToken", "UserAuth.login", "formatError"], "depth": 0 }
```

**Response format (batch):**
```json
{
  "results": [
    {
      "input": "validateToken",
      "data": {
        "symbol": { "name": "validateToken", "code_block": "...", ... },
        "dependencies": [...],
        "memories": [...]
      }
    },
    {
      "input": "nonExistent",
      "error": "symbol \"nonExistent\" not found in repository \"repo\""
    }
  ],
  "total_symbols": 2,
  "success_count": 1,
  "error_count": 1,
  "metadata": { "timing_ms": 18, "tokens_saved_est": 4200 }
}
```

**Single symbol response (legacy format for backward compatibility):**

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

Returns a structural outline of one or more files — symbol names, kinds, and signatures (first line of code block only). No full code bodies. Use to understand file structure before extracting specific symbols. **Supports batch operations** for multiple files.

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `file_path` | string OR array | yes | — | Single file path OR array of file paths (max 50). Relative paths (e.g. `internal/store/symbols.go`) |

**Single file (backward compatible):**
```json
{ "file_path": "internal/store/symbols.go" }
```

**Batch operation:**
```json
{ "file_path": ["internal/store/symbols.go", "internal/store/files.go", "internal/store/deps.go"] }
```

**Response format (batch):**
```json
{
  "results": [
    {
      "input": "internal/store/symbols.go",
      "data": {
        "file_path": "internal/store/symbols.go",
        "symbols": [
          { "name": "GetSymbolByName", "kind": "function", "signature": "func GetSymbolByName(...", "start_line": 42 }
        ],
        "symbol_count": 15
      }
    },
    {
      "input": "nonexistent.go",
      "error": "file does not exist: nonexistent.go"
    }
  ],
  "total_files": 2,
  "success_count": 1,
  "error_count": 1,
  "metadata": { "timing_ms": 5, "tokens_saved_est": 3200 }
}
```

**Single file response (legacy format for backward compatibility):**
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

### `search_code_patterns`

**Database-driven regex search across indexed symbol code blocks.** Useful for finding error sentinels, retry logic, specific function calls, or any code pattern matching a regex.

**CRITICAL LIMITATION:** This tool searches **indexed symbol code blocks only** (functions, classes, methods, types, variables). It does **NOT** search file-level code outside symbols such as:
- Decorators (e.g., `@app.route` in Flask)
- Top-level statements
- Module docstrings
- Configuration dictionaries
- Import statements

**For file-level patterns, read the file directly using the `Read` tool.**

| Param | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `pattern` | string | yes | — | Regex pattern (Go RE2 syntax) to search for in code blocks |
| `file_path_prefix` | string | no | _(all)_ | Filter to symbols in files starting with this prefix |
| `kind` | string | no | _(all)_ | Filter: `function`, `class`, `interface`, `type`, `variable`, `method` |
| `limit` | number | no | `50` | Max results (max `200`) |

```json
{
  "results": [
    {
      "symbol_name": "ErrNotFound",
      "qualified_name": "store.ErrNotFound",
      "kind": "variable",
      "file_path": "internal/store/errors.go",
      "start_line": 12,
      "end_line": 12,
      "match_snippet": "var ErrNotFound = errors.New(\"not found\")",
      "match_indices": [18, 40]
    }
  ],
  "result_count": 1,
  "pattern": "errors\\.New\\(",
  "metadata": { "timing_ms": 8, "tokens_saved_est": 4200 }
}
```

**Example patterns:**
- Find error sentinels: `errors\\.New\\(`
- Find retry logic: `retry.*(?:backoff|timeout)`
- Find SQL queries: `SELECT.*FROM`
- Find unsafe string concatenation: `\\+.*\\+`

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

## Limitations & Known Issues

While `context-link` dramatically reduces token consumption for symbol-based queries, it has specific limitations you should understand to use it effectively.

### Symbol-Centric Index Architecture

**What's Indexed:**
- Functions, classes, methods, interfaces, types, variables **with AST nodes**
- Code blocks that Tree-sitter identifies as named declarations
- Dependencies between these symbols (call graph)

**What's NOT Indexed:**
- **File-level code outside symbols** (top-level statements in Python `__main__`, Go init blocks)
- **Import/require statements** (not considered symbols)
- **Decorators on classes** (e.g., `@app.route('/api/users')` in Flask)
- **Module docstrings** (top-level documentation)
- **Configuration dictionaries** at file scope (e.g., `SETTINGS = {...}` in Python)
- **Inline comments** (only code blocks are indexed)

**Impact:** Tools like `search_code_patterns` only search **indexed symbol code blocks**. If you need to find decorators, imports, or top-level configuration, fall back to reading the file directly with the `Read` tool.

**Example Scenario:**
```python
# This Flask route decorator is NOT indexed:
@app.route('/api/users', methods=['GET'])
def get_users():  # ← This function IS indexed
    return jsonify(users)
```

Searching for `@app.route` patterns will return zero results. Read `routes.py` directly to find decorator patterns.

### Batch Parameters & Polymorphic Types

The batch-enabled tools (`get_file_skeleton`, `get_code_by_symbol`) accept **either a single string OR an array of strings**:

```json
// Single file (backward compatible)
{ "file_path": "internal/store/symbols.go" }

// Multiple files (batch operation)
{ "file_path": ["internal/store/symbols.go", "internal/store/files.go"] }
```

**Automatic JSON Array Parsing:** If you accidentally pass a JSON-serialized array as a string (e.g., `"[\"file1.go\", \"file2.go\"]"`), the tool will automatically detect and parse it. This prevents confusing "not indexed" errors when parameters are incorrectly formatted.

**Batch Limits:**
- Max 50 files per `get_file_skeleton` call
- Max 50 symbols per `get_code_by_symbol` call
- Per-item error handling: partial failures don't abort the entire batch

### Tool Independence (No Compound Queries)

Each MCP tool call is **independent**—there are no compound queries like "find all callers of X that don't check the error return value."

**Workaround:** Chain tools manually:
1. `search_code_patterns` to find error-returning functions
2. `get_symbol_usages` to find callers
3. `get_code_by_symbol` to inspect each caller
4. Manually verify error handling logic

**Example:** To find functions that call `store.GetUser()` but don't use `errors.Is()`:
```
1. search_code_patterns(pattern: "store\\.GetUser\\(")  → 12 callers
2. get_code_by_symbol(symbol_name: each caller)       → inspect code
3. Manually grep each code block for "errors.Is"
```

This is inherently token-expensive but still cheaper than reading all files.

### No Negative Queries

You **cannot** search for the **absence** of a pattern. Queries like "functions that DON'T call errors.Is" are not supported.

**Workaround:**
1. Get the full list of target symbols (`semantic_search_symbols`)
2. Search for the positive pattern (`search_code_patterns`)
3. Manually compute the set difference

**Trust the result count:** If `search_code_patterns` returns `result_count: 2`, you can trust that only 2 symbols match. There's no hidden "maybe more" ambiguity.

### search_code_patterns Scope Limitation

**Tool:** `search_code_patterns`

**What it searches:** The `code_block` column of the `symbols` table—only indexed symbol bodies.

**What it CANNOT find:**
- Decorators (Flask `@app.route`, FastAPI `@app.get`)
- Import statements (`from typing import Optional`)
- Top-level variable assignments outside functions (e.g., `logger = logging.getLogger()` in Python)
- Main entrypoint code (`if __name__ == "__main__":` blocks)

**The tool description explicitly warns about this limitation** to prevent confusion when searches return zero results.

**When to fall back to direct file reads:**
- Searching for import patterns: Use `Read` + manual grep
- Finding decorator patterns: Use `find_http_routes` (specialized) or `Read`
- Analyzing main entrypoints: Read `main.py`, `main.go`, etc. directly

### Performance Characteristics

**Fast operations (<10ms):**
- `semantic_search_symbols` (in-memory vector cache, 0.2ms average)
- `get_file_skeleton` (signature extraction only)
- `reindex_project` (incremental, no file changes)

**Moderate operations (10–100ms):**
- `get_code_by_symbol` with `depth=1` (BFS dependency resolution)
- `search_code_patterns` with broad regex (SQL LIKE prefiltering helps)

**Slow operations (>100ms):**
- `get_blast_radius` with `depth=5` (BFS through large call graphs)
- `reindex_project` after modifying 50+ files (re-parses all changed files)

**Tip:** Use `get_file_skeleton` before `get_code_by_symbol` to understand file structure first. Avoids guessing symbol names.

### Known Edge Cases

1. **Overloaded symbols:** If multiple symbols share the same name (e.g., `validate` in different classes), `get_code_by_symbol(symbol_name: "validate")` returns the **first match** by insertion order. Use **qualified names** (`ClassName.validate`) for disambiguation.

2. **Stale embeddings:** If you switch between Model2Vec (128-dim) and ONNX (384-dim) embeddings, you **must** run `index --force` to regenerate all embeddings. Dimension mismatch causes search to fail silently.

3. **Case sensitivity:** Symbol names are **case-sensitive**. `GetUser` and `getUser` are distinct. Use `semantic_search_symbols` with natural language queries if unsure of exact casing.

4. **Multi-repo namespacing:** When indexing multiple repos into one database (`--repo-name`), always specify `repo_name` in queries. Omitting it searches across all repos, which may return unexpected matches.

### Best Practices for Token Efficiency

**Built-in Observability:**
Every tool response includes real-time token savings metrics in the `metadata` field:
```json
{
  "metadata": {
    "timing_ms": 18,
    "tokens_saved_est": 4200,
    "cost_avoided_est": "$0.05",
    "session_tokens_saved": 12500,
    "session_cost_avoided": "$0.15"
  }
}
```

Use these metrics to **verify actual savings during your workflow**. If `tokens_saved_est` is low or negative, you may be using the wrong tool for the task (e.g., searching for file-level patterns with `search_code_patterns` instead of `Read`).

**DO:**
- ✅ Use `semantic_search_symbols` for discovery (metadata only, ~388 tokens for 10 results)
- ✅ Use `get_file_skeleton` to understand file structure (signatures only, <200 tokens)
- ✅ Batch operations when inspecting multiple symbols (`get_code_by_symbol` with arrays)
- ✅ Trust `result_count` fields—if a search returns 2 matches, there are exactly 2 matches
- ✅ Check `memories` arrays for prior findings before re-analyzing code
- ✅ **Monitor `tokens_saved_est` in responses—it's your efficiency compass**

**DON'T:**
- ❌ Read entire files for symbol lookup (defeats the purpose)
- ❌ Search for file-level patterns with `search_code_patterns` (use `Read` instead)
- ❌ Expect compound queries ("callers that don't X")—chain tools manually
- ❌ Re-index unnecessarily—incremental updates are fast, `--force` is slow
- ❌ **Ignore low or negative `tokens_saved_est`—it signals you're using the wrong approach**

**Token Savings Rule of Thumb:**
- Single symbol lookup: **91% reduction** vs. reading full file
- Symbol + dependencies (depth=1): **86% reduction**
- Aggregate discovery (search → skeleton → extract): **80–85% reduction**

For a 70-80% token reduction across a real-world audit task (as reported in user feedback), the key is **using the right tool for the job**: semantic search for discovery, skeleton for structure, code extraction for implementation, and direct file reads **only** for file-level patterns.

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
