package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseStringOrArray accepts a parameter that can be either a string or []string.
// This enables polymorphic parameters for batch operations while maintaining backward compatibility.
// Returns a slice of strings and an error if the type is invalid.
//
// Special handling: if a string looks like a JSON-serialized array (starts with "[" and ends with "]"),
// attempts to parse it as JSON. This handles cases where agents accidentally serialize arrays as strings.
func parseStringOrArray(param any) ([]string, error) {
	if param == nil {
		return nil, fmt.Errorf("parameter is nil")
	}

	// Try string first (single-value case or serialized JSON array).
	if s, ok := param.(string); ok {
		// Detect serialized JSON array: "[\"file1.go\", \"file2.go\"]"
		trimmed := strings.TrimSpace(s)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			var parsed []string
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				return parsed, nil
			}
			// If it doesn't parse as JSON array, treat as a literal file path
			// that happens to start with "[" (unlikely but safe fallback)
		}
		return []string{s}, nil
	}

	// Try []interface{} (JSON arrays unmarshal to this type).
	if arr, ok := param.([]interface{}); ok {
		result := make([]string, 0, len(arr))
		for i, item := range arr {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("array element at index %d is not a string (got %T)", i, item)
			}
			result = append(result, s)
		}
		return result, nil
	}

	// Try []string (in case it's already typed as string slice).
	if arr, ok := param.([]string); ok {
		return arr, nil
	}

	return nil, fmt.Errorf("parameter must be string or array of strings, got %T", param)
}

// batchItemResult represents one result in a batch response.
// Each item includes the input that was processed, and either data (on success) or error message (on failure).
type batchItemResult struct {
	Input any    `json:"input"`           // The input value that was processed
	Data  any    `json:"data,omitempty"`  // Result data (present on success)
	Error string `json:"error,omitempty"` // Error message (present on failure)
}

// newBatchSuccess creates a successful batch item result.
func newBatchSuccess(input any, data any) batchItemResult {
	return batchItemResult{Input: input, Data: data}
}

// newBatchError creates a failed batch item result.
func newBatchError(input any, errMsg string) batchItemResult {
	return batchItemResult{Input: input, Error: errMsg}
}

// countSuccesses returns the number of successful results in a batch.
func countSuccesses(results []batchItemResult) int {
	count := 0
	for _, r := range results {
		if r.Error == "" {
			count++
		}
	}
	return count
}

// countErrors returns the number of failed results in a batch.
func countErrors(results []batchItemResult) int {
	count := 0
	for _, r := range results {
		if r.Error != "" {
			count++
		}
	}
	return count
}
