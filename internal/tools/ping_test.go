package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPingHandler(t *testing.T) {
	t.Parallel()

	handler := pingHandler()

	result, err := callHandler(t, handler)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "ping should always succeed")
}
