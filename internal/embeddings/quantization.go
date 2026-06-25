package embeddings

import (
	"errors"
	"math"
)

// nearestPowerOf2 returns the smallest power of 2 greater than or equal to n.
func nearestPowerOf2(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p *= 2
	}
	return p
}

// FWHT computes the in-place Fast Walsh-Hadamard Transform of slice x.
// The length of x must be a power of 2.
func FWHT(x []float32) {
	n := len(x)
	for h := 1; h < n; h *= 2 {
		for i := 0; i < n; i += 2 * h {
			for j := i; j < i+h; j++ {
				u := x[j]
				v := x[j+h]
				x[j] = u + v
				x[j+h] = u - v
			}
		}
	}
}

// IFWHT computes the in-place Inverse Fast Walsh-Hadamard Transform of slice x.
// The length of x must be a power of 2.
func IFWHT(x []float32) {
	FWHT(x)
	n := float32(len(x))
	for i := 0; i < len(x); i++ {
		x[i] /= n
	}
}

// QuantizedVector holds a quantized embedding with its scale factor and original dimension.
type QuantizedVector struct {
	Bytes       []int8
	Scale       float32
	OriginalDim int
}

// QuantizeTurbo pads, rotates (via FWHT), and quantizes a vector to 8-bit signed integers.
func QuantizeTurbo(vec []float32) (*QuantizedVector, error) {
	originalDim := len(vec)
	if originalDim == 0 {
		return nil, errors.New("cannot quantize empty vector")
	}

	n := nearestPowerOf2(originalDim)
	padded := make([]float32, n)
	copy(padded, vec) // Zeros remain at the end

	FWHT(padded)

	var maxAbs float64
	for _, val := range padded {
		absVal := math.Abs(float64(val))
		if absVal > maxAbs {
			maxAbs = absVal
		}
	}

	scale := float32(0.0)
	qbytes := make([]int8, n)
	if maxAbs > 0 {
		scale = float32(maxAbs / 127.0)
		for i, val := range padded {
			qval := math.Round(float64(val) / float64(scale))
			if qval > 127 {
				qval = 127
			} else if qval < -128 {
				qval = -128
			}
			qbytes[i] = int8(qval)
		}
	}

	return &QuantizedVector{
		Bytes:       qbytes,
		Scale:       scale,
		OriginalDim: originalDim,
	}, nil
}

// DequantizeTurbo reconstructs the original vector from its quantized representation.
func DequantizeTurbo(qv *QuantizedVector) ([]float32, error) {
	if qv == nil || len(qv.Bytes) == 0 {
		return nil, errors.New("invalid quantized vector")
	}

	padded := make([]float32, len(qv.Bytes))
	for i, qb := range qv.Bytes {
		padded[i] = float32(qb) * qv.Scale
	}

	IFWHT(padded)

	// Truncate back to the original dimension
	if qv.OriginalDim > len(padded) {
		return nil, errors.New("original dimension exceeds quantized slice size")
	}
	return padded[:qv.OriginalDim], nil
}
