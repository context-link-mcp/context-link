package vectorstore

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawSafetensors builds a safetensors file from a literal JSON header, so
// tests can express offsets and shapes json.Marshal of TensorInfo would not.
func rawSafetensors(headerJSON string, data []byte) []byte {
	buf := make([]byte, 8+len(headerJSON)+len(data))
	binary.LittleEndian.PutUint64(buf[:8], uint64(len(headerJSON)))
	copy(buf[8:], headerJSON)
	copy(buf[8+len(headerJSON):], data)
	return buf
}

// TestGetFloat32Tensor_MalformedHeader covers hostile tensor headers. On
// master the negative-offset, negative-dimension and overflowing-shape cases
// panicked (slice out of range / makeslice), and the offset-into-header case
// silently read header bytes as tensor data.
func TestGetFloat32Tensor_MalformedHeader(t *testing.T) {
	t.Parallel()

	data := make([]byte, 16) // four F32 zeros
	tests := []struct {
		name   string
		header string
		ok     bool
	}{
		{"valid", `{"w":{"dtype":"F32","shape":[2,2],"data_offsets":[0,16]}}`, true},
		{"valid F16", `{"w":{"dtype":"F16","shape":[8],"data_offsets":[0,16]}}`, true},
		{"empty tensor", `{"w":{"dtype":"F32","shape":[0,4],"data_offsets":[0,0]}}`, true},
		{"negative start offset", `{"w":{"dtype":"F32","shape":[4],"data_offsets":[-16,0]}}`, false},
		{"start offset reaching into the header", `{"w":{"dtype":"F32","shape":[1],"data_offsets":[-4,0]}}`, false},
		{"negative end offset", `{"w":{"dtype":"F32","shape":[0],"data_offsets":[0,-4]}}`, false},
		{"end beyond data", `{"w":{"dtype":"F32","shape":[5],"data_offsets":[0,20]}}`, false},
		{"start after end", `{"w":{"dtype":"F32","shape":[1],"data_offsets":[8,4]}}`, false},
		{"huge offsets", `{"w":{"dtype":"F32","shape":[1],"data_offsets":[9223372036854775800,9223372036854775807]}}`, false},
		{"negative dimension", `{"w":{"dtype":"F32","shape":[-1],"data_offsets":[0,16]}}`, false},
		{"two negative dimensions", `{"w":{"dtype":"F32","shape":[-2,-2],"data_offsets":[0,16]}}`, false},
		{"overflowing shape", `{"w":{"dtype":"F32","shape":[4611686018427387904,4],"data_offsets":[0,16]}}`, false},
		{"shape times element size overflows", `{"w":{"dtype":"F32","shape":[4611686018427387908],"data_offsets":[0,16]}}`, false},
		{"F16 shape times element size overflows", `{"w":{"dtype":"F16","shape":[4611686018427387912],"data_offsets":[0,16]}}`, false},
		{"size mismatch", `{"w":{"dtype":"F32","shape":[3],"data_offsets":[0,16]}}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sf, err := ParseSafetensors(rawSafetensors(tt.header, data))
			require.NoError(t, err)
			var vals []float32
			assert.NotPanics(t, func() { vals, _, err = sf.GetFloat32Tensor("w") })
			if tt.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Nil(t, vals)
			}
		})
	}
}

func TestElementCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		shape   []int
		want    int
		wantErr bool
	}{
		{nil, 1, false},
		{[]int{3, 4}, 12, false},
		{[]int{0, 1 << 62}, 0, false},
		{[]int{-1}, 0, true},
		{[]int{1 << 32, 1 << 32}, 0, true},
	}
	for _, tt := range tests {
		got, err := elementCount(tt.shape)
		if tt.wantErr {
			assert.Error(t, err, "%v", tt.shape)
			continue
		}
		assert.NoError(t, err, "%v", tt.shape)
		assert.Equal(t, tt.want, got, "%v", tt.shape)
	}
}

// FuzzSafetensors checks that no input makes parsing or tensor extraction
// panic. `go test` runs the seed corpus; `go test -fuzz=FuzzSafetensors`
// explores further.
func FuzzSafetensors(f *testing.F) {
	f.Add(rawSafetensors(`{"w":{"dtype":"F32","shape":[2],"data_offsets":[0,8]}}`, make([]byte, 8)))
	f.Add(rawSafetensors(`{"w":{"dtype":"F16","shape":[-3],"data_offsets":[-8,2]}}`, make([]byte, 8)))
	f.Add(rawSafetensors(`{"w":{"dtype":"F32","shape":[4611686018427387904,4],"data_offsets":[0,8]}}`, nil))
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		sf, err := ParseSafetensors(data)
		if err != nil {
			return
		}
		for name := range sf.Header {
			_, _, _ = sf.GetFloat32Tensor(name)
		}
	})
}
