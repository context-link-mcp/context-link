package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/context-link/context-link/internal/store"
	"github.com/context-link/context-link/internal/vectorstore"
)

// SemanticSearchResult is one item returned by the semantic_search_symbols tool.
type SemanticSearchResult struct {
	SymbolName    string  `json:"symbol_name"`
	QualifiedName string  `json:"qualified_name"`
	Kind          string  `json:"kind"`
	FilePath      string  `json:"file_path"`
	Language      string  `json:"language"`
	Similarity    float32 `json:"similarity_score"`
}

// semanticSearchResponse is the full JSON response from semantic_search_symbols.
type semanticSearchResponse struct {
	Results  []SemanticSearchResult `json:"results"`
	Metadata semanticSearchMeta     `json:"metadata"`
}

type semanticSearchMeta struct {
	TimingMs     int64 `json:"timing_ms"`
	TotalResults int   `json:"total_results"`
	Query        string `json:"query"`
}

// RegisterSemanticSearchTool registers the semantic_search_symbols MCP tool.
// If embedder is nil, the tool returns an appropriate "not available" error.
func RegisterSemanticSearchTool(s *server.MCPServer, db *store.DB, embedder vectorstore.Embedder, repoName string) {
	tool := mcp.NewTool("semantic_search_symbols",
		mcp.WithDescription(
			"Discover relevant code symbols by natural-language intent. "+
				"Returns symbol names, kinds, and file paths ranked by semantic similarity. "+
				"Does NOT return code — call get_code_by_symbol to retrieve actual source. "+
				"Example: query='authentication token validation' returns matching function/class names.",
		),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural-language description of what you are looking for. Example: 'user authentication', 'database connection pool'."),
		),
		mcp.WithNumber("top_k",
			mcp.Description("Maximum number of results to return (default: 10, max: 50)."),
		),
		mcp.WithString("kind",
			mcp.Description("Filter by symbol kind: 'function', 'class', 'interface', 'type', 'variable'. Leave empty for all kinds."),
		),
		mcp.WithString("file_path_prefix",
			mcp.Description("Filter results to symbols whose file path starts with this prefix. Example: 'src/auth/'."),
		),
		mcp.WithNumber("min_similarity",
			mcp.Description("Minimum cosine similarity threshold (0.0–1.0, default: 0.3). Higher values return fewer but more relevant results."),
		),
	)
	s.AddTool(tool, semanticSearchHandler(db, embedder, repoName))
}

// semanticSearchHandler returns the ToolHandlerFunc for semantic_search_symbols.
func semanticSearchHandler(db *store.DB, embedder vectorstore.Embedder, repoName string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		// --- Input validation ---
		query, err := req.RequireString("query")
		if err != nil || strings.TrimSpace(query) == "" {
			return mcp.NewToolResultError("semantic_search_symbols: 'query' parameter is required and must not be empty"), nil
		}

		topK := req.GetInt("top_k", 10)
		if topK <= 0 || topK > 50 {
			topK = 10
		}

		minSimilarity := float32(req.GetFloat("min_similarity", 0.3))
		if minSimilarity < 0 || minSimilarity > 1 {
			minSimilarity = 0.3
		}

		kindFilter := strings.TrimSpace(req.GetString("kind", ""))
		pathPrefix := strings.TrimSpace(req.GetString("file_path_prefix", ""))

		// --- Embedder availability check ---
		if embedder == nil {
			return mcp.NewToolResultError(
				"semantic_search_symbols: semantic search is not available — " +
					"start the server with --model-path and --vocab-path to enable it",
			), nil
		}

		// --- Generate query embedding ---
		queryVec, err := embedder.EmbedOne(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("semantic_search_symbols: failed to embed query: %v", err)), nil
		}

		// --- KNN search (fetch extra candidates for post-filter headroom) ---
		hits, err := vectorstore.KNNSearch(ctx, db, repoName, queryVec, topK*3, minSimilarity)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("semantic_search_symbols: search failed: %v", err)), nil
		}

		// --- Join with symbols table and apply optional filters ---
		var results []SemanticSearchResult
		for _, hit := range hits {
			if len(results) >= topK {
				break
			}

			row := db.QueryRowContext(ctx, `
				SELECT name, qualified_name, kind, file_path, language
				FROM symbols
				WHERE id = ? AND repo_name = ?
			`, hit.SymbolID, repoName)

			var name, qualName, kind, filePath, language string
			if err := row.Scan(&name, &qualName, &kind, &filePath, &language); err != nil {
				continue // symbol may have been deleted since embedding was stored
			}

			if kindFilter != "" && kind != kindFilter {
				continue
			}
			if pathPrefix != "" && !strings.HasPrefix(filePath, pathPrefix) {
				continue
			}

			results = append(results, SemanticSearchResult{
				SymbolName:    name,
				QualifiedName: qualName,
				Kind:          kind,
				FilePath:      filePath,
				Language:      language,
				Similarity:    hit.Similarity,
			})
		}

		resp := semanticSearchResponse{
			Results: results,
			Metadata: semanticSearchMeta{
				TimingMs:     time.Since(start).Milliseconds(),
				TotalResults: len(results),
				Query:        query,
			},
		}

		return mcp.NewToolResultJSON(resp)
	}
}
