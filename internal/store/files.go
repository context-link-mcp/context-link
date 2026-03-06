package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/context-link/context-link/pkg/models"
)

// ErrFileNotFound is returned when a file record is not found.
var ErrFileNotFound = errors.New("file not found")

// GetFileByPath retrieves a file record by repo name and relative path.
func GetFileByPath(ctx context.Context, db *DB, repoName, path string) (*models.File, error) {
	row := db.QueryRowContext(ctx,
		`SELECT id, repo_name, path, content_hash, last_indexed, size_bytes
		 FROM files WHERE repo_name = ? AND path = ?`,
		repoName, path,
	)

	var f models.File
	err := row.Scan(&f.ID, &f.RepoName, &f.Path, &f.ContentHash, &f.LastIndexed, &f.SizeBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFileNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: failed to get file %s/%s: %w", repoName, path, err)
	}
	return &f, nil
}

// UpsertFile inserts or updates a file record. On conflict (same repo+path),
// it updates the content hash, size, and timestamp.
func UpsertFile(ctx context.Context, db *DB, f *models.File) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO files (repo_name, path, content_hash, size_bytes)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(repo_name, path)
		 DO UPDATE SET content_hash = excluded.content_hash,
		               size_bytes = excluded.size_bytes,
		               last_indexed = CURRENT_TIMESTAMP`,
		f.RepoName, f.Path, f.ContentHash, f.SizeBytes,
	)
	if err != nil {
		return fmt.Errorf("store: failed to upsert file %s/%s: %w", f.RepoName, f.Path, err)
	}
	return nil
}

// ListFiles returns all file records for a repository.
func ListFiles(ctx context.Context, db *DB, repoName string) ([]models.File, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, repo_name, path, content_hash, last_indexed, size_bytes
		 FROM files WHERE repo_name = ? ORDER BY path`,
		repoName,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to list files for %s: %w", repoName, err)
	}
	defer rows.Close()

	var files []models.File
	for rows.Next() {
		var f models.File
		if err := rows.Scan(&f.ID, &f.RepoName, &f.Path, &f.ContentHash, &f.LastIndexed, &f.SizeBytes); err != nil {
			return nil, fmt.Errorf("store: failed to scan file row: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// DeleteFileByPath removes a file record from the database.
func DeleteFileByPath(ctx context.Context, db *DB, repoName, path string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM files WHERE repo_name = ? AND path = ?`,
		repoName, path,
	)
	if err != nil {
		return fmt.Errorf("store: failed to delete file %s/%s: %w", repoName, path, err)
	}
	return nil
}
