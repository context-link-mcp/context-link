package store

import (
	"context"
	"fmt"
	"log/slog"
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
	Limit    int    // max results (default 50, at most 200)
	// Unlimited returns every matching route and ignores Limit, for callers
	// that filter the routes themselves before capping them (extract).
	Unlimited bool
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

// DeleteRoutesByFile removes the routes of the given kind ("definition" or
// "call_site") detected in a single file. Called before re-indexing a file so
// routes are replaced rather than duplicated.
func DeleteRoutesByFile(ctx context.Context, db *DB, repoName, filePath, kind string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM routes WHERE repo_name = ? AND file_path = ? AND kind = ?`,
		repoName, filePath, kind,
	)
	if err != nil {
		return fmt.Errorf("store: failed to delete routes for %s: %w", filePath, err)
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
	if filter.Unlimited {
		filter.Limit = -1 // SQLite: a negative LIMIT means no limit
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
		// Substring match; LIKE wildcards in the filter match literally.
		conditions = append(conditions, `(path_pattern LIKE ? ESCAPE '\' OR normalized_path LIKE ? ESCAPE '\')`)
		p := "%" + EscapeLike(filter.Path) + "%"
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

// matchRouteLimit caps how many definitions and how many call sites
// MatchRoutes compares (the pairing is quadratic).
const matchRouteLimit = 200

// RouteMatchResult is the result of MatchRoutesDetailed.
type RouteMatchResult struct {
	Matches []RouteMatch
	// Truncated reports that more than matchRouteLimit definitions or call
	// sites exist, so only the first of each (by method, normalized path)
	// were matched and some matches may be missing.
	Truncated bool
}

// MatchRoutes finds route definitions that match call sites.
// Matching is based on normalized paths and HTTP methods. When the route
// table exceeds the comparison cap it logs a warning; MatchRoutesDetailed
// reports that to the caller instead.
func MatchRoutes(ctx context.Context, db *DB, repoName string) ([]RouteMatch, error) {
	res, err := MatchRoutesDetailed(ctx, db, repoName)
	if err != nil {
		return nil, err
	}
	if res.Truncated {
		slog.Warn("store: route matching truncated; some definition/call-site matches are missing",
			"repo", repoName, "limit", matchRouteLimit)
	}
	return res.Matches, nil
}

// MatchRoutesDetailed is MatchRoutes that also reports whether the
// comparison cap cut off any definitions or call sites.
func MatchRoutesDetailed(ctx context.Context, db *DB, repoName string) (RouteMatchResult, error) {
	defs, err := FindRoutes(ctx, db, repoName, RouteFilter{Kind: "definition", Unlimited: true})
	if err != nil {
		return RouteMatchResult{}, err
	}
	calls, err := FindRoutes(ctx, db, repoName, RouteFilter{Kind: "call_site", Unlimited: true})
	if err != nil {
		return RouteMatchResult{}, err
	}
	var res RouteMatchResult
	if len(defs) > matchRouteLimit {
		defs, res.Truncated = defs[:matchRouteLimit], true
	}
	if len(calls) > matchRouteLimit {
		calls, res.Truncated = calls[:matchRouteLimit], true
	}

	for _, call := range calls {
		for _, def := range defs {
			confidence := matchConfidence(def, call)
			if confidence > 0 {
				res.Matches = append(res.Matches, RouteMatch{
					Definition: def,
					CallSite:   call,
					Confidence: confidence,
				})
			}
		}
	}
	return res, nil
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

	defParts, callParts := pathSegments(def.NormalizedPath), pathSegments(call.NormalizedPath)

	// Same segments once :param and * placeholders are treated as wildcards.
	if segmentsMatch(defParts, callParts) {
		return 0.9
	}

	// Segment-wise prefix match (e.g., /api/users matches /api/users/:param,
	// but /api does not match /apiv2 and the root matches nothing).
	if segmentPrefix(defParts, callParts) || segmentPrefix(callParts, defParts) {
		return 0.5
	}

	return 0
}

// pathSegments splits a normalized path into its non-empty segments, so
// "/a//b/" and "a/b" both give [a b] and "/" gives none.
func pathSegments(p string) []string {
	var parts []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return parts
}

// segmentMatches reports whether two path segments match, treating a :param
// placeholder on either side as matching any one segment.
func segmentMatches(a, b string) bool {
	return a == b || strings.HasPrefix(a, ":") || strings.HasPrefix(b, ":")
}

// segmentsMatch reports whether two segment lists match: segment by segment,
// with a trailing "*" (catch-all) on either side matching one or more
// remaining segments.
func segmentsMatch(a, b []string) bool {
	for i := 0; ; i++ {
		switch {
		case i < len(a) && a[i] == catchAll:
			return i == len(a)-1 && len(b) > i
		case i < len(b) && b[i] == catchAll:
			return i == len(b)-1 && len(a) > i
		case i == len(a) || i == len(b):
			return len(a) == len(b)
		case !segmentMatches(a[i], b[i]):
			return false
		}
	}
}

// segmentPrefix reports whether the non-empty prefix is a strict leading
// part of full, segment by segment.
func segmentPrefix(prefix, full []string) bool {
	if len(prefix) == 0 || len(prefix) >= len(full) {
		return false
	}
	for i, s := range prefix {
		if s == catchAll || full[i] == catchAll || !segmentMatches(s, full[i]) {
			return false
		}
	}
	return true
}

// catchAll is the normalized form of a parameter that spans several path
// segments (Flask <path:p>, FastAPI/Starlette {p:path}, Express "*").
const catchAll = "*"

var (
	// flaskParam matches Flask/Werkzeug <name> and <converter:name> (also
	// <converter(args):name>).
	flaskParam = regexp.MustCompile(`<(?:([a-zA-Z_]\w*)(?:\([^)]*\))?:)?[a-zA-Z_]\w*>`)
	// braceParam matches FastAPI/Starlette {name} and {name:converter}.
	braceParam = regexp.MustCompile(`\{[a-zA-Z_]\w*(?::([a-zA-Z_]\w*))?\}`)
	// colonParam matches Express/Gin :name with an optional Express regex
	// constraint (:id(\d+)) and optional/repeat modifier (:id?, :id*, :id+).
	colonParam = regexp.MustCompile(`:[a-zA-Z_]\w*(?:\([^)]*\))?([?*+])?`)
	// starParam matches a bare "*" or Express 5 "*name" wildcard segment.
	starParam = regexp.MustCompile(`(^|/)\*[a-zA-Z_]\w*|(^|/)\*($|/)`)
)

// NormalizePath converts framework-specific path parameters to a canonical
// form: a parameter matching one segment becomes ":param", one that can span
// segments becomes "*". Express/Gin ":id" (with optional "(regex)"), FastAPI
// "{id}" and Flask "<id>" / "<int:id>" give ":param"; Flask "<path:p>",
// FastAPI "{p:path}", Express "*" / "*splat" / ":p*" / ":p+" give "*".
func NormalizePath(path string) string {
	path = flaskParam.ReplaceAllStringFunc(path, func(m string) string {
		if sub := flaskParam.FindStringSubmatch(m); sub[1] == "path" {
			return catchAll
		}
		return ":param"
	})
	path = braceParam.ReplaceAllStringFunc(path, func(m string) string {
		if sub := braceParam.FindStringSubmatch(m); sub[1] == "path" {
			return catchAll
		}
		return ":param"
	})
	path = starParam.ReplaceAllString(path, "${1}${2}"+catchAll+"${3}")
	return colonParam.ReplaceAllStringFunc(path, func(m string) string {
		if sub := colonParam.FindStringSubmatch(m); sub[1] == "*" || sub[1] == "+" {
			return catchAll
		}
		return ":param"
	})
}
