package embeddings

import (
	"math"
	"testing"
)

func TestMeanPoolHiddenState(t *testing.T) {
	data := make([]float32, 4*MiniLMDimension)
	data[0] = 2
	data[1] = 1
	data[MiniLMDimension+0] = 4
	data[MiniLMDimension+1] = -1

	got, err := meanPoolHiddenState(data, []int64{1, 4, MiniLMDimension}, []int64{1, 1, 0, 0})
	if err != nil {
		t.Fatalf("mean pool: %v", err)
	}
	if len(got) != MiniLMDimension {
		t.Fatalf("unexpected output dimension: %d", len(got))
	}

	if math.Abs(float64(got[0])-1) > 1e-6 {
		t.Fatalf("unexpected normalized dim 0: %f", got[0])
	}
	if math.Abs(float64(got[1])) > 1e-6 {
		t.Fatalf("unexpected normalized dim 1: %f", got[1])
	}
}
