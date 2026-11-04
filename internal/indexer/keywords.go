package indexer

import (
	"regexp"
	"sort"
	"strings"
)

// maxKeywords caps the number of extracted keywords per symbol.
const maxKeywords = 30

// errorTypeRe matches PascalCase error/exception type names.
var errorTypeRe = regexp.MustCompile(`[A-Z][a-zA-Z]*(Error|Exception|Timeout|Failure)`)

// catchTargetRe matches Python except/Ruby rescue targets and Java/C# catch types.
var catchTargetRe = regexp.MustCompile(`(?:except|catch|rescue)\s+\(?([A-Za-z_][\w.]*)`)

// behavioralPatterns are keywords indicating behavioral patterns in function bodies.
var behavioralPatterns = []string{
	"retry", "fallback", "timeout", "backoff",
	"cache", "throttle", "circuit_breaker", "rate_limit",
	"rollback", "compensate",
}

// BuildSymbolKeywords combines regex-extracted body keywords with
// structured data from the dependency graph and symbol metadata.
//
// Sources, in priority order:
//  1. Existing regex extraction (error types, catch blocks, behavioral patterns)
//  2. Type names from the symbol signature
//  3. Callee names from the dependency graph (sorted: the graph has no order)
//  4. Identifiers from variable/const initializers
//
// Returns a deduplicated, space-separated keyword string for embedding +
// FTS5. Keywords keep their priority and order of appearance, and the cap of
// maxKeywords drops the least relevant ones, never by alphabet. The result is
// deterministic for a given input.
func BuildSymbolKeywords(codeBlock, kind string, calleeNames []string) string {
	seen := make(map[string]struct{})
	keywords := make([]string, 0, maxKeywords)

	addKeyword := func(kw string) {
		if len(keywords) >= maxKeywords {
			return
		}
		lower := strings.ToLower(kw)
		if len(lower) <= 2 { // Skip single/two-char noise
			return
		}
		if _, ok := seen[lower]; ok {
			return
		}
		seen[lower] = struct{}{}
		keywords = append(keywords, lower)
	}

	// Source 1: Existing regex extraction for functions/methods.
	if kind == "function" || kind == "method" {
		for _, kw := range extractBodyPatterns(codeBlock) {
			addKeyword(kw)
		}
	}

	// Source 2: Type names from signature.
	for _, typeName := range extractTypeNames(codeBlock) {
		addKeyword(typeName)
	}

	// Source 3: Callee names from dependency graph.
	callees := append([]string(nil), calleeNames...)
	sort.Strings(callees)
	for _, callee := range callees {
		addKeyword(callee)
	}

	// Source 4: Variable/const initializers.
	if kind == "variable" || kind == "constant" {
		for _, kw := range extractInitializerKeywords(codeBlock) {
			addKeyword(kw)
		}
	}

	return strings.Join(keywords, " ")
}

// extractBodyPatterns extracts semantic keywords from function/method bodies (old logic).
func extractBodyPatterns(codeBlock string) []string {
	seen := make(map[string]struct{})
	var keywords []string

	addKeyword := func(kw string) {
		lower := strings.ToLower(kw)
		if _, ok := seen[lower]; ok {
			return
		}
		seen[lower] = struct{}{}
		keywords = append(keywords, lower)
	}

	// Extract PascalCase error/exception types.
	for _, match := range errorTypeRe.FindAllString(codeBlock, -1) {
		addKeyword(match)
	}

	// Extract catch/except/rescue targets.
	for _, match := range catchTargetRe.FindAllStringSubmatch(codeBlock, -1) {
		if len(match) > 1 {
			addKeyword(match[1])
		}
	}

	// Check for behavioral pattern keywords.
	lowerBlock := strings.ToLower(codeBlock)
	for _, pattern := range behavioralPatterns {
		if strings.Contains(lowerBlock, pattern) {
			addKeyword(pattern)
		}
	}

	return keywords
}

// extractTypeNames pulls type identifiers from a function signature.
// Handles common patterns across languages (Go, Python, TS, Java).
func extractTypeNames(codeBlock string) []string {
	// Take the first line (signature) only.
	firstLine := codeBlock
	if idx := strings.Index(codeBlock, "\n"); idx != -1 {
		firstLine = codeBlock[:idx]
	}

	// Extract PascalCase/TitleCase identifiers that look like type names.
	typePattern := regexp.MustCompile(`\b[A-Z][a-zA-Z0-9]+\b`)
	noiseWords := map[string]bool{
		"Func": true, "True": true, "False": true, "None": true,
		"String": true, "Int": true, "Bool": true, "Float": true,
	}

	var types []string
	for _, match := range typePattern.FindAllString(firstLine, -1) {
		if !noiseWords[match] {
			types = append(types, match)
		}
	}
	return types
}

// extractInitializerKeywords pulls meaningful identifiers from variable/const declarations.
// Targets: error sentinels, config constants, factory calls.
func extractInitializerKeywords(codeBlock string) []string {
	// Extract all identifiers (alphanumeric sequences > 2 chars).
	identPattern := regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9_]{2,}\b`)
	langKeywords := map[string]bool{
		"var": true, "const": true, "let": true, "new": true,
		"nil": true, "null": true, "true": true, "false": true,
		"string": true, "int": true, "bool": true, "float": true,
	}

	var keywords []string
	for _, match := range identPattern.FindAllString(codeBlock, -1) {
		lower := strings.ToLower(match)
		if !langKeywords[lower] {
			keywords = append(keywords, lower)
		}
	}
	return keywords
}

// ExtractBodyKeywords is deprecated — use BuildSymbolKeywords instead.
// Kept for backward compatibility during migration.
func ExtractBodyKeywords(codeBlock string) []string {
	return extractBodyPatterns(codeBlock)
}
