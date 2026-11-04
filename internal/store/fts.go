package store

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

// FTSSymbol represents a symbol entry for the FTS5 index.
type FTSSymbol struct {
	SymbolID      int64
	RepoName      string
	Name          string
	QualifiedName string
	Kind          string
	Signature     string
	ExtraKeywords string
}

// FTSResult represents a single FTS5 search result with BM25 score.
type FTSResult struct {
	SymbolID int64
	Rank     float64 // BM25 rank (lower = more relevant)
}

// BatchInsertFTSSymbols inserts multiple FTS5 entries in a single transaction.
func BatchInsertFTSSymbols(ctx context.Context, db *DB, symbols []FTSSymbol) error {
	if len(symbols) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: failed to begin FTS transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO fts_symbols (symbol_id, repo_name, name, qualified_name, kind, signature, extra_keywords)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("store: failed to prepare FTS insert: %w", err)
	}
	defer stmt.Close()

	for _, s := range symbols {
		// Index the words of camelCase/PascalCase names too, so a query for
		// "validate token" matches validateToken (see identifierWords).
		extra := s.ExtraKeywords
		if words := identifierWords(s.QualifiedName); words != "" {
			extra = strings.TrimSpace(extra + " " + words)
		}
		_, err := stmt.ExecContext(ctx,
			s.SymbolID, s.RepoName, s.Name, s.QualifiedName, s.Kind, s.Signature, extra,
		)
		if err != nil {
			return fmt.Errorf("store: failed to insert FTS symbol %s: %w", s.QualifiedName, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: failed to commit FTS batch: %w", err)
	}
	return nil
}

// DeleteFTSByRepo removes all FTS entries for a repository.
func DeleteFTSByRepo(ctx context.Context, db *DB, repoName string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM fts_symbols WHERE repo_name = ?`,
		repoName,
	)
	if err != nil {
		return fmt.Errorf("store: failed to delete FTS entries for repo %s: %w", repoName, err)
	}
	return nil
}

// DeleteFTSByFile removes FTS entries for symbols belonging to a specific file.
func DeleteFTSByFile(ctx context.Context, db *DB, repoName, filePath string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM fts_symbols WHERE symbol_id IN (
			SELECT id FROM symbols WHERE repo_name = ? AND file_path = ?
		)`,
		repoName, filePath,
	)
	if err != nil {
		return fmt.Errorf("store: failed to delete FTS entries for file %s/%s: %w", repoName, filePath, err)
	}
	return nil
}

// maxFTSQueryTerms bounds the number of OR-ed terms per query.
const maxFTSQueryTerms = 16

// ftsStopwords are English filler words dropped from natural-language
// queries ("how do we validate a token"). They would match almost any
// signature and only add noise to the BM25 ranking. A query made only of
// stopwords keeps them.
var ftsStopwords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {},
	"do": {}, "does": {}, "for": {}, "from": {}, "how": {}, "i": {}, "in": {}, "is": {},
	"it": {}, "of": {}, "on": {}, "or": {}, "that": {}, "the": {}, "this": {}, "to": {},
	"we": {}, "what": {}, "where": {}, "which": {}, "with": {},
}

// sanitizeFTSQuery turns free text into an injection-safe FTS5 query that
// matches any of its words: `"validate" OR "token"`.
//
// The text is split into words of letters, digits and combining marks only,
// so no FTS5 syntax (quotes, parentheses, *, ^, :, -, +) survives, and each
// word is wrapped in double quotes, so AND/OR/NOT/NEAR are literal terms.
// Words are OR-ed rather than AND-ed: natural-language queries contain words
// a symbol does not ("how", "check", synonyms), and AND would return nothing
// for them. BM25 sums per-term scores, so symbols matching more of the words
// still rank first. Stopwords are dropped (unless nothing else is left),
// duplicates removed and the term count capped at maxFTSQueryTerms.
// Returns "" when the text has no words.
func sanitizeFTSQuery(query string) string {
	words := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r)
	})
	if len(words) == 0 {
		return ""
	}

	seen := make(map[string]struct{}, len(words))
	terms := make([]string, 0, len(words))
	add := func(w string, skipStopwords bool) {
		if _, dup := seen[w]; dup {
			return
		}
		if _, stop := ftsStopwords[w]; stop && skipStopwords {
			return
		}
		seen[w] = struct{}{}
		terms = append(terms, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
	}
	for _, w := range words {
		add(w, true)
	}
	if len(terms) == 0 {
		for _, w := range words {
			add(w, false)
		}
	}
	if len(terms) > maxFTSQueryTerms {
		terms = terms[:maxFTSQueryTerms]
	}
	return strings.Join(terms, " OR ")
}

// identifierWords splits identifiers into their words so FTS can match
// natural-language queries against them: "ParserPool.Parse" → "parser pool
// parse", "HTTPServer" → "http server". FTS5's unicode61 tokenizer already
// splits on '_' and '.', but keeps "validateToken" as one token, so
// "validate token" could never match it. Returns "" when no identifier has
// more than one word.
func identifierWords(idents ...string) string {
	var words []string
	split := false
	for _, ident := range idents {
		for _, part := range strings.FieldsFunc(ident, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}) {
			sub := splitCamel(part)
			if len(sub) > 1 {
				split = true
			}
			words = append(words, sub...)
		}
	}
	if !split {
		return ""
	}
	return strings.ToLower(strings.Join(words, " "))
}

// splitCamel splits one alphanumeric run at lower→upper and acronym→word
// boundaries: "getHTTPResponse2" → ["get", "HTTP", "Response2"].
func splitCamel(s string) []string {
	runes := []rune(s)
	var out []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := (unicode.IsLower(prev) || unicode.IsDigit(prev)) && unicode.IsUpper(cur)
		if !boundary && unicode.IsUpper(prev) && unicode.IsUpper(cur) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
			boundary = true // "HTTPServer": split before "S"
		}
		if boundary {
			out = append(out, string(runes[start:i]))
			start = i
		}
	}
	return append(out, string(runes[start:]))
}

// SymbolFilter restricts search results by symbol kind and file path prefix.
// Empty fields do not filter.
type SymbolFilter struct {
	Kind           string
	FilePathPrefix string
}

// IsZero reports whether the filter accepts every symbol.
func (f SymbolFilter) IsZero() bool { return f.Kind == "" && f.FilePathPrefix == "" }

// FilterSymbolIDs returns the IDs of the repo's symbols that match f. Use it
// to restrict a vector search before its top-k cut.
func FilterSymbolIDs(ctx context.Context, db *DB, repoName string, f SymbolFilter) (map[int64]struct{}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id FROM symbols
		 WHERE repo_name = ?
		   AND (? = '' OR kind = ?)
		   AND (? = '' OR substr(file_path, 1, length(?)) = ?)`,
		repoName, f.Kind, f.Kind, f.FilePathPrefix, f.FilePathPrefix, f.FilePathPrefix,
	)
	if err != nil {
		return nil, fmt.Errorf("store: failed to filter symbols: %w", err)
	}
	defer rows.Close()

	ids := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: failed to scan symbol id: %w", err)
		}
		ids[id] = struct{}{}
	}
	return ids, rows.Err()
}

// FTSSearch performs a full-text search using FTS5 BM25 ranking.
// Returns results ordered by relevance (best match first).
func FTSSearch(ctx context.Context, db *DB, repoName, query string, limit int) ([]FTSResult, error) {
	return FTSSearchFiltered(ctx, db, repoName, query, SymbolFilter{}, limit)
}

// FTSSearchFiltered is FTSSearch restricted to symbols matching f; the filter
// is applied in SQL, before the LIMIT.
func FTSSearchFiltered(ctx context.Context, db *DB, repoName, query string, f SymbolFilter, limit int) ([]FTSResult, error) {
	if limit <= 0 {
		limit = 10
	}

	// Sanitize query to prevent FTS5 syntax errors from special characters.
	ftsQuery := sanitizeFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	rows, err := db.QueryContext(ctx,
		`SELECT fts_symbols.symbol_id, fts_symbols.rank
		 FROM fts_symbols
		 JOIN symbols s ON s.id = fts_symbols.symbol_id AND s.repo_name = fts_symbols.repo_name
		 WHERE fts_symbols MATCH ? AND fts_symbols.repo_name = ?
		   AND (? = '' OR s.kind = ?)
		   AND (? = '' OR substr(s.file_path, 1, length(?)) = ?)
		 ORDER BY fts_symbols.rank
		 LIMIT ?`,
		ftsQuery, repoName,
		f.Kind, f.Kind, f.FilePathPrefix, f.FilePathPrefix, f.FilePathPrefix,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: FTS search failed: %w", err)
	}
	defer rows.Close()

	var results []FTSResult
	for rows.Next() {
		var r FTSResult
		if err := rows.Scan(&r.SymbolID, &r.Rank); err != nil {
			return nil, fmt.Errorf("store: failed to scan FTS result: %w", err)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}
