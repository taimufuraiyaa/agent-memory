package modelgateway

import (
	"context"
	"errors"
	"testing"
)

type embeddingFixture struct{ inputs []string }

func (*embeddingFixture) Name() string         { return "local-model" }
func (*embeddingFixture) ModelVersion() string { return "embed-v1" }
func (*embeddingFixture) Dimension() int       { return 2 }
func (*embeddingFixture) Embed(context.Context, string) ([]float32, error) {
	return []float32{1, 0}, nil
}
func (f *embeddingFixture) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	f.inputs = append([]string(nil), texts...)
	return [][]float32{{1, 0}}, nil
}

func TestEmbeddingProviderAdapterPreservesContractAndRejectsGeneration(t *testing.T) {
	underlying := &embeddingFixture{}
	provider, err := NewEmbeddingProvider(underlying, "local-only")
	if err != nil {
		t.Fatal(err)
	}
	vectors, err := provider.EmbedBatch(context.Background(), []string{"source text"})
	if err != nil || len(vectors) != 1 || len(vectors[0]) != 2 || provider.RetentionPolicy() != "local-only" {
		t.Fatalf("vectors=%v retention=%s err=%v", vectors, provider.RetentionPolicy(), err)
	}
	if _, err := provider.Generate(context.Background(), "prompt"); !errors.Is(err, ErrGenerationUnsupported) {
		t.Fatalf("generation error=%v", err)
	}
}

func TestEmbeddingProviderAdapterRequiresExplicitRetentionPolicy(t *testing.T) {
	if _, err := NewEmbeddingProvider(&embeddingFixture{}, ""); err == nil {
		t.Fatal("empty retention policy was accepted")
	}
}
