package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testBinaryPath is set by TestMain after building the binary.
var testBinaryPath string

// binaryPath returns the path to the compiled context-link binary.
func binaryPath() string {
	return testBinaryPath
}

// runCLI executes the context-link CLI with the given arguments and returns stdout, stderr, and exit code.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	cmd := exec.Command(binaryPath(), args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run CLI: %v", err)
		}
	} else {
		exitCode = 0
	}

	return stdout, stderr, exitCode
}

// setupTestRepo creates a temporary directory with a sample codebase for testing.
// Returns the repo root path.
func setupTestRepo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	// Create sample Go file
	goFile := filepath.Join(root, "main.go")
	goCode := `package main

import "fmt"

func main() {
	fmt.Println(greet("World"))
}

func greet(name string) string {
	return "Hello, " + name
}
`
	require.NoError(t, os.WriteFile(goFile, []byte(goCode), 0644))

	// Create sample TypeScript file
	tsDir := filepath.Join(root, "src")
	require.NoError(t, os.MkdirAll(tsDir, 0755))
	tsFile := filepath.Join(tsDir, "app.ts")
	tsCode := `export function add(a: number, b: number): number {
	return a + b;
}

export class Calculator {
	multiply(a: number, b: number): number {
		return a * b;
	}
}
`
	require.NoError(t, os.WriteFile(tsFile, []byte(tsCode), 0644))

	return root
}

// generateLargeCodebase creates a synthetic codebase with many files for performance testing.
func generateLargeCodebase(t *testing.T, root string, fileCount int) {
	t.Helper()

	for i := 0; i < fileCount; i++ {
		dir := filepath.Join(root, "pkg", fmt.Sprintf("mod%d", i/100))
		require.NoError(t, os.MkdirAll(dir, 0755))

		fileName := filepath.Join(dir, fmt.Sprintf("file%d.go", i))
		code := fmt.Sprintf(`package mod%d

// Function%d performs operation %d
func Function%d() int {
	return %d
}

// Helper%d is a helper function
func Helper%d(x int) int {
	return x * %d
}
`, i/100, i, i, i, i, i, i, i%10+1)

		require.NoError(t, os.WriteFile(fileName, []byte(code), 0644))
	}
}

// MCPClient is a test client for communicating with the MCP server over stdio.
//
// Note: sendRequest spawns a goroutine to read from stdout. If the context times out
// before the server responds, this goroutine will leak (blocked on ReadBytes). This is
// an acceptable limitation for E2E tests with short timeouts.
type MCPClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int
}

// NewMCPClient starts the context-link server and returns a client that can send MCP requests.
func NewMCPClient(t *testing.T, projectRoot string) *MCPClient {
	t.Helper()

	// Use the same DB path as indexRepo to ensure consistency
	dbPath := filepath.Join(projectRoot, ".context-link.db")
	t.Logf("NewMCPClient: project_root=%s db_path=%s binary=%s", projectRoot, dbPath, binaryPath())

	// Verify DB exists before starting server
	info, err := os.Stat(dbPath)
	require.NoError(t, err, "DB should exist at %s before starting serve", dbPath)
	t.Logf("NewMCPClient: DB size=%d bytes", info.Size())

	cmd := exec.Command(binaryPath(), "serve", "--project-root", projectRoot, "--db-path", dbPath)

	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)

	stdoutPipe, err := cmd.StdoutPipe()
	require.NoError(t, err)

	cmd.Stderr = &bytes.Buffer{}

	require.NoError(t, cmd.Start())

	client := &MCPClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdoutPipe),
		nextID: 1,
	}

	t.Cleanup(func() {
		stdin.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})

	// Wait for server to be ready (send initialize)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = client.Initialize(ctx)
	require.NoError(t, err, "MCP server failed to initialize")

	return client
}

// Initialize sends an initialize request to the MCP server.
func (c *MCPClient) Initialize(ctx context.Context) error {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      c.nextID,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "e2e-test-client",
				"version": "1.0.0",
			},
		},
	}
	c.nextID++

	_, err := c.sendRequest(ctx, req)
	return err
}

// CallTool invokes an MCP tool and returns the result as a JSON map.
func (c *MCPClient) CallTool(ctx context.Context, toolName string, arguments map[string]any) (map[string]any, error) {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      c.nextID,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": arguments,
		},
	}
	c.nextID++

	respBytes, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tool response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("JSON-RPC error [%d]: %s", resp.Error.Code, resp.Error.Message)
	}

	if resp.Result.IsError {
		return nil, fmt.Errorf("tool returned error: %v", resp.Result.Content)
	}

	if len(resp.Result.Content) == 0 {
		return nil, fmt.Errorf("tool returned no content")
	}

	// Parse the JSON from the text content
	var result map[string]any
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &result); err != nil {
		return nil, fmt.Errorf("failed to parse tool result JSON: %w", err)
	}

	return result, nil
}

// ListTools requests the list of available MCP tools.
func (c *MCPClient) ListTools(ctx context.Context) ([]string, error) {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      c.nextID,
		"method":  "tools/list",
		"params":  map[string]any{},
	}
	c.nextID++

	respBytes, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}

	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tools/list response: %w", err)
	}

	names := make([]string, len(resp.Result.Tools))
	for i, tool := range resp.Result.Tools {
		names[i] = tool.Name
	}

	return names, nil
}

// sendRequest sends a JSON-RPC request and reads the response.
func (c *MCPClient) sendRequest(ctx context.Context, req map[string]any) ([]byte, error) {
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Write request with newline delimiter
	if _, err := c.stdin.Write(append(reqBytes, '\n')); err != nil {
		return nil, fmt.Errorf("failed to write request: %w", err)
	}

	// Read response with timeout
	respChan := make(chan []byte, 1)
	errChan := make(chan error, 1)

	go func() {
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			errChan <- err
			return
		}
		respChan <- line
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errChan:
		return nil, fmt.Errorf("failed to read response: %w", err)
	case respBytes := <-respChan:
		return bytes.TrimSpace(respBytes), nil
	}
}

// indexRepo runs the index command on a repository and waits for completion.
func indexRepo(t *testing.T, root string) {
	t.Helper()

	dbPath := filepath.Join(root, ".context-link.db")
	stdout, stderr, exitCode := runCLI(t, "index", "--project-root", root, "--db-path", dbPath)
	require.Equal(t, 0, exitCode, "index command failed\nstdout: %s\nstderr: %s", stdout, stderr)

	// Check both stdout and stderr for "indexing complete" (logs may go to either)
	output := strings.ToLower(stdout + stderr)
	require.Contains(t, output, "indexing complete", "index output should confirm completion")

	// Log full output for CI debugging
	t.Logf("index stdout: %s", stdout)
	t.Logf("index stderr: %s", stderr)

	// Verify symbols were extracted (catches silent indexing failures).
	// The JSON log contains "symbols":N and the text summary contains "Symbols extracted:".
	require.NotContains(t, output, `"symbols":0`, "indexer should extract at least one symbol")
}

