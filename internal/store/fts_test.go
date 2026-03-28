package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeFTSQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple query",
			input:    "error handling",
			expected: `"error handling"`,
		},
		{
			name:     "query with parentheses",
			input:    "error (timeout)",
			expected: `"error (timeout)"`,
		},
		{
			name:     "query with double quotes",
			input:    `user's "token"`,
			expected: `"user's ""token"""`,
		},
		{
			name:     "query with AND operator",
			input:    "retry AND backoff",
			expected: `"retry AND backoff"`,
		},
		{
			name:     "query with OR operator",
			input:    "cache OR throttle",
			expected: `"cache OR throttle"`,
		},
		{
			name:     "query with NOT operator",
			input:    "error NOT timeout",
			expected: `"error NOT timeout"`,
		},
		{
			name:     "query with NEAR operator",
			input:    "user NEAR token",
			expected: `"user NEAR token"`,
		},
		{
			name:     "query with asterisk wildcard",
			input:    "error*",
			expected: `"error*"`,
		},
		{
			name:     "query with caret prefix",
			input:    "^error",
			expected: `"^error"`,
		},
		{
			name:     "query with multiple quotes",
			input:    `"hello" "world"`,
			expected: `"""hello"" ""world"""`,
		},
		{
			name:     "empty query",
			input:    "",
			expected: `""`,
		},
		{
			name:     "query with mixed special characters",
			input:    `error (timeout) AND "retry" OR backoff*`,
			expected: `"error (timeout) AND ""retry"" OR backoff*"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := sanitizeFTSQuery(tt.input)
			assert.Equal(t, tt.expected, result, "sanitizeFTSQuery(%q) should return %q", tt.input, tt.expected)
		})
	}
}
