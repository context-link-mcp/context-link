package vectorstore

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/store"
)

// TestBasicTokenize_BERTNormalization covers M9: no accent stripping,
// control-character cleanup or CJK splitting before this fix.
func TestBasicTokenize_BERTNormalization(t *testing.T) {
	t.Parallel()
	tok := NewBERTTokenizerFromMap(map[string]int64{"[UNK]": 100})
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"accents stripped", "Café", []string{"cafe"}},
		{"accents stripped, decomposed input", "Café", []string{"cafe"}},
		{"several accents", "naïve résumé", []string{"naive", "resume"}},
		{"NUL dropped", "a\x00b", []string{"ab"}},
		{"replacement char dropped", "a�b", []string{"ab"}},
		{"format char dropped", "x​y", []string{"xy"}},
		{"control char dropped", "x\x07y", []string{"xy"}},
		{"whitespace kinds split", "a\tb\nc d", []string{"a", "b", "c", "d"}},
		{"CJK split per ideograph", "用户登录", []string{"用", "户", "登", "录"}},
		{"CJK next to latin", "token令牌", []string{"token", "令", "牌"}},
		{"punctuation still split", "foo.bar!", []string{"foo", ".", "bar", "!"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tok.basicTokenize(tt.in))
		})
	}
}

// TestModel2Vec_SpecialTokensNotPooled covers M9: [UNK] was averaged into
// the embedding, so any two inputs made of unknown words were identical.
func TestModel2Vec_SpecialTokensNotPooled(t *testing.T) {
	t.Parallel()
	sf, tok, cfg := buildTestModel2Vec(t)
	e := NewModel2VecEmbedderFromBytes(sf, tok, cfg)
	ctx := context.Background()

	hello, err := e.EmbedOne(ctx, "hello")
	require.NoError(t, err)
	helloUnk, err := e.EmbedOne(ctx, "hello qqq zzz")
	require.NoError(t, err)
	assert.InDeltaSlice(t, hello, helloUnk, 1e-6, "unknown words must not shift the embedding")

	allUnk, err := e.EmbedOne(ctx, "qqq zzz")
	require.NoError(t, err)
	assert.True(t, isZeroVector(allUnk), "all-[UNK] input must be the zero vector, got %v", allUnk)
}

// TestModel2Vec_BuiltinUnrelatedNonASCII is the review's example: with the
// real model "café" and "用户登录" had cosine 1.0 and cleared the 0.3 threshold.
func TestModel2Vec_BuiltinUnrelatedNonASCII(t *testing.T) {
	t.Parallel()
	e := NewModel2VecEmbedder()
	ctx := context.Background()
	embed := func(s string) []float32 {
		v, err := e.EmbedOne(ctx, s)
		require.NoError(t, err)
		return v
	}
	cafe, cafeAccent, cjk := embed("cafe"), embed("café"), embed("用户登录")
	assert.InDelta(t, 1.0, dotProduct(cafe, cafeAccent), 1e-5, "accents are stripped: café == cafe")
	assert.Less(t, dotProduct(cafeAccent, cjk), float32(0.3), "unrelated inputs must not clear the 0.3 threshold")
}

// openInternalTestDB opens a migrated database for package-internal tests.
func openInternalTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() }) //nolint:errcheck
	require.NoError(t, store.Migrate(db))
	return db
}

func insertSymbolWithVec(t *testing.T, db *store.DB, repo, name string, vec []float32) int64 {
	t.Helper()
	ctx := context.Background()
	res, err := db.ExecContext(ctx, `
		INSERT INTO symbols (repo_name, name, qualified_name, kind, file_path, content_hash, code_block, start_line, end_line, language)
		VALUES (?, ?, ?, 'function', 'a.go', 'h', 'func x(){}', 1, 2, 'go')`, repo, name, name)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	require.NoError(t, UpsertEmbedding(ctx, db, id, repo, vec))
	return id
}

func spike(dim, pos int) []float32 {
	v := make([]float32, dim)
	v[pos] = 1
	return v
}

// TestKNN_SkipsIncomparableVectors covers M11/M9: vectors of another
// dimension (and zero vectors) scored 0 and came back as hits with
// min_similarity=0; a zero query matched everything at 0.
func TestKNN_SkipsIncomparableVectors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openInternalTestDB(t)
	const repo = "r"

	same := insertSymbolWithVec(t, db, repo, "same", spike(128, 0))
	orth := insertSymbolWithVec(t, db, repo, "orthogonal", spike(128, 1))
	insertSymbolWithVec(t, db, repo, "old384", spike(384, 0))
	insertSymbolWithVec(t, db, repo, "zero", make([]float32, 128))

	searches := map[string]func(q []float32) ([]SearchResult, error){
		"db": func(q []float32) ([]SearchResult, error) { return KNNSearch(ctx, db, repo, q, 10, 0) },
		"cached": func(q []float32) ([]SearchResult, error) {
			return KNNSearchCached(ctx, db, NewVectorCache(repo), q, 10, 0)
		},
	}
	for name, search := range searches {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			res, err := search(spike(128, 0))
			require.NoError(t, err)
			ids := make([]int64, 0, len(res))
			for _, r := range res {
				ids = append(ids, r.SymbolID)
			}
			assert.ElementsMatch(t, []int64{same, orth}, ids,
				"only same-dimension, non-zero vectors may be returned (orthogonal is a real 0 score)")

			res, err = search(make([]float32, 128))
			require.NoError(t, err)
			assert.Empty(t, res, "a zero query (no known tokens) matches nothing")
		})
	}
}

func TestKNNFiltered_FilterBeforeTopK(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openInternalTestDB(t)
	const repo = "r"

	q := spike(4, 0)
	var weakest int64
	for i := 0; i < 5; i++ {
		v := []float32{1 - float32(i)*0.1, float32(i) * 0.1, 0, 0}
		l2Normalize(v)
		weakest = insertSymbolWithVec(t, db, repo, "s"+strconv.Itoa(i), v)
	}
	allow := func(id int64) bool { return id == weakest }

	res, err := KNNSearchFiltered(ctx, db, repo, q, 1, 0, allow)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, weakest, res[0].SymbolID)

	res, err = KNNSearchCachedFiltered(ctx, db, NewVectorCache(repo), q, 1, 0, allow)
	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, weakest, res[0].SymbolID)
}

func TestSimilarities(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openInternalTestDB(t)
	const repo = "r"
	a := insertSymbolWithVec(t, db, repo, "a", spike(8, 0))
	b := insertSymbolWithVec(t, db, repo, "b", spike(8, 1))
	wrongDim := insertSymbolWithVec(t, db, repo, "c", spike(16, 0))

	for _, cache := range []*VectorCache{nil, NewVectorCache(repo)} {
		sims, err := Similarities(ctx, db, cache, repo, spike(8, 0), []int64{a, b, wrongDim, 9999})
		require.NoError(t, err)
		assert.Equal(t, map[int64]float32{a: 1, b: 0}, sims)
	}
}

func readDimMeta(t *testing.T, db *store.DB) string {
	t.Helper()
	var v string
	err := db.QueryRowContext(context.Background(), `SELECT value FROM vec_meta WHERE key = 'embedding_dim'`).Scan(&v)
	if err == sql.ErrNoRows {
		return ""
	}
	require.NoError(t, err)
	return v
}

// TestEnsureEmbeddingDimension covers M11: the mismatch never cleared after
// --force replaced every vector.
func TestEnsureEmbeddingDimension(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name     string
		meta     string // "" = no row
		vecDims  []int
		expected int
		wantErr  bool
		wantMeta string
	}{
		{"fresh db records dim", "", nil, 128, false, "128"},
		{"matching record", "128", []int{128}, 128, false, "128"},
		{"old vectors remain", "384", []int{384, 384}, 128, true, "384"},
		{"mixed vectors remain", "384", []int{128, 384}, 128, true, "384"},
		{"all re-embedded clears mismatch", "384", []int{128, 128}, 128, false, "128"},
		{"record missing but old vectors", "", []int{384}, 128, true, ""},
		{"corrupt record, vectors match", "abc", []int{128}, 128, false, "128"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := openInternalTestDB(t)
			if tt.meta != "" {
				_, err := db.ExecContext(ctx, `INSERT INTO vec_meta (key, value) VALUES ('embedding_dim', ?)`, tt.meta)
				require.NoError(t, err)
			}
			for i, d := range tt.vecDims {
				insertSymbolWithVec(t, db, "r", "s"+strconv.Itoa(i), spike(d, 0))
			}
			err := EnsureEmbeddingDimension(ctx, db, tt.expected)
			if tt.wantErr {
				require.ErrorIs(t, err, ErrDimensionMismatch)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantMeta, readDimMeta(t, db))
		})
	}
}

// failingMetaReader fails every read of vec_meta, as a SQLITE_BUSY would,
// while writes go through.
type failingMetaReader struct{ *store.DB }

func (f failingMetaReader) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if strings.Contains(query, "vec_meta") {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return f.DB.QueryRowContext(cancelled, query, args...)
	}
	return f.DB.QueryRowContext(ctx, query, args...)
}

// TestEnsureEmbeddingDimension_ReadErrorKeepsRecord covers M11: any read
// error used to overwrite the stored dimension.
func TestEnsureEmbeddingDimension_ReadErrorKeepsRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openInternalTestDB(t)
	require.NoError(t, SetEmbeddingDimension(ctx, db, 384))

	err := ensureEmbeddingDimension(ctx, failingMetaReader{db}, 128)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrDimensionMismatch)
	assert.Equal(t, "384", readDimMeta(t, db), "a failed read must not rewrite the dimension")
}

// TestNewONNXEmbedder_EagerValidation covers M10: a missing model or vocab
// used to "succeed" and fail lazily on the first embedding.
func TestNewONNXEmbedder_EagerValidation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	model := filepath.Join(dir, "model.onnx")
	require.NoError(t, os.WriteFile(model, []byte("not a model"), 0o600))
	missing := filepath.Join(dir, "missing.txt")

	tests := []struct {
		name              string
		model, vocab, lib string
	}{
		{"missing model", missing, model, ""},
		{"missing vocab", model, missing, ""},
		{"model is a directory", dir, model, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e, err := NewONNXEmbedder(tt.model, tt.vocab, tt.lib)
			require.Error(t, err)
			assert.Nil(t, e)

			fallback := NewConfiguredEmbedder(tt.model, tt.vocab, tt.lib)
			require.IsType(t, &Model2VecEmbedder{}, fallback)
			assert.Equal(t, Model2VecDim, fallback.Dim())
		})
	}

	_, err := NewONNXEmbedder("", "", "")
	assert.ErrorIs(t, err, ErrModelNotConfigured)
	assert.IsType(t, &Model2VecEmbedder{}, NewConfiguredEmbedder("", "", ""))
	assert.IsType(t, &Model2VecEmbedder{}, NewConfiguredEmbedder(model, "", ""), "half-configured ONNX falls back")
}
