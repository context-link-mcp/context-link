package indexer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildSymbolKeywords(t *testing.T) {
	t.Parallel()

	// 40 callees named aaa00..zzz39 spread over the alphabet.
	var manyCallees []string
	for i := 0; i < 40; i++ {
		manyCallees = append(manyCallees, fmt.Sprintf("%c%c%c%02d", 'a'+i%26, 'a'+i%26, 'a'+i%26, i))
	}

	tests := []struct {
		name        string
		code        string
		kind        string
		callees     []string
		wantPrefix  []string // the first keywords, in order
		wantContain []string
		wantLen     int // 0 = don't check
	}{
		{
			name:       "signature types and body patterns kept before callees",
			code:       "func Zeta(r Request) error {\n\tretry()\n\treturn ZoneTimeout\n}",
			kind:       "function",
			callees:    manyCallees,
			wantPrefix: []string{"zonetimeout", "retry", "timeout", "zeta", "request", "aaa00"},
			wantLen:    maxKeywords,
		},
		{
			name:        "late-alphabet error kept despite many early callees",
			code:        "func f() {\n\tthrow new ZookeeperError()\n}",
			kind:        "function",
			callees:     manyCallees,
			wantContain: []string{"zookeepererror"},
			wantLen:     maxKeywords,
		},
		{
			name:       "initializer identifiers keep order of appearance",
			code:       "const zulu = makeAlpha(yankee)",
			kind:       "constant",
			wantPrefix: []string{"zulu", "makealpha", "yankee"},
			wantLen:    3,
		},
		{
			name:    "short and duplicate keywords dropped",
			code:    "x",
			kind:    "function",
			callees: []string{"go", "Run", "run", "RUN"},
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := BuildSymbolKeywords(tt.code, tt.kind, tt.callees)
			fields := strings.Fields(got)
			if tt.wantLen > 0 {
				assert.Len(t, fields, tt.wantLen)
			}
			if len(tt.wantPrefix) > 0 {
				assert.GreaterOrEqual(t, len(fields), len(tt.wantPrefix))
				assert.Equal(t, tt.wantPrefix, fields[:min(len(fields), len(tt.wantPrefix))])
			}
			for _, kw := range tt.wantContain {
				assert.Contains(t, fields, kw)
			}
		})
	}
}

func TestBuildSymbolKeywords_Deterministic(t *testing.T) {
	t.Parallel()

	code := "func handle(req Request, cfg Config) error {\n\treturn fallback()\n}"
	a := BuildSymbolKeywords(code, "function", []string{"zeta", "alpha", "mid"})
	b := BuildSymbolKeywords(code, "function", []string{"mid", "zeta", "alpha"})
	assert.Equal(t, a, b, "callee order from the database must not change the result")
	assert.Equal(t, "fallback request config alpha mid zeta", a)
}
