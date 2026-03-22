-- Migration 004: Add repo_name index on vec_symbols for faster KNN scans.
CREATE INDEX IF NOT EXISTS idx_vec_symbols_repo ON vec_symbols(repo_name);
