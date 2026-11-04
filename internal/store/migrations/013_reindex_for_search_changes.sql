-- Search changes in the same version only reach a file when it is indexed
-- again: FTS rows now also hold the words of camelCase/PascalCase names
-- (validateToken -> "validate token"), and embeddings use BERT normalisation
-- without [UNK]/special tokens in the Model2Vec mean. Blank every file hash,
-- as 007/009/010/012 did, so the next normal index re-indexes every file and
-- rewrites its FTS rows and embeddings. No row is deleted here.
UPDATE files SET content_hash = '';
