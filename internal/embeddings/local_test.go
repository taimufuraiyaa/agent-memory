package embeddings

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalProviderEmbedDeterministic(t *testing.T) {
	modelDir := filepath.Join(t.TempDir(), "all-MiniLM-L6-v2")
	if err := ensureDir(modelDir); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p, err := NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	v1, err := p.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("embed #1: %v", err)
	}
	v2, err := p.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("embed #2: %v", err)
	}
	if len(v1) != MiniLMDimension || len(v2) != MiniLMDimension {
		t.Fatalf("unexpected dimensions: %d %d", len(v1), len(v2))
	}
	if !vectorsEqual(v1, v2) {
		t.Fatalf("expected deterministic vectors")
	}

	// Check unit normalization (allow tiny floating-point tolerance).
	var sum float64
	for _, x := range v1 {
		sum += float64(x * x)
	}
	if math.Abs(math.Sqrt(sum)-1.0) > 1e-4 {
		t.Fatalf("vector not normalized: %.8f", math.Sqrt(sum))
	}
}

func TestEmbedBatch(t *testing.T) {
	modelDir := filepath.Join(t.TempDir(), "all-MiniLM-L6-v2")
	if err := ensureDir(modelDir); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p, err := NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	vecs, err := p.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("embed batch: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(vecs))
	}
	for i := range vecs {
		if len(vecs[i]) != MiniLMDimension {
			t.Fatalf("vector %d has bad dimension %d", i, len(vecs[i]))
		}
	}
}

func TestCosineStability(t *testing.T) {
	modelDir := filepath.Join(t.TempDir(), "all-MiniLM-L6-v2")
	if err := ensureDir(modelDir); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p, err := NewLocalProvider(modelDir)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	a, _ := p.Embed(context.Background(), "orders service consumes events")
	b, _ := p.Embed(context.Background(), "orders service consumes events")
	c, _ := p.Embed(context.Background(), "unrelated random text")

	ab, err := Cosine(a, b)
	if err != nil {
		t.Fatalf("cosine ab: %v", err)
	}
	ac, err := Cosine(a, c)
	if err != nil {
		t.Fatalf("cosine ac: %v", err)
	}
	if ab < ac {
		t.Fatalf("expected equal text similarity to be higher, got ab=%.4f ac=%.4f", ab, ac)
	}
}

func ensureDir(path string) error {
	return os.MkdirAll(path, 0o755)
}

func vectorsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func BenchmarkLocalProviderEmbedBatch100(b *testing.B) {
	modelDir := filepath.Join(b.TempDir(), "all-MiniLM-L6-v2")
	if err := ensureDir(modelDir); err != nil {
		b.Fatalf("mkdir: %v", err)
	}
	p, err := NewLocalProvider(modelDir)
	if err != nil {
		b.Fatalf("new provider: %v", err)
	}
	texts := make([]string, 100)
	for i := range texts {
		texts[i] = fmt.Sprintf("orders service event #%d retries and observability notes", i)
	}
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := p.EmbedBatch(ctx, texts)
		if err != nil {
			b.Fatalf("embed batch: %v", err)
		}
	}
}
