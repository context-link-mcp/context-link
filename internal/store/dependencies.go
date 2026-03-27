package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// InsertDependency inserts a single dependency edge. Ignores duplicates.
func InsertDependency(ctx context.Context, db *DB, dep *models.Dependency) error {
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO dependencies (caller_id, callee_id, kind)
		 VALUES (?, ?, ?)`,
		dep.CallerID, dep.CalleeID, dep.Kind,
	)
	if err != nil {
		return fmt.Errorf("store: failed to insert dependency: %w", err)
	}
	return nil
}

// BatchInsertDependencies inserts multiple dependency edges in a transaction.
func BatchInsertDependencies(ctx context.Context, db *DB, deps []models.Dependency) error {
	if len(deps) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO dependencies (caller_id, callee_id, kind)
		 VALUES (?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("store: failed to prepare dependency insert: %w", err)
	}
	defer stmt.Close()

	for _, d := range deps {
		if _, err := stmt.ExecContext(ctx, d.CallerID, d.CalleeID, d.Kind); err != nil {
			return fmt.Errorf("store: failed to insert dependency %d->%d: %w", d.CallerID, d.CalleeID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: failed to commit dependency batch: %w", err)
	}
	return nil
}

// GetDependenciesByCaller returns all dependency edges where the given symbol
// is the caller.
func GetDependenciesByCaller(ctx context.Context, db *DB, callerID int64) ([]models.Dependency, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, caller_id, callee_id, kind FROM dependencies WHERE caller_id = ?`,
		callerID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get dependencies for caller %d: %w", callerID, err)
	}
	defer rows.Close()

	return scanDependencies(rows)
}

// GetDependenciesByCallee returns all dependency edges where the given symbol
// is the callee (i.e., who calls this symbol).
func GetDependenciesByCallee(ctx context.Context, db *DB, calleeID int64) ([]models.Dependency, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, caller_id, callee_id, kind FROM dependencies WHERE callee_id = ?`,
		calleeID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get reverse dependencies for callee %d: %w", calleeID, err)
	}
	defer rows.Close()

	return scanDependencies(rows)
}

// DeleteDependenciesByCallerFile removes all dependency edges originating
// from symbols in a specific file. Used during re-indexing.
func DeleteDependenciesByCallerFile(ctx context.Context, db *DB, repoName, filePath string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM dependencies WHERE caller_id IN (
		   SELECT id FROM symbols WHERE repo_name = ? AND file_path = ?
		 )`,
		repoName, filePath,
	)
	if err != nil {
		return fmt.Errorf("store: failed to delete dependencies for file %s/%s: %w", repoName, filePath, err)
	}
	return nil
}

// GetCalleeNamesForSymbols returns a map of caller ID → []callee name for all given caller IDs.
// Used during keyword enrichment to add function call names to embedding and FTS5 data.
// Single batch query with IN clause for efficiency during bulk indexing.
func GetCalleeNamesForSymbols(ctx context.Context, db *DB, callerIDs []int64) (map[int64][]string, error) {
	if len(callerIDs) == 0 {
		return make(map[int64][]string), nil
	}

	// Build parameterized IN clause.
	placeholders := make([]string, len(callerIDs))
	args := make([]any, len(callerIDs))
	for i, id := range callerIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT d.caller_id, s.name
		 FROM dependencies d
		 JOIN symbols s ON s.id = d.callee_id
		 WHERE d.caller_id IN (%s)`,
		strings.Join(placeholders, ","),
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get callee names for symbols: %w", err)
	}
	defer rows.Close()

	// Group results by caller ID.
	result := make(map[int64][]string)
	for rows.Next() {
		var callerID int64
		var calleeName string
		if err := rows.Scan(&callerID, &calleeName); err != nil {
			return nil, fmt.Errorf("store: failed to scan callee name row: %w", err)
		}
		result[callerID] = append(result[callerID], calleeName)
	}

	return result, rows.Err()
}

// scanDependencies scans multiple dependency rows.
func scanDependencies(rows interface{ Next() bool; Scan(...any) error; Err() error }) ([]models.Dependency, error) {
	var deps []models.Dependency
	for rows.Next() {
		var d models.Dependency
		if err := rows.Scan(&d.ID, &d.CallerID, &d.CalleeID, &d.Kind); err != nil {
			return nil, fmt.Errorf("store: failed to scan dependency row: %w", err)
		}
		deps = append(deps, d)
	}
	return deps, rows.Err()
}
