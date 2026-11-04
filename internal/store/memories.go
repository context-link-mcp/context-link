package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/context-link-mcp/context-link/pkg/models"
)

// ErrMemoryNotFound is returned when a memory record is not found.
var ErrMemoryNotFound = errors.New("memory not found")

// SaveMemory inserts a new memory linked to a symbol.
// Returns the new memory's ID. Deduplicates by (symbol_id, exact note match).
// The row's repo_name is mem.RepoName, or the linked symbol's repo when
// mem.RepoName is empty. A memory with neither stays unattributed (NULL) and is
// never purged, relinked or returned by repo-scoped reads.
func SaveMemory(ctx context.Context, db *DB, mem *models.Memory) (int64, error) {
	if len([]rune(mem.Note)) > 2000 {
		return 0, fmt.Errorf("store: note exceeds maximum length of 2000 characters")
	}

	// Dedup: reject exact duplicate note for the same symbol.
	if mem.SymbolID != nil {
		var count int
		err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM memories WHERE symbol_id = ? AND note = ?`,
			*mem.SymbolID, mem.Note,
		).Scan(&count)
		if err != nil {
			return 0, fmt.Errorf("store: failed to check duplicate memory: %w", err)
		}
		if count > 0 {
			return 0, fmt.Errorf("store: identical note already exists for this symbol")
		}
	}

	res, err := db.ExecContext(ctx,
		`INSERT INTO memories (symbol_id, repo_name, note, author, last_known_symbol, last_known_file)
		 VALUES (?, COALESCE(?, (SELECT repo_name FROM symbols WHERE id = ?)), ?, ?, ?, ?)`,
		mem.SymbolID, nullString(mem.RepoName), mem.SymbolID,
		mem.Note, mem.Author, mem.LastKnownSymbol, mem.LastKnownFile,
	)
	if err != nil {
		return 0, fmt.Errorf("store: failed to insert memory: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: failed to get memory ID: %w", err)
	}
	return id, nil
}

// GetMemoriesBySymbolID returns all memories for a given symbol ID.
func GetMemoriesBySymbolID(ctx context.Context, db *DB, symbolID int64) ([]models.Memory, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, symbol_id, repo_name, note, author, is_stale, stale_reason,
		        last_known_symbol, last_known_file, created_at, updated_at
		 FROM memories WHERE symbol_id = ?
		 ORDER BY created_at DESC`,
		symbolID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get memories for symbol %d: %w", symbolID, err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

// GetMemoriesBySymbolName returns memories of symbols of repoName whose name
// or qualified name equals symbolName exactly (no substring or wildcard
// matching), and orphaned memories of this repo whose last known symbol is
// symbolName or ends in "."+symbolName. Results include stale memories with
// IsStale set, newest first, and support pagination. A blank name returns no
// memories.
func GetMemoriesBySymbolName(ctx context.Context, db *DB, repoName, symbolName string, offset, limit int) ([]models.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	if strings.TrimSpace(symbolName) == "" {
		return []models.Memory{}, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT m.id, m.symbol_id, m.repo_name, m.note, m.author, m.is_stale, m.stale_reason,
		        m.last_known_symbol, m.last_known_file, m.created_at, m.updated_at
		 FROM memories m
		 LEFT JOIN symbols s ON s.id = m.symbol_id
		 WHERE (s.repo_name = ? AND (s.name = ? OR s.qualified_name = ?))
		    OR (s.id IS NULL AND m.repo_name = ? AND
		        (m.last_known_symbol = ? OR substr(m.last_known_symbol, -(length(?) + 1)) = '.' || ?))
		 ORDER BY m.created_at DESC, m.id DESC
		 LIMIT ? OFFSET ?`,
		repoName, symbolName, symbolName,
		repoName, symbolName, symbolName, symbolName, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get memories for symbol name %q: %w", symbolName, err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

// GetMemoriesByFilePath returns memories for all symbols in a given file,
// plus orphaned memories of this repo last seen in that file. Supports pagination.
func GetMemoriesByFilePath(ctx context.Context, db *DB, repoName, filePath string, offset, limit int) ([]models.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.QueryContext(ctx,
		`SELECT m.id, m.symbol_id, m.repo_name, m.note, m.author, m.is_stale, m.stale_reason,
		        m.last_known_symbol, m.last_known_file, m.created_at, m.updated_at
		 FROM memories m
		 LEFT JOIN symbols s ON s.id = m.symbol_id
		 WHERE (s.repo_name = ? AND s.file_path = ?)
		    OR (s.id IS NULL AND m.repo_name = ? AND m.last_known_file = ?)
		 ORDER BY m.created_at DESC
		 LIMIT ? OFFSET ?`,
		repoName, filePath, repoName, filePath, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get memories for file %s: %w", filePath, err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

// GetOrphanedMemoriesBySymbol returns memories of repoName that are orphaned
// (symbol_id IS NULL) and whose last_known_symbol + last_known_file match the
// given values. Used during orphan recovery after re-indexing.
func GetOrphanedMemoriesBySymbol(ctx context.Context, db *DB, repoName, lastKnownSymbol, lastKnownFile string) ([]models.Memory, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, symbol_id, repo_name, note, author, is_stale, stale_reason,
		        last_known_symbol, last_known_file, created_at, updated_at
		 FROM memories
		 WHERE symbol_id IS NULL AND repo_name = ? AND last_known_symbol = ? AND last_known_file = ?`,
		repoName, lastKnownSymbol, lastKnownFile,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get orphaned memories: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

// GetAllOrphanedMemories returns the orphaned memories (symbol_id IS NULL) of
// repoName, grouped by "qualifiedName\x00filePath" key for fast lookup during
// relinking. Unattributed legacy rows (repo_name NULL) are never returned, so
// they cannot be relinked to another repo's symbol at the same path.
func GetAllOrphanedMemories(ctx context.Context, db *DB, repoName string) (map[string][]models.Memory, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, symbol_id, repo_name, note, author, is_stale, stale_reason,
		        last_known_symbol, last_known_file, created_at, updated_at
		 FROM memories
		 WHERE symbol_id IS NULL AND repo_name = ?
		   AND last_known_symbol IS NOT NULL AND last_known_file IS NOT NULL`,
		repoName,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to get all orphaned memories: %w", err)
	}
	defer rows.Close()

	memories, err := scanMemories(rows)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]models.Memory, len(memories))
	for _, m := range memories {
		key := m.LastKnownSymbol + "\x00" + m.LastKnownFile
		result[key] = append(result[key], m)
	}
	return result, nil
}

// SnapshotAndMarkStale snapshots last_known_symbol/file into memories linked to
// the given symbol and marks them stale. Called before deleting a symbol that has
// changed or is being removed. Returns the number of memories actually updated.
func SnapshotAndMarkStale(ctx context.Context, db *DB, symbolID int64, qualifiedName, filePath, reason string) (int, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE memories
		 SET is_stale = 1,
		     stale_reason = ?,
		     last_known_symbol = ?,
		     last_known_file = ?,
		     repo_name = COALESCE(repo_name, (SELECT repo_name FROM symbols WHERE id = memories.symbol_id)),
		     updated_at = ?
		 WHERE symbol_id = ? AND is_stale = 0`,
		reason, qualifiedName, filePath, time.Now().UTC(), symbolID,
	)
	if err != nil {
		return 0, fmt.Errorf("store: failed to mark memories stale for symbol %d: %w", symbolID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: failed to get rows affected for stale update: %w", err)
	}
	return int(n), nil
}

// DetachMemoriesFromSymbol explicitly sets symbol_id = NULL for all memories
// linked to the given symbol. Called before deleting a symbol to ensure memories
// become orphaned regardless of FK cascade support in the driver.
func DetachMemoriesFromSymbol(ctx context.Context, db *DB, symbolID int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE memories
		 SET repo_name = COALESCE(repo_name, (SELECT repo_name FROM symbols WHERE id = ?)),
		     symbol_id = NULL
		 WHERE symbol_id = ?`,
		symbolID, symbolID,
	)
	if err != nil {
		return fmt.Errorf("store: failed to detach memories from symbol %d: %w", symbolID, err)
	}
	return nil
}

// RelinkMemory re-attaches an orphaned memory to a new symbol and clears the stale flag.
func RelinkMemory(ctx context.Context, db *DB, memoryID, symbolID int64) error {
	_, err := db.ExecContext(ctx,
		`UPDATE memories
		 SET symbol_id = ?, is_stale = 0, stale_reason = NULL, updated_at = ?
		 WHERE id = ?`,
		symbolID, time.Now().UTC(), memoryID,
	)
	if err != nil {
		return fmt.Errorf("store: failed to relink memory %d: %w", memoryID, err)
	}
	return nil
}

// CountMemoriesBySymbolID returns the count of memories linked to a symbol.
func CountMemoriesBySymbolID(ctx context.Context, db *DB, symbolID int64) (int, error) {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories WHERE symbol_id = ?`,
		symbolID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: failed to count memories for symbol %d: %w", symbolID, err)
	}
	return count, nil
}

// CountMemoriesBySymbolIDs returns memory counts keyed by symbol ID for a
// batch. Symbols without memories are absent. Any number of IDs is accepted;
// they are queried in chunks.
func CountMemoriesBySymbolIDs(ctx context.Context, db *DB, symbolIDs []int64) (map[int64]int, error) {
	counts := make(map[int64]int, len(symbolIDs))
	err := forEachIDChunk(symbolIDs, 0, func(chunk []int64) error {
		rows, err := db.QueryContext(ctx,
			`SELECT symbol_id, COUNT(*) FROM memories WHERE symbol_id IN (`+placeholders(len(chunk))+`) GROUP BY symbol_id`,
			idArgs(chunk)...,
		)
		if err != nil {
			return fmt.Errorf("store: failed to count memories by symbol IDs: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var id int64
			var count int
			if err := rows.Scan(&id, &count); err != nil {
				return fmt.Errorf("store: failed to scan memory count row: %w", err)
			}
			counts[id] = count
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return counts, nil
}

// PurgeStaleMemories deletes stale memories of repoName. If orphanedOnly is
// true, only deletes memories where symbol_id IS NULL (fully orphaned).
// Returns purged count.
//
// A memory belongs to repoName when its repo_name says so, or when it is still
// linked to one of repoName's symbols. Rows with repo_name NULL and no live
// link (legacy orphans in a shared DB, see migration 008) are never deleted:
// they cannot be attributed to this repo, and deleting another repo's note is
// permanent.
func PurgeStaleMemories(ctx context.Context, db *DB, repoName string, orphanedOnly bool) (int, error) {
	var res sql.Result
	var err error
	if orphanedOnly {
		res, err = db.ExecContext(ctx,
			`DELETE FROM memories
			 WHERE is_stale = 1 AND symbol_id IS NULL AND repo_name = ?`,
			repoName,
		)
	} else {
		res, err = db.ExecContext(ctx,
			`DELETE FROM memories
			 WHERE is_stale = 1 AND (
			   repo_name = ?
			   OR symbol_id IN (SELECT id FROM symbols WHERE repo_name = ?)
			 )`,
			repoName, repoName,
		)
	}
	if err != nil {
		return 0, fmt.Errorf("store: failed to purge stale memories: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: failed to get rows affected: %w", err)
	}
	return int(n), nil
}

// nullString maps "" to SQL NULL.
func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// scanMemory scans a single memory row from a *sql.Rows cursor.
func scanMemory(rows *sql.Rows) (models.Memory, error) {
	var m models.Memory
	var symbolID sql.NullInt64
	var repoName, staleReason sql.NullString
	err := rows.Scan(
		&m.ID, &symbolID, &repoName, &m.Note, &m.Author, &m.IsStale, &staleReason,
		&m.LastKnownSymbol, &m.LastKnownFile, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return models.Memory{}, fmt.Errorf("store: failed to scan memory row: %w", err)
	}
	if symbolID.Valid {
		m.SymbolID = &symbolID.Int64
	}
	m.RepoName = repoName.String
	m.StaleReason = staleReason.String
	return m, nil
}

// scanMemories scans all memory rows.
func scanMemories(rows *sql.Rows) ([]models.Memory, error) {
	var mems []models.Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		mems = append(mems, m)
	}
	return mems, rows.Err()
}
