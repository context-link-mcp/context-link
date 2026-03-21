package vectorstore

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildSafetensors constructs a minimal safetensors file in memory.
func buildSafetensors(t *testing.T, tensors map[string]struct {
	Dtype string
	Shape []int
	Data  []byte
}) []byte {
	t.Helper()

	// Build header and data block.
	header := make(map[string]TensorInfo)
	var dataBlock []byte
	for name, tensor := range tensors {
		start := len(dataBlock)
		dataBlock = append(dataBlock, tensor.Data...)
		header[name] = TensorInfo{
			Dtype:       tensor.Dtype,
			Shape:       tensor.Shape,
			DataOffsets: [2]int{start, len(dataBlock)},
		}
	}

	headerJSON, err := json.Marshal(header)
	require.NoError(t, err)

	// 8-byte LE header length + JSON + data
	buf := make([]byte, 8+len(headerJSON)+len(dataBlock))
	binary.LittleEndian.PutUint64(buf[:8], uint64(len(headerJSON)))
	copy(buf[8:], headerJSON)
	copy(buf[8+len(headerJSON):], dataBlock)

	return buf
}

func float32Bytes(vals ...float32) []byte {
	buf := make([]byte, len(vals)*4)
	for i, v := range vals {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

func float16Bytes(vals ...float32) []byte {
	buf := make([]byte, len(vals)*2)
	for i, v := range vals {
		binary.LittleEndian.PutUint16(buf[i*2:], float32ToFloat16(v))
	}
	return buf
}

func float32ToFloat16(f float32) uint16 {
	bits := math.Float32bits(f)
	sign := uint16((bits >> 16) & 0x8000)
	exp := int((bits>>23)&0xFF) - 127 + 15
	mant := uint16((bits >> 13) & 0x3FF)
	if exp <= 0 {
		return sign
	}
	if exp >= 31 {
		return sign | 0x7C00
	}
	return sign | uint16(exp)<<10 | mant
}

func TestParseSafetensors_ValidF32(t *testing.T) {
	t.Parallel()

	data := buildSafetensors(t, map[string]struct {
		Dtype string
		Shape []int
		Data  []byte
	}{
		"embeddings": {
			Dtype: "F32",
			Shape: []int{2, 3},
			Data:  float32Bytes(1.0, 2.0, 3.0, 4.0, 5.0, 6.0),
		},
	})

	sf, err := ParseSafetensors(data)
	require.NoError(t, err)
	require.NotNil(t, sf)

	vals, shape, err := sf.GetFloat32Tensor("embeddings")
	require.NoError(t, err)
	assert.Equal(t, []int{2, 3}, shape)
	assert.Equal(t, []float32{1.0, 2.0, 3.0, 4.0, 5.0, 6.0}, vals)
}

func TestParseSafetensors_F16Conversion(t *testing.T) {
	t.Parallel()

	data := buildSafetensors(t, map[string]struct {
		Dtype string
		Shape []int
		Data  []byte
	}{
		"weights": {
			Dtype: "F16",
			Shape: []int{3},
			Data:  float16Bytes(1.0, 0.5, -2.0),
		},
	})

	sf, err := ParseSafetensors(data)
	require.NoError(t, err)

	vals, shape, err := sf.GetFloat32Tensor("weights")
	require.NoError(t, err)
	assert.Equal(t, []int{3}, shape)
	assert.InDelta(t, 1.0, vals[0], 0.01)
	assert.InDelta(t, 0.5, vals[1], 0.01)
	assert.InDelta(t, -2.0, vals[2], 0.01)
}

func TestParseSafetensors_TensorNotFound(t *testing.T) {
	t.Parallel()

	data := buildSafetensors(t, map[string]struct {
		Dtype string
		Shape []int
		Data  []byte
	}{
		"a": {Dtype: "F32", Shape: []int{1}, Data: float32Bytes(1.0)},
	})

	sf, err := ParseSafetensors(data)
	require.NoError(t, err)

	_, _, err = sf.GetFloat32Tensor("missing")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestParseSafetensors_UnsupportedDtype(t *testing.T) {
	t.Parallel()

	data := buildSafetensors(t, map[string]struct {
		Dtype string
		Shape []int
		Data  []byte
	}{
		"x": {Dtype: "I8", Shape: []int{4}, Data: []byte{1, 2, 3, 4}},
	})

	sf, err := ParseSafetensors(data)
	require.NoError(t, err)

	_, _, err = sf.GetFloat32Tensor("x")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported dtype")
}

func TestParseSafetensors_TooShort(t *testing.T) {
	t.Parallel()

	_, err := ParseSafetensors([]byte{1, 2, 3})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

func TestParseSafetensors_MalformedJSON(t *testing.T) {
	t.Parallel()

	buf := make([]byte, 8+5)
	binary.LittleEndian.PutUint64(buf[:8], 5)
	copy(buf[8:], "bad!!")

	_, err := ParseSafetensors(buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse header")
}

func TestParseSafetensors_MetadataKeySkipped(t *testing.T) {
	t.Parallel()

	// Build manually with __metadata__
	tensorData := float32Bytes(1.0, 2.0)
	header := map[string]interface{}{
		"__metadata__": map[string]string{"format": "pt"},
		"emb": TensorInfo{
			Dtype:       "F32",
			Shape:       []int{2},
			DataOffsets: [2]int{0, 8},
		},
	}
	headerJSON, err := json.Marshal(header)
	require.NoError(t, err)

	buf := make([]byte, 8+len(headerJSON)+len(tensorData))
	binary.LittleEndian.PutUint64(buf[:8], uint64(len(headerJSON)))
	copy(buf[8:], headerJSON)
	copy(buf[8+len(headerJSON):], tensorData)

	sf, err := ParseSafetensors(buf)
	require.NoError(t, err)
	assert.NotContains(t, sf.Header, "__metadata__")

	vals, _, err := sf.GetFloat32Tensor("emb")
	require.NoError(t, err)
	assert.Equal(t, []float32{1.0, 2.0}, vals)
}

func TestParseSafetensors_ShapeMismatch(t *testing.T) {
	t.Parallel()

	// Shape says 3 elements but only provide 2 floats
	data := buildSafetensors(t, map[string]struct {
		Dtype string
		Shape []int
		Data  []byte
	}{
		"bad": {Dtype: "F32", Shape: []int{3}, Data: float32Bytes(1.0, 2.0)},
	})

	sf, err := ParseSafetensors(data)
	require.NoError(t, err)

	_, _, err = sf.GetFloat32Tensor("bad")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "size mismatch")
}

func TestFloat16ToFloat32_SpecialValues(t *testing.T) {
	t.Parallel()

	// Zero
	assert.Equal(t, float32(0), float16ToFloat32(0x0000))
	// Negative zero
	assert.Equal(t, float32(math.Float32frombits(0x80000000)), float16ToFloat32(0x8000))
	// Positive infinity
	assert.True(t, math.IsInf(float64(float16ToFloat32(0x7C00)), 1))
	// NaN
	assert.True(t, math.IsNaN(float64(float16ToFloat32(0x7C01))))
}
