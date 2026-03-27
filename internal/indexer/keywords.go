package indexer

import (
	"regexp"
	"strings"
)

// maxKeywords caps the number of extracted keywords per symbol.
const maxKeywords = 20

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

// ExtractBodyKeywords extracts semantic keywords from a code block body.
// These keywords enrich embedding text and FTS5 indexing for better recall
// on queries like "error handling", "retry logic", etc.
func ExtractBodyKeywords(codeBlock string) []string {
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

	if len(keywords) > maxKeywords {
		keywords = keywords[:maxKeywords]
	}
	return keywords
}
