package store

import (
	"context"
	"fmt"

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
