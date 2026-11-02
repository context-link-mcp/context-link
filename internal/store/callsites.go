package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// CallSite is a raw reference extracted from a file, kept so the file's edges
// can be resolved again without re-parsing it.
type CallSite struct {
	Line int
	// Kind is "call", "extends", "implements" or "import".
	Kind string
	// Name is the referenced symbol name, or the import source for imports.
	Name string
	// Qualifier is the receiver of a method or selector call ("" for a plain
	// call, "*" for a receiver that is not a dotted name), or for an import
	// the local name it binds ("" when it binds none).
	Qualifier string
}

// maxNamesPerQuery bounds the IN list of a single query, well below SQLite's
// host-parameter limit.
const maxNamesPerQuery = 500

// insertCallSites stores sites for one file inside tx.
func insertCallSites(ctx context.Context, tx *sql.Tx, repoName, filePath string, sites []CallSite) error {
	// Multi-row inserts: a file has hundreds of call sites, and per-statement
	// overhead dominates single-row inserts.
	const rowsPerInsert = 100
	for start := 0; start < len(sites); start += rowsPerInsert {
		chunk := sites[start:min(start+rowsPerInsert, len(sites))]
		args := make([]any, 0, 6*len(chunk))
		for _, s := range chunk {
			args = append(args, repoName, filePath, s.Line, s.Kind, s.Name, s.Qualifier)
		}
		values := strings.TrimSuffix(strings.Repeat("(?, ?, ?, ?, ?, ?),", len(chunk)), ",")
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO call_sites (repo_name, file_path, line, kind, name, qualifier) VALUES `+values, args...,
		); err != nil {
			return fmt.Errorf("store: failed to insert call sites for %s/%s: %w", repoName, filePath, err)
		}
	}
	return nil
}

// GetCallSitesByFiles returns the stored call sites of the given files, path
// → sites in line order. Files without call sites are absent.
func GetCallSitesByFiles(ctx context.Context, db *DB, repoName string, paths []string) (map[string][]CallSite, error) {
	result := make(map[string][]CallSite)
	for start := 0; start < len(paths); start += maxNamesPerQuery {
		end := min(start+maxNamesPerQuery, len(paths))
		chunk := paths[start:end]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, repoName)
		for _, p := range chunk {
			args = append(args, p)
		}
		rows, err := db.QueryContext(ctx,
			`SELECT file_path, line, kind, name, qualifier FROM call_sites
			 WHERE repo_name = ? AND file_path IN (`+placeholders(len(chunk))+`)
			 ORDER BY file_path, line, id`, args...)
		if err != nil {
			return nil, fmt.Errorf("store: failed to get call sites for %s: %w", repoName, err)
		}
		for rows.Next() {
			var path string
			var s CallSite
			if err := rows.Scan(&path, &s.Line, &s.Kind, &s.Name, &s.Qualifier); err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: failed to scan call site row: %w", err)
			}
			result[path] = append(result[path], s)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: failed to read call sites for %s: %w", repoName, err)
		}
		rows.Close()
	}
	return result, nil
}

// DeleteCallSitesByFile removes the stored call sites of a file.
func DeleteCallSitesByFile(ctx context.Context, db *DB, repoName, filePath string) error {
	if _, err := db.ExecContext(ctx,
		`DELETE FROM call_sites WHERE repo_name = ? AND file_path = ?`, repoName, filePath,
	); err != nil {
		return fmt.Errorf("store: failed to delete call sites for %s/%s: %w", repoName, filePath, err)
	}
	return nil
}

// markEdgeCallers marks for re-resolution, inside tx, every other file with
// an edge into filePath: the file's symbols are about to be deleted (and get
// new IDs if re-inserted), so those callers' edges must be resolved again.
// The joins are pinned to start from the file's own symbols (CROSS JOIN fixes
// the order in SQLite); otherwise the planner scans every symbol of the repo.
func markEdgeCallers(ctx context.Context, tx *sql.Tx, repoName, filePath string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO pending_resolution (repo_name, file_path)
		 SELECT DISTINCT caller.repo_name, caller.file_path
		 FROM symbols callee
		 CROSS JOIN dependencies d ON d.callee_id = callee.id
		 CROSS JOIN symbols caller ON caller.id = d.caller_id
		 WHERE callee.repo_name = ? AND callee.file_path = ?
		   AND caller.repo_name = ? AND caller.file_path != ?`,
		repoName, filePath, repoName, filePath,
	); err != nil {
		return fmt.Errorf("store: failed to mark callers of %s/%s: %w", repoName, filePath, err)
	}
	return nil
}

// DetachFileDependencies prepares filePath for deletion: in one transaction
// it marks for re-resolution every other file with an edge into it and
// deletes every dependency edge that starts or ends at a symbol in it, so if
// the run is interrupted before Phase 5 the callers' edges are still restored
// by a later run. (ReplaceFileIndex does the same for a re-indexed file.)
// Callers by name are marked separately, see MarkNameCallers.
func DetachFileDependencies(ctx context.Context, db *DB, repoName, filePath string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if err := markEdgeCallers(ctx, tx, repoName, filePath); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM dependencies
		 WHERE caller_id IN (`+fileSymbolIDs+`)
		    OR callee_id IN (`+fileSymbolIDs+`)`,
		repoName, filePath, repoName, filePath,
	); err != nil {
		return fmt.Errorf("store: failed to delete dependencies for %s/%s: %w", repoName, filePath, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: failed to commit dependency detach for %s/%s: %w", repoName, filePath, err)
	}
	return nil
}

// MarkNameCallers marks for re-resolution, in one transaction, every file
// with a call site naming a symbol that one of the given files gains or
// loses: newNames maps each file about to be re-indexed or deleted to the
// names and qualified names it will define (nil when deleted), compared
// against what it defines now. A name that appears may now resolve or become
// ambiguous; one that disappears may leave another candidate unambiguous.
// Files in skip (those re-resolved anyway by this run) are not marked. Call
// it before the files' symbols are replaced, since it reads the old names.
func MarkNameCallers(ctx context.Context, db *DB, repoName string, newNames map[string][]string, skip map[string]bool) error {
	if len(newNames) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	paths := make([]string, 0, len(newNames))
	for p := range newNames {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	changedSet := make(map[string]bool)
	for _, p := range paths {
		changed, err := changedNames(ctx, tx, repoName, p, newNames[p])
		if err != nil {
			return err
		}
		for _, n := range changed {
			changedSet[n] = true
		}
	}
	changed := make([]string, 0, len(changedSet))
	for n := range changedSet {
		changed = append(changed, n)
	}
	sort.Strings(changed)

	marked := make(map[string]bool)
	for start := 0; start < len(changed); start += maxNamesPerQuery {
		end := min(start+maxNamesPerQuery, len(changed))
		chunk := changed[start:end]
		args := make([]any, 0, len(chunk)+1)
		args = append(args, repoName)
		for _, n := range chunk {
			args = append(args, n)
		}
		rows, err := tx.QueryContext(ctx,
			`SELECT DISTINCT file_path FROM call_sites INDEXED BY idx_call_sites_name
			 WHERE repo_name = ? AND kind != 'import' AND name IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return fmt.Errorf("store: failed to find callers of changed names in %s: %w", repoName, err)
		}
		for rows.Next() {
			var f string
			if err := rows.Scan(&f); err != nil {
				rows.Close()
				return fmt.Errorf("store: failed to scan caller file row: %w", err)
			}
			if _, own := newNames[f]; !own && !skip[f] {
				marked[f] = true
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("store: failed to read callers of changed names in %s: %w", repoName, err)
		}
		rows.Close()
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO pending_resolution (repo_name, file_path) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("store: failed to prepare pending resolution insert: %w", err)
	}
	defer stmt.Close()
	files := make([]string, 0, len(marked))
	for f := range marked {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		if _, err := stmt.ExecContext(ctx, repoName, f); err != nil {
			return fmt.Errorf("store: failed to mark %s/%s: %w", repoName, f, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: failed to commit name callers for %s: %w", repoName, err)
	}
	return nil
}

// changedNames returns, sorted, the names and qualified names that filePath
// defines now or will define (newNames) but not both.
func changedNames(ctx context.Context, tx *sql.Tx, repoName, filePath string, newNames []string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT name, qualified_name FROM symbols WHERE repo_name = ? AND file_path = ?`,
		repoName, filePath)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get names of %s/%s: %w", repoName, filePath, err)
	}
	old := make(map[string]bool)
	for rows.Next() {
		var name, qn string
		if err := rows.Scan(&name, &qn); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: failed to scan symbol name row: %w", err)
		}
		old[name], old[qn] = true, true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: failed to read names of %s/%s: %w", repoName, filePath, err)
	}
	rows.Close()

	fresh := make(map[string]bool, len(newNames))
	for _, n := range newNames {
		fresh[n] = true
	}
	var changed []string
	for n := range old {
		if !fresh[n] {
			changed = append(changed, n)
		}
	}
	for n := range fresh {
		if !old[n] {
			changed = append(changed, n)
		}
	}
	sort.Strings(changed)
	return changed, nil
}

// GetPendingResolution returns, sorted, the files of repoName whose edges must
// be resolved again (see DetachFileDependencies).
func GetPendingResolution(ctx context.Context, db *DB, repoName string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT file_path FROM pending_resolution WHERE repo_name = ? ORDER BY file_path`, repoName)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get pending resolution for %s: %w", repoName, err)
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("store: failed to scan pending resolution row: %w", err)
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

// ReplaceResolvedDependencies writes the resolved outgoing edges of files in
// one transaction: it deletes every edge whose caller is a symbol in files,
// inserts deps (duplicates ignored), and clears the pending-resolution rows of
// pending. Either all of it is committed or none of it is.
func ReplaceResolvedDependencies(ctx context.Context, db *DB, repoName string, files []string, deps []models.Dependency, pending []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	del, err := tx.PrepareContext(ctx, `DELETE FROM dependencies WHERE caller_id IN (`+fileSymbolIDs+`)`)
	if err != nil {
		return fmt.Errorf("store: failed to prepare dependency delete: %w", err)
	}
	defer del.Close()
	for _, f := range files {
		if _, err := del.ExecContext(ctx, repoName, f); err != nil {
			return fmt.Errorf("store: failed to delete dependencies of %s/%s: %w", repoName, f, err)
		}
	}

	if len(deps) > 0 {
		ins, err := tx.PrepareContext(ctx,
			`INSERT OR IGNORE INTO dependencies (caller_id, callee_id, kind) VALUES (?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("store: failed to prepare dependency insert: %w", err)
		}
		defer ins.Close()
		for _, d := range deps {
			if _, err := ins.ExecContext(ctx, d.CallerID, d.CalleeID, d.Kind); err != nil {
				return fmt.Errorf("store: failed to insert dependency %d->%d: %w", d.CallerID, d.CalleeID, err)
			}
		}
	}

	clr, err := tx.PrepareContext(ctx, `DELETE FROM pending_resolution WHERE repo_name = ? AND file_path = ?`)
	if err != nil {
		return fmt.Errorf("store: failed to prepare pending resolution delete: %w", err)
	}
	defer clr.Close()
	for _, p := range pending {
		if _, err := clr.ExecContext(ctx, repoName, p); err != nil {
			return fmt.Errorf("store: failed to clear pending resolution of %s/%s: %w", repoName, p, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: failed to commit resolved dependencies: %w", err)
	}
	return nil
}

// placeholders returns n comma-separated "?" markers.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
