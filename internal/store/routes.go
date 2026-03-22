package store

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// RouteFilter controls filtering for FindRoutes.
type RouteFilter struct {
	Method   string // optional: filter by HTTP method
	Path     string // optional: filter by path pattern (substring match)
	FilePath string // optional: filter by file path
	Kind     string // optional: "definition" or "call_site"
	Limit    int    // max results (default 50)
}

// BatchInsertRoutes inserts multiple routes in a single transaction.
// Uses INSERT OR REPLACE for idempotent re-indexing.
func BatchInsertRoutes(ctx context.Context, db *DB, routes []models.Route) error {
	if len(routes) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: failed to begin route transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO routes
		 (repo_name, method, path_pattern, normalized_path, handler_symbol_id, file_path, start_line, framework, kind)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("store: failed to prepare route insert: %w", err)
	}
	defer stmt.Close()

	for _, r := range routes {
		_, err := stmt.ExecContext(ctx,
			r.RepoName, r.Method, r.PathPattern, r.NormalizedPath,
			r.HandlerSymbolID, r.FilePath, r.StartLine, r.Framework, r.Kind,
		)
		if err != nil {
			return fmt.Errorf("store: failed to insert route %s %s: %w", r.Method, r.PathPattern, err)
		}
	}

	return tx.Commit()
}

// DeleteRoutesByRepo removes all routes for a given repo.
func DeleteRoutesByRepo(ctx context.Context, db *DB, repoName string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM routes WHERE repo_name = ?`, repoName)
	if err != nil {
		return fmt.Errorf("store: failed to delete routes for %s: %w", repoName, err)
	}
	return nil
}

// FindRoutes retrieves routes matching the given filter.
func FindRoutes(ctx context.Context, db *DB, repoName string, filter RouteFilter) ([]models.Route, error) {
	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 200 {
		filter.Limit = 200
	}

	var conditions []string
	var args []any

	conditions = append(conditions, "repo_name = ?")
	args = append(args, repoName)

	if filter.Method != "" {
		conditions = append(conditions, "method = ?")
		args = append(args, strings.ToUpper(filter.Method))
	}
	if filter.Path != "" {
		conditions = append(conditions, "(path_pattern LIKE ? OR normalized_path LIKE ?)")
		p := "%" + filter.Path + "%"
		args = append(args, p, p)
	}
	if filter.FilePath != "" {
		conditions = append(conditions, "file_path = ?")
		args = append(args, filter.FilePath)
	}
	if filter.Kind != "" {
		conditions = append(conditions, "kind = ?")
		args = append(args, filter.Kind)
	}

	args = append(args, filter.Limit)

	query := fmt.Sprintf(
		`SELECT id, repo_name, method, path_pattern, normalized_path,
		        handler_symbol_id, file_path, start_line, framework, kind
		 FROM routes
		 WHERE %s
		 ORDER BY method, normalized_path
		 LIMIT ?`,
		strings.Join(conditions, " AND "),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: failed to find routes: %w", err)
	}
	defer rows.Close()

	var routes []models.Route
	for rows.Next() {
		var r models.Route
		if err := rows.Scan(
			&r.ID, &r.RepoName, &r.Method, &r.PathPattern, &r.NormalizedPath,
			&r.HandlerSymbolID, &r.FilePath, &r.StartLine, &r.Framework, &r.Kind,
		); err != nil {
			return nil, fmt.Errorf("store: failed to scan route row: %w", err)
		}
		routes = append(routes, r)
	}
	return routes, rows.Err()
}

// RouteMatch pairs a route definition with a call site that references it.
type RouteMatch struct {
	Definition models.Route `json:"definition"`
	CallSite   models.Route `json:"call_site"`
	Confidence float64      `json:"confidence"` // 0.0–1.0
}

// MatchRoutes finds route definitions that match call sites.
// Matching is based on normalized paths and HTTP methods.
func MatchRoutes(ctx context.Context, db *DB, repoName string) ([]RouteMatch, error) {
	defs, err := FindRoutes(ctx, db, repoName, RouteFilter{Kind: "definition", Limit: 200})
	if err != nil {
		return nil, err
	}
	calls, err := FindRoutes(ctx, db, repoName, RouteFilter{Kind: "call_site", Limit: 200})
	if err != nil {
		return nil, err
	}

	var matches []RouteMatch
	for _, call := range calls {
		for _, def := range defs {
			confidence := matchConfidence(def, call)
			if confidence > 0 {
				matches = append(matches, RouteMatch{
					Definition: def,
					CallSite:   call,
					Confidence: confidence,
				})
			}
		}
	}
	return matches, nil
}

// matchConfidence returns a confidence score (0.0–1.0) for how well
// a call site matches a route definition.
func matchConfidence(def, call models.Route) float64 {
	// Method must match (or one of them is wildcard "*").
	methodMatch := def.Method == call.Method || def.Method == "*" || call.Method == "*"
	if !methodMatch {
		return 0
	}

	// Exact normalized path match.
	if def.NormalizedPath == call.NormalizedPath {
		return 1.0
	}

	// Check if paths match with param placeholders.
	if pathsMatchWithParams(def.NormalizedPath, call.NormalizedPath) {
		return 0.9
	}

	// Prefix match (e.g., /api/users matches /api/users/:id).
	if strings.HasPrefix(call.NormalizedPath, def.NormalizedPath) ||
		strings.HasPrefix(def.NormalizedPath, call.NormalizedPath) {
		return 0.5
	}

	return 0
}

// pathsMatchWithParams checks if two normalized paths match when
// treating :param segments as wildcards.
func pathsMatchWithParams(a, b string) bool {
	aParts := strings.Split(strings.Trim(a, "/"), "/")
	bParts := strings.Split(strings.Trim(b, "/"), "/")

	if len(aParts) != len(bParts) {
		return false
	}

	for i := range aParts {
		if aParts[i] == bParts[i] {
			continue
		}
		if strings.HasPrefix(aParts[i], ":") || strings.HasPrefix(bParts[i], ":") {
			continue
		}
		return false
	}
	return true
}

// paramPattern matches Express-style `:id`, FastAPI-style `{id}`, and Go-style `:id`.
var paramPattern = regexp.MustCompile(`:[a-zA-Z_]\w*|\{[a-zA-Z_]\w*\}`)

// NormalizePath converts framework-specific path parameters to a canonical form.
// Express `:id` → `:param`, FastAPI `{id}` → `:param`, Gin `:id` → `:param`.
func NormalizePath(path string) string {
	return paramPattern.ReplaceAllString(path, ":param")
}
