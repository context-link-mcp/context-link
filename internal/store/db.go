// Package store provides SQLite database access for context-link.
// It manages the connection pool, WAL mode settings, and migration runner.
package store

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

const driverName = "sqlite"

// DB wraps a *sql.DB with context-link specific helpers.
type DB struct {
	*sql.DB
}

// Open opens (or creates) the SQLite database at the given path and applies
// the required PRAGMA settings for safe concurrent access.
func Open(path string) (*DB, error) {
	// modernc/sqlite DSN supports query parameters for pragmas.
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000&_synchronous=NORMAL&_cache_size=-8000&_temp_store=memory", path)

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("store: failed to open database at %s: %w", path, err)
	}

	// SQLite performs best with a single writer. Cap the pool accordingly.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	// Verify connection is alive.
	if err := db.Ping(); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("store: failed to ping database: %w", err)
	}

	// Restrict database file permissions to owner-only (security standard).
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close() //nolint:errcheck
		return nil, fmt.Errorf("store: failed to set database file permissions: %w", err)
	}

	return &DB{db}, nil
}
