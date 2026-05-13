package embeddings

import "context"

// ONNXMiniLMProvider is a model-lifecycle aware local provider scaffold.
// It preserves the provider contract while full ONNX runtime inference remains optional.
type ONNXMiniLMProvider struct {
	local *LocalProvider
}

// NewONNXMiniLMProvider creates an ONNX MiniLM provider scaffold.
func NewONNXMiniLMProvider(modelDir string, opt ModelLifecycleOptions) (*ONNXMiniLMProvider, error) {
	if err := EnsureMiniLMModel(modelDir, opt); err != nil {
		return nil, err
	}
	lp, err := NewLocalProvider(modelDir)
	if err != nil {
		return nil, err
	}
	return &ONNXMiniLMProvider{local: lp}, nil
}

func (p *ONNXMiniLMProvider) Name() string   { return "onnx-minilm-scaffold" }
func (p *ONNXMiniLMProvider) Dimension() int { return MiniLMDimension }

func (p *ONNXMiniLMProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	return p.local.Embed(ctx, text)
}

func (p *ONNXMiniLMProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return p.local.EmbedBatch(ctx, texts)
}
