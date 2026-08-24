package embeddings

import (
	"errors"
	"math"
)

// Cosine computes cosine similarity for same-sized vectors.
func Cosine(a, b []float32) (float64, error) {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0, errors.New("vectors must be same non-zero length")
	}
	var dot, na, nb float64
	for i := range a {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0, nil
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb)), nil
}
