package embeddings

import "context"

// Provider abstracts embedding backends (local ONNX, cloud, etc.).
type Provider interface {
	Name() string
	Dimension() int
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

