package vectorstore

import _ "embed"

//go:embed model2vec/model.safetensors
var model2vecSafetensors []byte

//go:embed model2vec/tokenizer.json
var model2vecTokenizerJSON []byte

//go:embed model2vec/config.json
var model2vecConfigJSON []byte
