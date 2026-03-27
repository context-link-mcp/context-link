-- FTS5 full-text search index for hybrid search (vector KNN + BM25 keyword matching).
-- Uses porter stemmer and unicode61 tokenizer for broad language support.
CREATE VIRTUAL TABLE IF NOT EXISTS fts_symbols USING fts5(
    symbol_id UNINDEXED,
    repo_name UNINDEXED,
    name,
    qualified_name,
    kind,
    signature,
    extra_keywords,
    tokenize = 'porter unicode61'
);
