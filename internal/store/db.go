// Package store provides SQLite database access for context-link.
// It manages the connection pool, WAL mode settings, and migration runner.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

const driverName = "sqlite"

// DB wraps a *sql.DB with context-link specific helpers.
type DB struct {
	*sql.DB
	path string
}

// Path returns the filesystem path the database was opened from, or "" for
// a DB not created by Open. The indexer locks <Path()>.lock across processes
// for the duration of an index run (see internal/store/filelock).
func (db *DB) Path() string {
	if db == nil {
		return ""
	}
	return db.path
}

// Open opens (or creates) the SQLite database at the given path and applies
// the required PRAGMA settings for safe concurrent access.
func Open(path string) (*DB, error) {
	// modernc.org/sqlite runs each _pragma parameter on every new connection.
	// It ignores mattn-style names such as _foreign_keys, so every setting must
	// use this form. busy_timeout goes first so the others wait on a lock.
	dsn := path + "?" + strings.Join([]string{
		"_pragma=busy_timeout(5000)",
		"_pragma=foreign_keys(1)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=cache_size(-8000)",
		"_pragma=temp_store(MEMORY)",
	}, "&")

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

	return &DB{DB: db, path: path}, nil
}
