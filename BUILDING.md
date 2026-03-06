# Building context-link

context-link uses CGo for Tree-sitter language grammars. This document covers the build prerequisites for each platform.

## Prerequisites

| Dependency | Version | Purpose |
|-----------|---------|---------|
| Go | 1.22+ | Runtime and build toolchain |
| GCC (C compiler) | Any recent | Required by `smacker/go-tree-sitter` CGo bindings |
| Git | Any | Dependency fetching |

## CGo Dependencies

The following Go packages require a C compiler via CGo:

- **`github.com/smacker/go-tree-sitter`** — Core AST parser
- **`github.com/smacker/go-tree-sitter/typescript/typescript`** — TypeScript grammar
- **`github.com/smacker/go-tree-sitter/typescript/tsx`** — TSX grammar (separate from TS)
- **`github.com/smacker/go-tree-sitter/golang`** — Go grammar

The SQLite driver (`modernc.org/sqlite`) is pure Go and does **not** require CGo.

## Platform-Specific Setup

### Windows

Install MinGW-w64 via winget:

```powershell
winget install -e --id niXman.mingw-w64-ucrt
```

After installation, restart your terminal/IDE so `gcc` is on PATH. Verify:

```powershell
gcc --version
```

### macOS

Xcode Command Line Tools include `clang` which works as the C compiler:

```bash
xcode-select --install
```

### Linux (Debian/Ubuntu)

```bash
sudo apt-get install build-essential
```

### Linux (Fedora/RHEL)

```bash
sudo dnf install gcc gcc-c++ make
```

## Building

Ensure `CGO_ENABLED=1` (this is the default when a C compiler is available):

```bash
# Development build
CGO_ENABLED=1 go build -o ./bin/context-link ./cmd/context-link

# Release build (stripped binary)
CGO_ENABLED=1 go build -ldflags="-s -w" -o ./bin/context-link ./cmd/context-link
```

On Windows, the output binary will be `context-link.exe`.

## Running Tests

```bash
CGO_ENABLED=1 go test ./... -count=1
```

Snapshot tests can be updated with:

```bash
CGO_ENABLED=1 go test ./internal/indexer/ -args -update-golden
```

## Troubleshooting

### `CGO_ENABLED=0` or "gcc not found"

Tree-sitter grammars are C libraries compiled via CGo. If `gcc` is not on PATH or `CGO_ENABLED=0` is set, the build will fail with linker errors. Ensure a C compiler is installed and accessible.

### Windows: "cc1.exe: sorry, unimplemented: 64-bit mode not compiled in"

This means a 32-bit MinGW is installed. Use the 64-bit UCRT variant:

```powershell
winget install -e --id niXman.mingw-w64-ucrt
```

### Slow first build

The first build compiles all Tree-sitter C sources. Subsequent builds use the Go build cache and are much faster.
