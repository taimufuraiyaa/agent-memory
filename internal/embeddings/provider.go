package embeddings

import "context"

// Provider abstracts embedding backends (local ONNX, cloud, etc.).
type Provider interface {
	Name() string
	ModelVersion() string // Returns version identifier (e.g., "minilm-l6-v2-fp32")
	Dimension() int
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

