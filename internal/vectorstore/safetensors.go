package vectorstore

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
)

// TensorInfo describes a single tensor's layout in a safetensors file.
type TensorInfo struct {
	Dtype       string `json:"dtype"`
	Shape       []int  `json:"shape"`
	DataOffsets [2]int `json:"data_offsets"`
}

// SafetensorsFile holds the parsed header and raw bytes of a safetensors file.
type SafetensorsFile struct {
	Header     map[string]TensorInfo
	dataOffset int // byte offset where tensor data starts
	raw        []byte
}

// ParseSafetensors parses a safetensors byte slice.
// Format: [8-byte LE uint64 header_len] [JSON header] [tensor data]
func ParseSafetensors(data []byte) (*SafetensorsFile, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("safetensors: file too short (%d bytes)", len(data))
	}

	headerLen := binary.LittleEndian.Uint64(data[:8])
	if headerLen > 100*1024*1024 {
		return nil, fmt.Errorf("safetensors: header too large (%d bytes)", headerLen)
	}

	headerEnd := 8 + int(headerLen)
	if headerEnd > len(data) {
		return nil, fmt.Errorf("safetensors: header extends beyond file (header_len=%d, file_len=%d)", headerLen, len(data))
	}

	// Parse JSON header. The header may contain a __metadata__ key with string
	// values, so we parse into raw messages first and skip non-tensor entries.
	var rawHeader map[string]json.RawMessage
	if err := json.Unmarshal(data[8:headerEnd], &rawHeader); err != nil {
		return nil, fmt.Errorf("safetensors: failed to parse header JSON: %w", err)
	}

	header := make(map[string]TensorInfo, len(rawHeader))
	for name, raw := range rawHeader {
		if name == "__metadata__" {
			continue
		}
		var info TensorInfo
		if err := json.Unmarshal(raw, &info); err != nil {
			return nil, fmt.Errorf("safetensors: failed to parse tensor %q: %w", name, err)
		}
		header[name] = info
	}

	return &SafetensorsFile{
		Header:     header,
		dataOffset: headerEnd,
		raw:        data,
	}, nil
}

// GetFloat32Tensor extracts a named tensor as []float32.
// Supports F32 (direct) and F16 (converted to F32).
// Returns the data and shape, or an error if the tensor is not found or has
// an unsupported dtype.
func (sf *SafetensorsFile) GetFloat32Tensor(name string) ([]float32, []int, error) {
	info, ok := sf.Header[name]
	if !ok {
		return nil, nil, fmt.Errorf("safetensors: tensor %q not found", name)
	}

	start := sf.dataOffset + info.DataOffsets[0]
	end := sf.dataOffset + info.DataOffsets[1]
	if start > len(sf.raw) || end > len(sf.raw) || start > end {
		return nil, nil, fmt.Errorf("safetensors: tensor %q has invalid data offsets [%d, %d]", name, info.DataOffsets[0], info.DataOffsets[1])
	}

	dataBytes := sf.raw[start:end]
	numElements := 1
	for _, d := range info.Shape {
		numElements *= d
	}

	switch info.Dtype {
	case "F32":
		if len(dataBytes) != numElements*4 {
			return nil, nil, fmt.Errorf("safetensors: tensor %q size mismatch: expected %d bytes, got %d", name, numElements*4, len(dataBytes))
		}
		vals := make([]float32, numElements)
		for i := range vals {
			vals[i] = math.Float32frombits(binary.LittleEndian.Uint32(dataBytes[i*4:]))
		}
		return vals, info.Shape, nil

	case "F16":
		if len(dataBytes) != numElements*2 {
			return nil, nil, fmt.Errorf("safetensors: tensor %q size mismatch: expected %d bytes, got %d", name, numElements*2, len(dataBytes))
		}
		vals := make([]float32, numElements)
		for i := range vals {
			vals[i] = float16ToFloat32(binary.LittleEndian.Uint16(dataBytes[i*2:]))
		}
		return vals, info.Shape, nil

	default:
		return nil, nil, fmt.Errorf("safetensors: unsupported dtype %q for tensor %q", info.Dtype, name)
	}
}

// float16ToFloat32 converts an IEEE 754 half-precision float to float32.
func float16ToFloat32(h uint16) float32 {
	sign := uint32(h>>15) & 1
	exp := uint32(h>>10) & 0x1F
	mant := uint32(h) & 0x3FF

	switch {
	case exp == 0:
		if mant == 0 {
			// Zero (signed)
			return math.Float32frombits(sign << 31)
		}
		// Subnormal: normalize
		for mant&0x400 == 0 {
			mant <<= 1
			exp--
		}
		exp++
		mant &= 0x3FF
		fallthrough
	case exp < 31:
		// Normal number
		exp += 127 - 15
		return math.Float32frombits((sign << 31) | (exp << 23) | (mant << 13))
	default:
		// Inf or NaN
		if mant == 0 {
			return math.Float32frombits((sign << 31) | 0x7F800000)
		}
		return math.Float32frombits((sign << 31) | 0x7F800000 | (mant << 13))
	}
}
