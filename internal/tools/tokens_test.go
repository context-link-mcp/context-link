package tools

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEstimateTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		bytes int
		want  int64
	}{
		{"zero", 0, 0},
		{"negative", -1, 0},
		{"one byte", 1, 1},
		{"exactly 4 bytes", 4, 1},
		{"five bytes", 5, 2},
		{"eight bytes", 8, 2},
		{"large file 10KB", 10240, 2560},
		{"typical file 50KB", 51200, 12800},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := EstimateTokens(tt.bytes)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatCost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tokens int64
		want   string
	}{
		{"zero", 0, "$0.00"},
		{"small", 100, "$0.00"},
		{"medium", 100_000, "$0.30"},
		{"large", 1_000_000, "$3.00"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FormatCost(tt.tokens)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestComputeSavings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fileBytes int
		respBytes int
		wantSaved int64
	}{
		{"typical", 10000, 1000, 2250},
		{"no savings", 100, 200, 0},
		{"zero file", 0, 100, 0},
		{"equal", 400, 400, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := ComputeSavings(tt.fileBytes, tt.respBytes)
			assert.Equal(t, tt.wantSaved, s.Saved)
		})
	}
}

func TestSessionTokenTracker(t *testing.T) {
	t.Parallel()

	tracker := NewSessionTokenTracker()
	assert.Equal(t, int64(0), tracker.Total())

	tracker.Record(100)
	assert.Equal(t, int64(100), tracker.Total())

	tracker.Record(200)
	assert.Equal(t, int64(300), tracker.Total())

	// Negative savings should not decrease total.
	tracker.Record(-50)
	assert.Equal(t, int64(300), tracker.Total())
}

func TestSessionTokenTracker_Concurrent(t *testing.T) {
	t.Parallel()

	tracker := NewSessionTokenTracker()
	const goroutines = 100
	const perGoroutine = int64(10)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			tracker.Record(perGoroutine)
		}()
	}
	wg.Wait()

	assert.Equal(t, goroutines*perGoroutine, tracker.Total())
}
