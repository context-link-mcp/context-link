package tools

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/context-link-mcp/context-link/internal/store"
	"github.com/context-link-mcp/context-link/internal/vectorstore"
)

// SemanticSearchResult is one item returned by the semantic_search_symbols tool.
type SemanticSearchResult struct {
	SymbolName    string  `json:"symbol_name"`
	QualifiedName string  `json:"qualified_name"`
	Kind          string  `json:"kind"`
	FilePath      string  `json:"file_path"`
	Similarity    float32 `json:"similarity_score"`
	MatchSource   string  `json:"match_source"` // "vector", "fts", or "both"
	MemoryCount   int     `json:"memory_count,omitempty"`
}

// semanticSearchResponse is the full JSON response from semantic_search_symbols.
type semanticSearchResponse struct {
	Results  []SemanticSearchResult `json:"results"`
	Metadata semanticSearchMeta     `json:"metadata"`
}

type semanticSearchMeta struct {
	TimingMs           int64  `json:"timing_ms"`
	TotalResults       int    `json:"total_results"`
	Query              string `json:"query"`
	TokensSavedEst     int64  `json:"tokens_saved_est,omitempty"`
	CostAvoidedEst     string `json:"cost_avoided_est,omitempty"`
	SessionTokensSaved int64  `json:"session_tokens_saved,omitempty"`
	SessionCostAvoided string `json:"session_cost_avoided,omitempty"`
}

// RegisterSemanticSearchTool registers the semantic_search_symbols MCP tool.
// If embedder is nil, the tool returns an appropriate "not available" error.
// If vecCache is non-nil, KNN search uses the in-memory cache for faster queries.
func RegisterSemanticSearchTool(s *server.MCPServer, db *store.DB, embedder vectorstore.Embedder, repoName string, timeout time.Duration, tracker *SessionTokenTracker, vecCache *vectorstore.VectorCache) {
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
	s.AddTool(tool, WithTimeout(timeout, semanticSearchHandler(db, embedder, repoName, tracker, vecCache)))
}

// rrfEntry is an intermediate result during Reciprocal Rank Fusion.
type rrfEntry struct {
	symbolID    int64
	rrfScore    float64
	similarity  float32 // best vector similarity (0 if FTS-only)
	matchSource string  // "vector", "fts", or "both"
}

// rrfFuse merges ranked results from vector KNN and FTS5 using Reciprocal Rank
// Fusion (RRF) with constant k=60. Returns fused results sorted by RRF score.
func rrfFuse(vectorIDs []int64, vectorSims []float32, ftsIDs []int64, topK int) []rrfEntry {
	const k = 60.0

	scores := make(map[int64]*rrfEntry)

	for rank, id := range vectorIDs {
		e, ok := scores[id]
		if !ok {
			e = &rrfEntry{symbolID: id, matchSource: "vector"}
			scores[id] = e
		}
		e.rrfScore += 1.0 / (k + float64(rank+1))
		if rank < len(vectorSims) {
			e.similarity = vectorSims[rank]
		}
	}

	for rank, id := range ftsIDs {
		e, ok := scores[id]
		if !ok {
			e = &rrfEntry{symbolID: id, matchSource: "fts"}
			scores[id] = e
		} else {
			e.matchSource = "both"
		}
		e.rrfScore += 1.0 / (k + float64(rank+1))
	}

	result := make([]rrfEntry, 0, len(scores))
	for _, e := range scores {
		result = append(result, *e)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].rrfScore > result[j].rrfScore
	})

	if len(result) > topK {
		result = result[:topK]
	}
	return result
}

// semanticSearchHandler returns the ToolHandlerFunc for semantic_search_symbols.
func semanticSearchHandler(db *store.DB, embedder vectorstore.Embedder, repoName string, tracker *SessionTokenTracker, cache *vectorstore.VectorCache) server.ToolHandlerFunc {
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

		// --- Run KNN and FTS in parallel ---
		fetchLimit := topK * 3

		var knnHits []vectorstore.SearchResult
		var ftsHits []store.FTSResult
		var knnErr, ftsErr error

		// KNN search.
		if cache != nil {
			knnHits, knnErr = vectorstore.KNNSearchCached(ctx, db, cache, queryVec, fetchLimit, minSimilarity)
		} else {
			knnHits, knnErr = vectorstore.KNNSearch(ctx, db, repoName, queryVec, fetchLimit, minSimilarity)
		}
		if knnErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("semantic_search_symbols: search failed: %v", knnErr)), nil
		}

		// FTS search (non-fatal if it fails — degrade to vector-only).
		ftsHits, ftsErr = store.FTSSearch(ctx, db, repoName, query, fetchLimit)
		if ftsErr != nil {
			slog.Debug("semantic_search: FTS search failed, degrading to vector-only", "error", ftsErr)
			ftsHits = nil
		}

		// --- RRF fusion ---
		vectorIDs := make([]int64, len(knnHits))
		vectorSims := make([]float32, len(knnHits))
		for i, h := range knnHits {
			vectorIDs[i] = h.SymbolID
			vectorSims[i] = h.Similarity
		}
		ftsIDs := make([]int64, len(ftsHits))
		for i, h := range ftsHits {
			ftsIDs[i] = h.SymbolID
		}

		fused := rrfFuse(vectorIDs, vectorSims, ftsIDs, topK*2)

		// --- Batch-fetch symbols for all fused hits ---
		fusedIDs := make([]int64, len(fused))
		for i, f := range fused {
			fusedIDs[i] = f.symbolID
		}
		symbolMap, err := store.GetSymbolsByIDs(ctx, db, repoName, fusedIDs)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("semantic_search_symbols: failed to fetch symbols: %v", err)), nil
		}

		// Apply filters and collect results in RRF order.
		type candidate struct {
			symbolID int64
			result   SemanticSearchResult
		}
		var candidates []candidate
		for _, f := range fused {
			if len(candidates) >= topK {
				break
			}
			sym, ok := symbolMap[f.symbolID]
			if !ok {
				continue
			}
			if kindFilter != "" && sym.Kind != kindFilter {
				continue
			}
			if pathPrefix != "" && !strings.HasPrefix(sym.FilePath, pathPrefix) {
				continue
			}
			candidates = append(candidates, candidate{
				symbolID: sym.ID,
				result: SemanticSearchResult{
					SymbolName:    sym.Name,
					QualifiedName: sym.QualifiedName,
					Kind:          sym.Kind,
					FilePath:      sym.FilePath,
					Similarity:    float32(math.Round(float64(f.similarity)*100) / 100),
					MatchSource:   f.matchSource,
				},
			})
		}

		// Batch fetch memory counts.
		symbolIDs := make([]int64, len(candidates))
		for i, c := range candidates {
			symbolIDs[i] = c.symbolID
		}
		memoryCounts, _ := store.CountMemoriesBySymbolIDs(ctx, db, symbolIDs)

		var results []SemanticSearchResult
		fileSet := map[string]struct{}{}
		for _, c := range candidates {
			r := c.result
			r.MemoryCount = memoryCounts[c.symbolID]
			results = append(results, r)
			fileSet[r.FilePath] = struct{}{}
		}

		// Token savings: agent would read all matched files; we return metadata only.
		var totalFileBytes int
		for fp := range fileSet {
			if f, err := store.GetFileByPath(ctx, db, repoName, fp); err == nil {
				totalFileBytes += int(f.SizeBytes)
			}
		}
		responseBytes := len(results) * 80
		savings := ComputeSavings(totalFileBytes, responseBytes)
		sessionTotal := tracker.Record(savings.Saved)
		timingMs := time.Since(start).Milliseconds()

		resp := semanticSearchResponse{
			Results: results,
			Metadata: semanticSearchMeta{
				TimingMs:           timingMs,
				TotalResults:       len(results),
				Query:              query,
				TokensSavedEst:     savings.Saved,
				CostAvoidedEst:     FormatCost(savings.Saved),
				SessionTokensSaved: sessionTotal,
				SessionCostAvoided: FormatCost(sessionTotal),
			},
		}

		return mcp.NewToolResultJSON(resp)
	}
}
