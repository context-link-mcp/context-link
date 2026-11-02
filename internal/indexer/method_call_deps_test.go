package indexer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/context-link-mcp/context-link/internal/indexer/adapters"
)

// TestExtractDependencies_MethodAndSelectorCalls guards against method and
// selector calls (s.Load(), pkg.Func(), self.helper(), this.helper()) being
// dropped: they must produce call deps on the method or function name.
func TestExtractDependencies_MethodAndSelectorCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		adapter LanguageAdapter
		source  string
		want    []string // call names that must be extracted
		absent  []string // receiver/object names that must not become calls
	}{
		{
			name:    "go",
			adapter: adapters.NewGoAdapter(),
			source: `package main

func run(s *Store) {
	s.Load()
	pkg.Func()
	s.cache.Get()
	local()
}
`,
			want:   []string{"Load", "Func", "Get", "local"},
			absent: []string{"s", "pkg", "cache"},
		},
		{
			name:    "python",
			adapter: adapters.NewPythonAdapter(),
			source: `class Service:
    def run(self):
        self.helper()
        os.path.join("a", "b")
        self.client.fetch()
        local()
`,
			want:   []string{"helper", "join", "fetch", "local"},
			absent: []string{"self", "os", "client"},
		},
		{
			name:    "javascript",
			adapter: adapters.NewJavaScriptAdapter(),
			source: `class Service {
  run() {
    this.helper();
    api.fetch();
    this.client.send();
    local();
  }
}
`,
			want:   []string{"helper", "fetch", "send", "local"},
			absent: []string{"this", "api", "client"},
		},
		{
			name:    "typescript",
			adapter: adapters.NewTypeScriptAdapter(),
			source: `class Service {
  run(): void {
    this.helper();
    api.fetch();
    this.client.send();
    local();
  }
}
`,
			want:   []string{"helper", "fetch", "send", "local"},
			absent: []string{"this", "api", "client"},
		},
		{
			name:    "java",
			adapter: adapters.NewJavaAdapter(),
			source: `class Service {
  void run() {
    this.helper();
    api.fetch();
    this.client.send();
    local();
  }
}
`,
			want:   []string{"helper", "fetch", "send", "local"},
			absent: []string{"this", "api", "client"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			source := []byte(tt.source)
			tree, err := NewParserPoolManager().GetPool(tt.adapter).Parse(ctx, source)
			require.NoError(t, err)

			deps, err := NewExtractor().ExtractDependencies(ctx, tree, source, tt.adapter, "f")
			require.NoError(t, err)

			calls := map[string]int{}
			for _, d := range deps {
				if d.Kind == "call" {
					calls[d.Symbol]++
				}
			}
			for _, name := range tt.want {
				assert.Equal(t, 1, calls[name], "call %q: want exactly one dep, got %v", name, calls)
			}
			for _, name := range tt.absent {
				assert.Zero(t, calls[name], "object %q must not be a call", name)
			}
		})
	}
}
