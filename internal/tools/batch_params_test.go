package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseStringOrArray_SingleString(t *testing.T) {
	t.Parallel()

	result, err := parseStringOrArray("single-value")
	require.NoError(t, err)
	assert.Equal(t, []string{"single-value"}, result)
}

func TestParseStringOrArray_StringArray(t *testing.T) {
	t.Parallel()

	// Simulate JSON unmarshaling (produces []interface{})
	input := []interface{}{"value1", "value2", "value3"}
	result, err := parseStringOrArray(input)
	require.NoError(t, err)
	assert.Equal(t, []string{"value1", "value2", "value3"}, result)
}

func TestParseStringOrArray_TypedStringSlice(t *testing.T) {
	t.Parallel()

	// Pre-typed string slice
	input := []string{"foo", "bar"}
	result, err := parseStringOrArray(input)
	require.NoError(t, err)
	assert.Equal(t, []string{"foo", "bar"}, result)
}

func TestParseStringOrArray_EmptyArray(t *testing.T) {
	t.Parallel()

	input := []interface{}{}
	result, err := parseStringOrArray(input)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestParseStringOrArray_NilInput(t *testing.T) {
	t.Parallel()

	result, err := parseStringOrArray(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parameter is nil")
	assert.Nil(t, result)
}

func TestParseStringOrArray_InvalidArrayElement(t *testing.T) {
	t.Parallel()

	// Array with non-string element
	input := []interface{}{"valid", 123, "also-valid"}
	result, err := parseStringOrArray(input)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "array element at index 1 is not a string")
	assert.Nil(t, result)
}

func TestParseStringOrArray_InvalidType(t *testing.T) {
	t.Parallel()

	// Wrong type (int)
	result, err := parseStringOrArray(42)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parameter must be string or array of strings")
	assert.Nil(t, result)
}

func TestParseStringOrArray_SerializedJSONArray(t *testing.T) {
	t.Parallel()

	// Agent accidentally passes JSON-serialized array as string
	input := `["file1.go", "file2.go", "file3.go"]`
	result, err := parseStringOrArray(input)
	require.NoError(t, err)
	assert.Equal(t, []string{"file1.go", "file2.go", "file3.go"}, result)
}

func TestParseStringOrArray_SerializedJSONArrayWithWhitespace(t *testing.T) {
	t.Parallel()

	// Serialized array with extra whitespace
	input := `  ["symbol1", "symbol2"]  `
	result, err := parseStringOrArray(input)
	require.NoError(t, err)
	assert.Equal(t, []string{"symbol1", "symbol2"}, result)
}

func TestParseStringOrArray_InvalidJSONArray(t *testing.T) {
	t.Parallel()

	// String that looks like JSON array but isn't valid
	input := `["file1", "file2"`
	result, err := parseStringOrArray(input)
	require.NoError(t, err)
	// Should fall back to treating as literal string
	assert.Equal(t, []string{input}, result)
}

func TestParseStringOrArray_LiteralBracketString(t *testing.T) {
	t.Parallel()

	// Edge case: literal file path that happens to start with [
	input := "[test].txt"
	result, err := parseStringOrArray(input)
	require.NoError(t, err)
	// Should treat as literal string (not valid JSON array)
	assert.Equal(t, []string{input}, result)
}

func TestBatchItemResult_Success(t *testing.T) {
	t.Parallel()

	result := newBatchSuccess("input-value", map[string]any{"key": "data"})
	assert.Equal(t, "input-value", result.Input)
	assert.NotNil(t, result.Data)
	assert.Empty(t, result.Error)
}

func TestBatchItemResult_Error(t *testing.T) {
	t.Parallel()

	result := newBatchError("input-value", "something went wrong")
	assert.Equal(t, "input-value", result.Input)
	assert.Nil(t, result.Data)
	assert.Equal(t, "something went wrong", result.Error)
}

func TestCountSuccesses(t *testing.T) {
	t.Parallel()

	results := []batchItemResult{
		newBatchSuccess("item1", "data1"),
		newBatchError("item2", "error2"),
		newBatchSuccess("item3", "data3"),
		newBatchError("item4", "error4"),
		newBatchSuccess("item5", "data5"),
	}

	count := countSuccesses(results)
	assert.Equal(t, 3, count)
}

func TestCountErrors(t *testing.T) {
	t.Parallel()

	results := []batchItemResult{
		newBatchSuccess("item1", "data1"),
		newBatchError("item2", "error2"),
		newBatchSuccess("item3", "data3"),
		newBatchError("item4", "error4"),
		newBatchSuccess("item5", "data5"),
	}

	count := countErrors(results)
	assert.Equal(t, 2, count)
}

func TestCountSuccesses_EmptySlice(t *testing.T) {
	t.Parallel()

	count := countSuccesses([]batchItemResult{})
	assert.Equal(t, 0, count)
}

func TestCountErrors_AllSuccess(t *testing.T) {
	t.Parallel()

	results := []batchItemResult{
		newBatchSuccess("item1", "data1"),
		newBatchSuccess("item2", "data2"),
	}

	count := countErrors(results)
	assert.Equal(t, 0, count)
}

func TestCountErrors_AllErrors(t *testing.T) {
	t.Parallel()

	results := []batchItemResult{
		newBatchError("item1", "error1"),
		newBatchError("item2", "error2"),
	}

	count := countErrors(results)
	assert.Equal(t, 2, count)
}
