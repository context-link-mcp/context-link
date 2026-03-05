package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMarkdownSections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected []string // section titles in order
	}{
		{
			name: "multiple sections",
			input: `# Title

Intro text.

## Overview

Some overview content.

## Design

Design details.
`,
			expected: []string{"", "Overview", "Design"},
		},
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name: "no sections only preamble",
			input: `# Title
Just a README.`,
			expected: []string{""},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sections := parseMarkdownSections(tc.input)
			require.Len(t, sections, len(tc.expected))
			for i, title := range tc.expected {
				assert.Equal(t, title, sections[i].Title)
			}
		})
	}
}

func TestReadArchitectureRulesHandler_Found(t *testing.T) {
	t.Parallel()

	// Create a temporary directory with an ARCHITECTURE.md fixture.
	dir := t.TempDir()
	content := `# Architecture

## Overview

This is the overview.

## Design Principles

Principle 1.
Principle 2.
`
	err := os.WriteFile(filepath.Join(dir, "ARCHITECTURE.md"), []byte(content), 0o600)
	require.NoError(t, err)

	handler := readArchitectureRulesHandler(dir)

	// Build a minimal CallToolRequest.
	result, err := callHandler(t, handler)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "expected success, got error")
}

func TestReadArchitectureRulesHandler_Missing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir() // empty dir — no ARCHITECTURE.md
	handler := readArchitectureRulesHandler(dir)

	result, err := callHandler(t, handler)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.IsError, "expected error result for missing file")
}
