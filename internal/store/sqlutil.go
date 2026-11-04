package store

import (
	"strings"
)

// idChunkSize is the number of IDs bound per IN (...) query. SQLite rejects a
// statement with more than SQLITE_MAX_VARIABLE_NUMBER (32,766) parameters, so
// every query over a caller-supplied ID list goes through forEachIDChunk.
const idChunkSize = 500

// forEachIDChunk calls fn with consecutive slices of items of at most size
// elements (idChunkSize when size <= 0), stopping at the first error. Bind
// each chunk with placeholders(len(chunk)).
func forEachIDChunk[T any](items []T, size int, fn func(chunk []T) error) error {
	if size <= 0 {
		size = idChunkSize
	}
	for start := 0; start < len(items); start += size {
		end := min(start+size, len(items))
		if err := fn(items[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// idArgs converts ids to query arguments, after any leading arguments.
func idArgs(ids []int64, leading ...any) []any {
	args := make([]any, 0, len(leading)+len(ids))
	args = append(args, leading...)
	for _, id := range ids {
		args = append(args, id)
	}
	return args
}

// EscapeLike escapes the LIKE wildcards % and _ and the escape character
// itself, so s matches literally in a pattern used with ESCAPE '\'.
func EscapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
