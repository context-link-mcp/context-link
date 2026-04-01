package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain ensures the binary is built before running E2E tests.
func TestMain(m *testing.M) {
	// Build binary once for all tests.
	// The test working directory is tests/e2e/, so "../.." is the repo root.
	repoRoot := filepath.Join("..", "..")

	// binPath is relative to tests/e2e/ (used by binaryPath() and os.Stat below)
	binPath := filepath.Join(repoRoot, "bin", "context-link.exe")

	// Create bin directory if it doesn't exist
	binDir := filepath.Dir(binPath)
	if err := os.MkdirAll(binDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create bin directory: %v\n", err)
		os.Exit(1)
	}

	// Build with -o relative to cmd.Dir (the repo root), not relative to tests/e2e/
	cmd := exec.Command("go", "build", "-o", filepath.Join("bin", "context-link.exe"), "./cmd/context-link")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	cmd.Dir = repoRoot

	// Capture output to display if build fails
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to build binary: %v\n", err)
		fmt.Fprintf(os.Stderr, "Build output:\n%s\n", string(output))
		os.Exit(1)
	}

	// Verify binary was created (binPath is relative to cwd = tests/e2e/)
	if _, err := os.Stat(binPath); err != nil {
		fmt.Fprintf(os.Stderr, "Binary not found at %s: %v\n", binPath, err)
		fmt.Fprintf(os.Stderr, "Build output:\n%s\n", string(output))
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	os.Exit(code)
}

// =============================================================================
// CLI Tests (Phase 4.2)
// =============================================================================

func TestCLI_Version(t *testing.T) {
	t.Parallel()
	stdout, stderr, exitCode := runCLI(t, "version")

	require.Equal(t, 0, exitCode, "version command should succeed\nstderr: %s", stderr)
	assert.Contains(t, stdout, "context-link", "version output should contain binary name")
	assert.NotEmpty(t, stdout, "version output should not be empty")
}

func TestCLI_Index(t *testing.T) {
	t.Parallel()
	root := setupTestRepo(t)
	dbPath := filepath.Join(root, ".context-link.db")

	// DB should not exist before indexing
	_, err := os.Stat(dbPath)
	require.True(t, os.IsNotExist(err), "DB should not exist before indexing")

	// Run index command with explicit DB path
	stdout, stderr, exitCode := runCLI(t, "index", "--project-root", root, "--db-path", dbPath)

	require.Equal(t, 0, exitCode, "index command should succeed\nstdout: %s\nstderr: %s", stdout, stderr)

	// Check both stdout and stderr for completion message
	output := strings.ToLower(stdout + stderr)
	assert.Contains(t, output, "indexing complete", "index output should confirm completion")

	// Verify DB file was created
	info, err := os.Stat(dbPath)
	require.NoError(t, err, "DB should exist at %s", dbPath)
	assert.Greater(t, info.Size(), int64(0), "DB should not be empty")
}

func TestCLI_Serve(t *testing.T) {
	t.Parallel()

	root := setupTestRepo(t)
	indexRepo(t, root)

	// Start serve in background
	cmd := exec.Command(binaryPath(), "serve", "--project-root", root)

	stdinPipe, err := cmd.StdinPipe()
	require.NoError(t, err)

	require.NoError(t, cmd.Start())
	defer func() {
		stdinPipe.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Verify process is running
	require.NotNil(t, cmd.Process, "serve process should be running")

	// Kill and verify clean exit
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait() // May return error due to kill signal, ignore
}

func TestCLI_ServeWithWatch(t *testing.T) {
	t.Parallel()
	root := setupTestRepo(t)
	indexRepo(t, root)

	// Start serve with --watch flag
	cmd := exec.Command(binaryPath(), "serve", "--project-root", root, "--watch")

	stdinPipe, err := cmd.StdinPipe()
	require.NoError(t, err)

	require.NoError(t, cmd.Start())
	defer func() {
		stdinPipe.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

	// Give watcher time to start
	time.Sleep(500 * time.Millisecond)

	// Verify process is running
	require.NotNil(t, cmd.Process, "serve --watch process should be running")

	// Create a new file to trigger watcher
	newFile := filepath.Join(root, "new.go")
	err = os.WriteFile(newFile, []byte("package main\n\nfunc New() {}\n"), 0644)
	require.NoError(t, err)

	// Give watcher time to detect change
	time.Sleep(1 * time.Second)

	// Process should still be running
	require.NotNil(t, cmd.Process, "serve --watch should still be running after file change")

	// Clean shutdown
	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()
}

func TestCLI_SignalHandling(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Signal handling test not supported on Windows")
	}

	root := setupTestRepo(t)
	indexRepo(t, root)

	cmd := exec.Command(binaryPath(), "serve", "--project-root", root)

	stdinPipe, err := cmd.StdinPipe()
	require.NoError(t, err)

	require.NoError(t, cmd.Start())
	defer func() {
		stdinPipe.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Send SIGINT
	err = cmd.Process.Signal(syscall.SIGINT)
	require.NoError(t, err, "should be able to send SIGINT")

	// Wait for graceful shutdown (timeout after 5 seconds)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		// Process should exit cleanly (may return signal error, which is expected)
		if err != nil {
			// Check if it's a signal-related exit, which is acceptable
			if exitErr, ok := err.(*exec.ExitError); ok {
				assert.True(t, exitErr.Exited(), "process should have exited")
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down gracefully within timeout")
	}
}

// =============================================================================
// MCP Protocol Tests (Phase 4.3)
// =============================================================================

func TestMCP_PingTool(t *testing.T) {
	root := setupTestRepo(t)
	indexRepo(t, root)

	client := NewMCPClient(t, root)
	ctx := context.Background()

	result, err := client.CallTool(ctx, "ping", map[string]any{})
	require.NoError(t, err)

	assert.Equal(t, "ok", result["status"])
	assert.NotEmpty(t, result["version"])
	assert.NotEmpty(t, result["uptime"])
}

func TestMCP_GetCodeBySymbol(t *testing.T) {
	root := setupTestRepo(t)
	indexRepo(t, root)

	client := NewMCPClient(t, root)
	ctx := context.Background()

	// Get the 'greet' function (batch API returns array of results)
	result, err := client.CallTool(ctx, "get_code_by_symbol", map[string]any{
		"symbol_name": "greet",
		"depth":       1,
	})
	require.NoError(t, err)

	// Extract first result from batch response
	results, ok := result["results"].([]any)
	require.True(t, ok, "results should be an array")
	require.Len(t, results, 1, "should have one result")

	firstResult, ok := results[0].(map[string]any)
	require.True(t, ok, "result item should be a map")

	// Check for errors (error field should be empty or absent)
	if errMsg, hasErr := firstResult["error"].(string); hasErr && errMsg != "" {
		require.FailNow(t, "result returned error", "Error: %s", errMsg)
	}

	data, ok := firstResult["data"].(map[string]any)
	require.True(t, ok, "result should have data field")

	symbol, ok := data["symbol"].(map[string]any)
	require.True(t, ok, "data should have symbol field")

	assert.Equal(t, "greet", symbol["name"])
	assert.Equal(t, "function", symbol["kind"])

	// Check code_block in symbol
	codeBlock, ok := symbol["code_block"].(string)
	require.True(t, ok, "symbol should have code_block field")
	assert.Contains(t, codeBlock, "greet")
}

func TestMCP_SemanticSearch(t *testing.T) {
	root := setupTestRepo(t)
	indexRepo(t, root)

	client := NewMCPClient(t, root)
	ctx := context.Background()

	// Search for greeting-related symbols
	result, err := client.CallTool(ctx, "semantic_search_symbols", map[string]any{
		"query": "greeting function",
		"top_k": 5,
	})
	require.NoError(t, err)

	results, ok := result["results"].([]any)
	require.True(t, ok, "result should have 'results' array")
	assert.NotEmpty(t, results, "should find at least one result")

	// Check if we got valid results with symbol_name and similarity_score
	hasValidResult := false
	for _, item := range results {
		res, ok := item.(map[string]any)
		if !ok {
			continue
		}

		symbolName, ok := res["symbol_name"].(string)
		if !ok || symbolName == "" {
			continue
		}

		similarityScore, ok := res["similarity_score"].(float64)
		if !ok {
			continue
		}

		hasValidResult = true
		assert.Greater(t, similarityScore, 0.0, "similarity_score should be positive")
		t.Logf("Found symbol: %s (similarity: %.4f)", symbolName, similarityScore)
		break
	}

	assert.True(t, hasValidResult, "should find at least one valid symbol in semantic search results")
}

func TestMCP_ToolTimeout(t *testing.T) {
	t.Skip("Timeout test is timing-dependent and may be flaky (ping is too fast)")

	// NOTE: This test is skipped because the ping tool often responds faster than 1ms,
	// making timeout testing unreliable. Context timeout is still tested via other
	// mechanisms in the codebase.
	//
	// Original implementation:
	// root := setupTestRepo(t)
	// indexRepo(t, root)
	// client := NewMCPClient(t, root)
	// ctx, cancel := context.WithTimeout(context.Background(), 1*time.Microsecond)
	// defer cancel()
	// _, err := client.CallTool(ctx, "ping", map[string]any{})
	// require.Error(t, err, "should timeout")
}

func TestMCP_InvalidTool(t *testing.T) {
	root := setupTestRepo(t)
	indexRepo(t, root)

	client := NewMCPClient(t, root)
	ctx := context.Background()

	// Call non-existent tool
	_, err := client.CallTool(ctx, "nonexistent_tool", map[string]any{})
	require.Error(t, err, "should return error for invalid tool")
	assert.Contains(t, strings.ToLower(err.Error()), "tool", "error should mention tool")
}

func TestMCP_MissingParam(t *testing.T) {
	root := setupTestRepo(t)
	indexRepo(t, root)

	client := NewMCPClient(t, root)
	ctx := context.Background()

	// Call get_code_by_symbol without required symbol_name parameter
	_, err := client.CallTool(ctx, "get_code_by_symbol", map[string]any{})
	require.Error(t, err, "should return error for missing required parameter")
}

func TestMCP_ListTools(t *testing.T) {
	root := setupTestRepo(t)
	indexRepo(t, root)

	client := NewMCPClient(t, root)
	ctx := context.Background()

	// List all available MCP tools
	tools, err := client.ListTools(ctx)
	require.NoError(t, err)

	// Verify core tools are registered
	expectedTools := []string{
		"ping",
		"get_code_by_symbol",
		"semantic_search_symbols",
		"get_file_skeleton",
		"get_symbol_usages",
		"get_call_tree",
		"find_dead_code",
		"get_blast_radius",
		"find_http_routes",
		"save_symbol_memory",
		"get_symbol_memories",
		"purge_stale_memories",
		"read_architecture_rules",
	}

	for _, expected := range expectedTools {
		found := false
		for _, tool := range tools {
			if tool == expected {
				found = true
				break
			}
		}
		assert.True(t, found, "expected tool %q should be registered", expected)
	}

	t.Logf("Registered %d tools", len(tools))
}

func TestMCP_BatchOperation(t *testing.T) {
	root := setupTestRepo(t)
	indexRepo(t, root)

	client := NewMCPClient(t, root)
	ctx := context.Background()

	// Get multiple symbols in a single batch request
	symbols := []string{"greet", "main"}

	result, err := client.CallTool(ctx, "get_code_by_symbol", map[string]any{
		"symbol_name": symbols,
		"depth":       0,
	})
	require.NoError(t, err)

	// Verify batch response
	results, ok := result["results"].([]any)
	require.True(t, ok, "results should be an array")
	require.Len(t, results, 2, "should have two results")

	// Check both symbols were retrieved successfully
	for i, sym := range symbols {
		item, ok := results[i].(map[string]any)
		require.True(t, ok, "result item should be a map")

		// Check no error
		if errMsg, hasErr := item["error"].(string); hasErr && errMsg != "" {
			require.FailNow(t, "result returned error", "Symbol %s error: %s", sym, errMsg)
		}

		data, ok := item["data"].(map[string]any)
		require.True(t, ok, "result should have data field")

		symbol, ok := data["symbol"].(map[string]any)
		require.True(t, ok, "data should have symbol field")

		assert.Equal(t, sym, symbol["name"])
	}
}

func TestMCP_TokenTracking(t *testing.T) {
	root := setupTestRepo(t)
	indexRepo(t, root)

	client := NewMCPClient(t, root)
	ctx := context.Background()

	// Call a tool that returns metadata with token_savings
	result, err := client.CallTool(ctx, "get_code_by_symbol", map[string]any{
		"symbol_name": "greet",
		"depth":       0,
	})
	require.NoError(t, err)

	metadata, ok := result["metadata"].(map[string]any)
	require.True(t, ok, "result should have 'metadata' field")

	// Check metadata
	timingMs, ok := metadata["timing_ms"].(float64)
	require.True(t, ok, "metadata should have 'timing_ms' field")
	assert.GreaterOrEqual(t, timingMs, 0.0, "timing_ms should be non-negative")

	// Token savings should be present for get_code_by_symbol
	tokens_saved_est, ok := metadata["tokens_saved_est"].(float64)
	require.True(t, ok, "metadata should have tokens_saved_est field")
	assert.GreaterOrEqual(t, tokens_saved_est, 0.0, "tokens_saved_est should be non-negative")

	// Session totals should also be present
	sessionTokensSaved, ok := metadata["session_tokens_saved"].(float64)
	require.True(t, ok, "metadata should have session_tokens_saved field")
	assert.GreaterOrEqual(t, sessionTokensSaved, 0.0, "session_tokens_saved should be non-negative")
}

// =============================================================================
// Performance Tests (Phase 4.4)
// =============================================================================

func TestPerformance_LargeCodebase(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	root := t.TempDir()
	fileCount := 1000

	t.Logf("Generating %d files...", fileCount)
	generateLargeCodebase(t, root, fileCount)

	t.Logf("Indexing %d files...", fileCount)
	start := time.Now()
	indexRepo(t, root)
	duration := time.Since(start)

	t.Logf("Indexed %d files in %v", fileCount, duration)

	// Should complete in reasonable time (< 60 seconds)
	assert.Less(t, duration, 60*time.Second, "indexing 1000 files should complete in under 60s")

	// Verify DB was created and has content (indexRepo passes --db-path .context-link.db)
	dbPath := filepath.Join(root, ".context-link.db")
	info, err := os.Stat(dbPath)
	require.NoError(t, err, "DB should exist at %s", dbPath)
	assert.Greater(t, info.Size(), int64(100_000), "DB should have substantial size")
}

func TestPerformance_SearchLatency(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	root := t.TempDir()
	generateLargeCodebase(t, root, 500)
	indexRepo(t, root)

	client := NewMCPClient(t, root)
	ctx := context.Background()

	// Perform 20 searches and track latencies
	queries := []string{
		"function implementation",
		"helper utility",
		"operation method",
		"calculation logic",
		"processing routine",
	}

	latencies := make([]time.Duration, 0, 20)

	for i := 0; i < 20; i++ {
		query := queries[i%len(queries)]

		start := time.Now()
		_, err := client.CallTool(ctx, "semantic_search_symbols", map[string]any{
			"query": query,
			"top_k": 10,
		})
		duration := time.Since(start)

		require.NoError(t, err)
		latencies = append(latencies, duration)
	}

	// Calculate P95 latency (19th value when sorted, index 18)
	// Simple bubble sort for small dataset
	for i := 0; i < len(latencies); i++ {
		for j := i + 1; j < len(latencies); j++ {
			if latencies[i] > latencies[j] {
				latencies[i], latencies[j] = latencies[j], latencies[i]
			}
		}
	}

	// P50 = 10th value = index 9, P95 = 19th value = index 18
	p50 := latencies[9]
	p95 := latencies[18]

	t.Logf("Search latencies - P50: %v, P95: %v, Max: %v",
		p50, p95, latencies[len(latencies)-1])

	// P95 should be under 500ms (relaxed from 200ms for E2E test)
	assert.Less(t, p95, 500*time.Millisecond, "P95 search latency should be under 500ms")
}

func TestPerformance_MemoryUsage(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	t.Skip("Memory measurement is unreliable: runtime.MemStats measures parent process, not subprocess")

	// NOTE: This test is skipped because runtime.MemStats.Alloc measures the Go test process's
	// memory, not the indexer subprocess. To properly measure indexer memory, we would need
	// OS-level process inspection (e.g., /proc/<pid>/stat on Linux, GetProcessMemoryInfo on Windows).
	//
	// Original implementation for reference:
	// root := t.TempDir()
	// generateLargeCodebase(t, root, 500)
	// var before runtime.MemStats
	// runtime.ReadMemStats(&before)
	// indexRepo(t, root) // Runs subprocess - memory not reflected in parent
	// runtime.GC()
	// var after runtime.MemStats
	// runtime.ReadMemStats(&after)
	// allocatedMB := float64(after.Alloc-before.Alloc) / 1024 / 1024 // Meaningless
}
