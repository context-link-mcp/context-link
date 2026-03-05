package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate runs all pending SQL migration files in order.
// It creates and maintains a schema_version table to track which migrations
// have already been applied. Migrations are forward-only.
func Migrate(db *DB) error {
	// Ensure the version tracking table exists.
	if err := createVersionTable(db.DB); err != nil {
		return fmt.Errorf("store: failed to create schema_version table: %w", err)
	}

	// Read all migration files from the embedded FS.
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: failed to read migrations directory: %w", err)
	}

	// Sort by filename to guarantee correct application order.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		name := entry.Name()
		applied, err := isMigrationApplied(db.DB, name)
		if err != nil {
			return fmt.Errorf("store: failed to check migration %s: %w", name, err)
		}
		if applied {
			slog.Debug("migration already applied, skipping", "file", name)
			continue
		}

		slog.Info("applying migration", "file", name)

		content, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("store: failed to read migration file %s: %w", name, err)
		}

		if err := applyMigration(db.DB, name, string(content)); err != nil {
			return fmt.Errorf("store: failed to apply migration %s: %w", name, err)
		}

		slog.Info("migration applied successfully", "file", name)
	}

	return nil
}

func createVersionTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_version (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			filename    TEXT NOT NULL UNIQUE,
			applied_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

func isMigrationApplied(db *sql.DB, filename string) (bool, error) {
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM schema_version WHERE filename = ?`, filename,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func applyMigration(db *sql.DB, filename, content string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback() //nolint:errcheck
		}
	}()

	if _, err = tx.Exec(content); err != nil {
		return fmt.Errorf("execute SQL: %w", err)
	}

	if _, err = tx.Exec(
		`INSERT INTO schema_version (filename) VALUES (?)`, filename,
	); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	return tx.Commit()
}
