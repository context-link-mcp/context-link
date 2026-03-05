// Package models defines shared types used across all packages in context-link.
package models

import "time"

// Symbol represents a parsed code symbol (function, class, interface, etc.)
// stored in the symbols table.
type Symbol struct {
	ID            int64
	RepoName      string
	Name          string
	QualifiedName string
	Kind          string // "function","class","interface","type","variable"
	FilePath      string
	ContentHash   string
	CodeBlock     string
	StartLine     int
	EndLine       int
	Language      string
	IndexedAt     time.Time
}

// Memory represents a persistent note attached to a symbol.
type Memory struct {
	ID               int64
	SymbolID         *int64 // nullable — SET NULL on symbol delete
	Note             string
	Author           string // "agent" or "developer"
	IsStale          bool
	StaleReason      string
	LastKnownSymbol  string
	LastKnownFile    string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// File represents an indexed source file tracked for incremental re-indexing.
type File struct {
	ID          int64
	RepoName    string
	Path        string
	ContentHash string
	LastIndexed time.Time
	SizeBytes   int64
}

// Dependency represents a directed edge in the symbol call/dependency graph.
type Dependency struct {
	ID       int64
	CallerID int64
	CalleeID int64
	Kind     string // "call","import","extends","implements"
}

// Section represents a parsed section of a Markdown document.
type Section struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// ToolMetadata is included in every MCP tool response for observability.
type ToolMetadata struct {
	TimingMs int64  `json:"timing_ms"`
	Source   string `json:"source,omitempty"`
}
