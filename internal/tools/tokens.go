package tools

import (
	"fmt"
	"sync/atomic"
)

const (
	// bytesPerToken is the heuristic ratio for estimating tokens from byte counts.
	// Code averages ~4 bytes per token across most tokenizers.
	bytesPerToken = 4

	// defaultCostPerMTok is the default cost per million input tokens (Claude Sonnet pricing).
	defaultCostPerMTok = 3.0
)

// SessionTokenTracker accumulates token savings across a server session.
// It is safe for concurrent use from multiple goroutines.
type SessionTokenTracker struct {
	totalSaved atomic.Int64
}

// NewSessionTokenTracker creates a new session-level token savings tracker.
func NewSessionTokenTracker() *SessionTokenTracker {
	return &SessionTokenTracker{}
}

// Record adds saved tokens to the session total and returns the new cumulative total.
func (t *SessionTokenTracker) Record(saved int64) int64 {
	if saved <= 0 {
		return t.totalSaved.Load()
	}
	return t.totalSaved.Add(saved)
}

// Total returns the current cumulative token savings.
func (t *SessionTokenTracker) Total() int64 {
	return t.totalSaved.Load()
}

// EstimateTokens converts a byte count to an approximate token count.
func EstimateTokens(byteCount int) int64 {
	if byteCount <= 0 {
		return 0
	}
	return int64((byteCount + bytesPerToken - 1) / bytesPerToken)
}

// FormatCost formats a token count as a dollar cost string (e.g., "$0.14").
func FormatCost(tokens int64) string {
	cost := float64(tokens) * defaultCostPerMTok / 1_000_000
	if cost < 0.01 {
		return "$0.00"
	}
	return fmt.Sprintf("$%.2f", cost)
}

// TokenSavings holds the computed savings for a single tool call.
type TokenSavings struct {
	FileTokens     int64 // tokens the agent would read without context-link
	ResponseTokens int64 // tokens actually returned
	Saved          int64 // FileTokens - ResponseTokens (clamped to >= 0)
}

// ComputeSavings calculates token savings given file byte sizes and response byte size.
func ComputeSavings(fileBytesTotal int, responseBytesTotal int) TokenSavings {
	fileTokens := EstimateTokens(fileBytesTotal)
	respTokens := EstimateTokens(responseBytesTotal)
	saved := fileTokens - respTokens
	if saved < 0 {
		saved = 0
	}
	return TokenSavings{
		FileTokens:     fileTokens,
		ResponseTokens: respTokens,
		Saved:          saved,
	}
}
