package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openMigratedBefore opens a fresh DB with every migration older than `before` applied.
func openMigratedBefore(t *testing.T, before string) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "legacy.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, createVersionTable(db.DB))

	entries, err := fs.ReadDir(migrationsFS, "migrations")
	require.NoError(t, err)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if e.Name() >= before {
			break
		}
		content, err := migrationsFS.ReadFile("migrations/" + e.Name())
		require.NoError(t, err)
		require.NoError(t, applyMigration(db.DB, e.Name(), string(content)))
	}
	return db
}

func TestMigration008_MemoriesRepoName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		repos []string
		// want maps note -> expected repo_name ("" = NULL).
		want map[string]string
	}{
		{
			name:  "single repo attributes legacy orphans",
			repos: []string{"solo"},
			want: map[string]string{
				"solo linked":   "solo",
				"solo orphan":   "solo",
				"solo dangling": "solo",
			},
		},
		{
			name:  "shared DB leaves legacy orphans unattributed",
			repos: []string{"repoA", "repoB"},
			want: map[string]string{
				"repoA linked":   "repoA",
				"repoB linked":   "repoB",
				"repoA orphan":   "",
				"repoB orphan":   "",
				"repoA dangling": "",
				"repoB dangling": "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := openMigratedBefore(t, "008")

			for _, repo := range tt.repos {
				_, err := db.ExecContext(ctx,
					`INSERT INTO files (repo_name, path, content_hash) VALUES (?, 'auth.go', 'h')`, repo)
				require.NoError(t, err)
				res, err := db.ExecContext(ctx, `
					INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
					VALUES (?, 'Login', 'Login', 'function', 'auth.go', 'h', 'func Login() {}', 1, 1, 'go')`, repo)
				require.NoError(t, err)
				symID, err := res.LastInsertId()
				require.NoError(t, err)

				// Pre-008 rows: no repo_name column yet.
				_, err = db.ExecContext(ctx, `
					INSERT INTO memories (symbol_id, note, last_known_symbol, last_known_file) VALUES (?, ?, 'Login', 'auth.go')`,
					symID, repo+" linked")
				require.NoError(t, err)
				_, err = db.ExecContext(ctx, `
					INSERT INTO memories (symbol_id, note, is_stale, stale_reason, last_known_symbol, last_known_file)
					VALUES (NULL, ?, 1, 'symbol_deleted', 'Logout', 'auth.go')`, repo+" orphan")
				require.NoError(t, err)
				// Linked to a symbol id that no longer exists (FKs were never enforced).
				execWithoutForeignKeys(t, db, `
					INSERT INTO memories (symbol_id, note, is_stale, last_known_symbol, last_known_file)
					VALUES (999999, ?, 1, 'Gone', 'auth.go')`, repo+" dangling")
			}

			require.NoError(t, Migrate(db))

			rows, err := db.QueryContext(ctx, `SELECT note, repo_name FROM memories`)
			require.NoError(t, err)
			got := map[string]string{}
			for rows.Next() {
				var note string
				var repo sql.NullString
				require.NoError(t, rows.Scan(&note, &repo))
				got[note] = repo.String
			}
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
			assert.Equal(t, tt.want, got)

			// Unattributed rows survive a purge from every repo and are never relinked.
			for _, repo := range tt.repos {
				orphans, err := GetAllOrphanedMemories(ctx, db, repo)
				require.NoError(t, err)
				for _, ms := range orphans {
					for _, m := range ms {
						assert.Equal(t, repo, m.RepoName)
					}
				}
				_, err = PurgeStaleMemories(ctx, db, repo, false)
				require.NoError(t, err)
			}
			var left int
			require.NoError(t, db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM memories WHERE repo_name IS NULL`).Scan(&left))
			wantLeft := 0
			for _, r := range tt.want {
				if r == "" {
					wantLeft++
				}
			}
			assert.Equal(t, wantLeft, left, "unattributed legacy rows are never purged")
		})
	}
}
