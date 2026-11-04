package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// MaxTestResults caps the tests GetTestsForSymbol and GetTestsByNameHeuristic
// return.
const MaxTestResults = 100

// testDirs are directory names (lower-cased) that hold tests.
var testDirs = map[string]bool{
	"test": true, "tests": true, "__tests__": true, "spec": true, "specs": true,
	"e2e": true,
}

// IsTestPath reports whether a repo-relative path is a test file. Matching is
// by path segment and file-name convention, never by bare substring, so
// "latest.go", "contest.go" or "attestation/x.go" are not tests:
//   - a directory named test, tests, __tests__, spec, specs or e2e (any case),
//     or a .NET-style test project ("MyApp.Tests", "MyApp.UnitTests");
//   - Go "_test.go"; Python "test_*.py", "*_test.py", "conftest.py", "tests.py";
//     JS/TS "*.test.*" and "*.spec.*"; Ruby "*_spec.rb";
//   - JVM/.NET/Swift file names ending in "Test" or "Tests" with a capital T
//     ("UserServiceTest.java", "AuthTests.swift").
func IsTestPath(path string) bool {
	p := strings.ReplaceAll(path, "\\", "/")
	segments := strings.Split(p, "/")
	for _, dir := range segments[:len(segments)-1] {
		if isTestDir(strings.ToLower(dir)) {
			return true
		}
	}

	base := segments[len(segments)-1]
	lower := strings.ToLower(base)
	stem := base
	if dot := strings.IndexByte(base, '.'); dot > 0 {
		stem = base[:dot]
	}
	switch {
	case strings.HasSuffix(lower, "_test.go"),
		strings.HasSuffix(lower, ".py") && (strings.HasPrefix(lower, "test_") || strings.HasSuffix(lower, "_test.py")),
		lower == "conftest.py", lower == "tests.py",
		strings.Contains(lower, ".test."), strings.Contains(lower, ".spec."),
		strings.HasSuffix(lower, "_spec.rb"):
		return true
	}
	for _, suffix := range []string{"Test", "Tests"} {
		if len(stem) > len(suffix) && strings.HasSuffix(stem, suffix) {
			return true
		}
	}
	return false
}

// isTestDir reports whether a lower-cased directory name holds tests.
func isTestDir(dir string) bool {
	if testDirs[dir] {
		return true
	}
	dot := strings.LastIndexByte(dir, '.')
	if dot <= 0 {
		return false
	}
	last := dir[dot+1:]
	return last == "test" || last == "spec" || last == "specs" || strings.HasSuffix(last, "tests")
}

// IsTestSymbol reports whether a function or method is a test: it lives in a
// test file (IsTestPath) or sits in a "tests"/"test" module (Rust "mod tests"
// inside a source file). Names alone never qualify: "testConnection" or a Go
// "TestConnection" method outside a _test.go file is production code.
func IsTestSymbol(s models.Symbol) bool {
	if s.Kind != "function" && s.Kind != "method" {
		return false
	}
	if IsTestPath(s.FilePath) {
		return true
	}
	parts := strings.FieldsFunc(s.QualifiedName, func(r rune) bool { return r == '.' || r == ':' })
	for _, part := range parts[:max(len(parts)-1, 0)] {
		if part == "tests" || part == "test" {
			return true
		}
	}
	return false
}

// GetTestsForSymbol returns the tests (see IsTestSymbol) of repoName that call
// the symbol directly, ordered by file path and start line, at most
// MaxTestResults. truncated reports whether more exist.
func GetTestsForSymbol(ctx context.Context, db *DB, repoName string, symbolID int64) (tests []models.Symbol, truncated bool, err error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT s.id, s.repo_name, s.name, s.qualified_name, s.kind, s.file_path,
		       s.content_hash, s.code_block, s.start_line, s.end_line, s.language, s.indexed_at
		FROM dependencies d
		JOIN symbols s ON d.caller_id = s.id
		WHERE d.callee_id = ?
		  AND s.repo_name = ?
		  AND s.kind IN ('function', 'method')
		ORDER BY s.file_path, s.start_line, s.id
	`, symbolID, repoName)
	if err != nil {
		return nil, false, fmt.Errorf("store: failed to get tests for symbol: %w", err)
	}
	defer rows.Close()
	return collectTests(rows)
}

// GetTestsByNameHeuristic returns tests (see IsTestSymbol) of repoName whose
// name contains the base name of symbolName ("OrderService.ProcessOrder" →
// "ProcessOrder"), ASCII case-insensitive and with LIKE wildcards matched
// literally, ordered by file path and start line, at most MaxTestResults.
// truncated reports whether more exist. A blank base name matches nothing.
func GetTestsByNameHeuristic(ctx context.Context, db *DB, repoName, symbolName string) (tests []models.Symbol, truncated bool, err error) {
	baseName := symbolName
	if idx := strings.LastIndex(symbolName, "."); idx != -1 {
		baseName = symbolName[idx+1:]
	}
	if strings.TrimSpace(baseName) == "" {
		return []models.Symbol{}, false, nil
	}

	rows, err := db.QueryContext(ctx, `
		SELECT id, repo_name, name, qualified_name, kind, file_path,
		       content_hash, code_block, start_line, end_line, language, indexed_at
		FROM symbols
		WHERE repo_name = ?
		  AND kind IN ('function', 'method')
		  AND name LIKE ? ESCAPE '\'
		ORDER BY file_path, start_line, id
	`, repoName, "%"+EscapeLike(baseName)+"%")
	if err != nil {
		return nil, false, fmt.Errorf("store: failed to get tests by name heuristic: %w", err)
	}
	defer rows.Close()
	return collectTests(rows)
}

// collectTests keeps the test symbols of rows, up to MaxTestResults, and
// reports whether a further test was seen.
func collectTests(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]models.Symbol, bool, error) {
	tests := []models.Symbol{}
	for rows.Next() {
		var s models.Symbol
		if err := rows.Scan(
			&s.ID, &s.RepoName, &s.Name, &s.QualifiedName, &s.Kind, &s.FilePath,
			&s.ContentHash, &s.CodeBlock, &s.StartLine, &s.EndLine, &s.Language, &s.IndexedAt,
		); err != nil {
			return nil, false, fmt.Errorf("store: failed to scan test symbol: %w", err)
		}
		if !IsTestSymbol(s) {
			continue
		}
		if len(tests) == MaxTestResults {
			return tests, true, nil
		}
		tests = append(tests, s)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("store: failed to read test symbols: %w", err)
	}
	return tests, false, nil
}
