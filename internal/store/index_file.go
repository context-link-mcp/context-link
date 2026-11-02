package store

import (
	"context"
	"fmt"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// fileSymbolIDs selects the IDs of a file's symbols; it takes repo_name and
// file_path as parameters.
const fileSymbolIDs = `SELECT id FROM symbols WHERE repo_name = ? AND file_path = ?`

// ReplaceFileIndex atomically replaces everything indexed for one file: it
// marks the other files with edges into it for re-resolution, deletes the
// file's FTS rows, embeddings, dependency edges (both directions, as the
// schema's ON DELETE CASCADE would), route definitions, call sites and
// symbols, inserts the new symbols and call sites, and upserts the file record
// with f.ContentHash last. Either all of it is committed or none of it is.
//
// Callers that still have work to do for the file after this call (dependency
// resolution, FTS, embeddings) pass an empty ContentHash, which incremental
// runs treat as changed, and commit the real hash with SetFileHashes once the
// file is fully indexed.
func ReplaceFileIndex(ctx context.Context, db *DB, f *models.File, symbols []models.Symbol, sites []CallSite) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: failed to begin file replace for %s: %w", f.Path, err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Callers in other files lose their edges into this file below; mark them
	// for re-resolution in the same transaction (see markEdgeCallers).
	if err := markEdgeCallers(ctx, tx, f.RepoName, f.Path); err != nil {
		return err
	}

	deletes := []struct {
		what  string
		query string
		args  []any
	}{
		{"FTS entries", `DELETE FROM fts_symbols WHERE symbol_id IN (` + fileSymbolIDs + `)`,
			[]any{f.RepoName, f.Path}},
		{"embeddings", `DELETE FROM vec_symbols WHERE symbol_id IN (` + fileSymbolIDs + `)`,
			[]any{f.RepoName, f.Path}},
		{"dependencies", `DELETE FROM dependencies WHERE caller_id IN (` + fileSymbolIDs + `)
			OR callee_id IN (` + fileSymbolIDs + `)`,
			[]any{f.RepoName, f.Path, f.RepoName, f.Path}},
		{"routes", `DELETE FROM routes WHERE repo_name = ? AND file_path = ? AND kind = 'definition'`,
			[]any{f.RepoName, f.Path}},
		{"call sites", `DELETE FROM call_sites WHERE repo_name = ? AND file_path = ?`,
			[]any{f.RepoName, f.Path}},
		{"symbols", `DELETE FROM symbols WHERE repo_name = ? AND file_path = ?`,
			[]any{f.RepoName, f.Path}},
	}
	for _, d := range deletes {
		if _, err := tx.ExecContext(ctx, d.query, d.args...); err != nil {
			return fmt.Errorf("store: failed to delete old %s for %s/%s: %w", d.what, f.RepoName, f.Path, err)
		}
	}

	if len(symbols) > 0 {
		stmt, err := tx.PrepareContext(ctx,
			`INSERT OR REPLACE INTO symbols
			 (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		)
		if err != nil {
			return fmt.Errorf("store: failed to prepare symbol insert: %w", err)
		}
		defer stmt.Close()
		for _, s := range symbols {
			if _, err := stmt.ExecContext(ctx,
				s.RepoName, s.Name, s.QualifiedName, s.Kind, s.FilePath,
				s.ContentHash, s.CodeBlock, s.StartLine, s.EndLine, s.Language,
			); err != nil {
				return fmt.Errorf("store: failed to insert symbol %s: %w", s.QualifiedName, err)
			}
		}
	}

	if err := insertCallSites(ctx, tx, f.RepoName, f.Path, sites); err != nil {
		return err
	}

	// The file record goes last, so its hash is never newer than its symbols.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO files (repo_name, path, content_hash, size_bytes)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(repo_name, path)
		 DO UPDATE SET content_hash = excluded.content_hash,
		               size_bytes = excluded.size_bytes,
		               last_indexed = CURRENT_TIMESTAMP`,
		f.RepoName, f.Path, f.ContentHash, f.SizeBytes,
	); err != nil {
		return fmt.Errorf("store: failed to upsert file %s/%s: %w", f.RepoName, f.Path, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: failed to commit file replace for %s/%s: %w", f.RepoName, f.Path, err)
	}
	return nil
}

// SetFileHashes sets the content hash of existing file records (path → hash)
// in one transaction. Paths with no file record are ignored.
func SetFileHashes(ctx context.Context, db *DB, repoName string, hashes map[string]string) error {
	if len(hashes) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: failed to begin file hash update: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE files SET content_hash = ? WHERE repo_name = ? AND path = ?`)
	if err != nil {
		return fmt.Errorf("store: failed to prepare file hash update: %w", err)
	}
	defer stmt.Close()
	for path, hash := range hashes {
		if _, err := stmt.ExecContext(ctx, hash, repoName, path); err != nil {
			return fmt.Errorf("store: failed to set hash for %s/%s: %w", repoName, path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: failed to commit file hashes: %w", err)
	}
	return nil
}
