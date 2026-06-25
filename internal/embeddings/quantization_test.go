package embeddings

import (
	"math"
	"math/rand"
	"testing"
)

func TestQuantizationCorrectness(t *testing.T) {
	// Generate random 384-dimension vector
	rand.Seed(42)
	dims := []int{384, 1536, 128, 64}

	for _, dim := range dims {
		vec := make([]float32, dim)
		for i := 0; i < dim; i++ {
			vec[i] = rand.Float32()*2.0 - 1.0 // Range [-1.0, 1.0]
		}

		qv, err := QuantizeTurbo(vec)
		if err != nil {
			t.Fatalf("Failed to quantize: %v", err)
		}

		reconstructed, err := DequantizeTurbo(qv)
		if err != nil {
			t.Fatalf("Failed to dequantize: %v", err)
		}

		if len(reconstructed) != dim {
			t.Errorf("Expected length %d, got %d", dim, len(reconstructed))
		}

		// Calculate similarity between original and reconstructed
		sim, err := Cosine(vec, reconstructed)
		if err != nil {
			t.Fatalf("Failed to calculate cosine: %v", err)
		}

		if sim < 0.99 {
			t.Errorf("Expected high similarity, got %f for dimension %d", sim, dim)
		}
	}
}

func TestFWHT(t *testing.T) {
	x := []float32{1.0, 2.0, 3.0, 4.0}
	expected := []float32{10.0, -2.0, -4.0, 0.0}

	FWHT(x)

	for i, v := range expected {
		if x[i] != v {
			t.Errorf("FWHT element %d expected %f, got %f", i, v, x[i])
		}
	}

	IFWHT(x)
	original := []float32{1.0, 2.0, 3.0, 4.0}
	for i, v := range original {
		if mathAbs(x[i]-v) > 1e-5 {
			t.Errorf("IFWHT element %d expected %f, got %f", i, v, x[i])
		}
	}
}

func mathAbs(a float32) float32 {
	if a < 0 {
		return -a
	}
	return a
}

func TestQuantizedCosineSimilarity(t *testing.T) {
	rand.Seed(1337)
	dim := 384
	vec1 := make([]float32, dim)
	vec2 := make([]float32, dim)
	for i := 0; i < dim; i++ {
		vec1[i] = rand.Float32()*2.0 - 1.0
		vec2[i] = rand.Float32()*2.0 - 1.0
	}

	originalCos, err := Cosine(vec1, vec2)
	if err != nil {
		t.Fatalf("Failed to compute original cosine: %v", err)
	}

	q1, _ := QuantizeTurbo(vec1)
	q2, _ := QuantizeTurbo(vec2)

	// Compute similarity in quantized space
	var dot, qnorm, enorm float64
	for i := 0; i < len(q1.Bytes); i++ {
		v1 := float64(q1.Bytes[i])
		v2 := float64(q2.Bytes[i])
		dot += v1 * v2
		qnorm += v1 * v1
		enorm += v2 * v2
	}
	quantCos := dot / (math.Sqrt(qnorm) * math.Sqrt(enorm))

	diff := math.Abs(originalCos - quantCos)
	t.Logf("Original: %f, Quantized: %f, Diff: %f", originalCos, quantCos, diff)
	if diff > 0.05 {
		t.Errorf("Quantized cosine similarity diff is too large: %f", diff)
	}
}
