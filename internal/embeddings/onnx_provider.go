package embeddings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ONNXMiniLMProvider loads the MiniLM tokenizer and runs inference through ONNX Runtime.
type ONNXMiniLMProvider struct {
	modelDir string

	tokenizer      *WordPieceTokenizer
	runtimeFactory func(string) (miniLMRuntime, error)

	mu      sync.Mutex
	runtime miniLMRuntime
}

// NewONNXMiniLMProvider creates a real ONNX-backed MiniLM provider.
func NewONNXMiniLMProvider(modelDir string, opt ModelLifecycleOptions) (*ONNXMiniLMProvider, error) {
	return newONNXMiniLMProviderWithRuntime(modelDir, opt, newORTMiniLMRuntime)
}

func newONNXMiniLMProviderWithRuntime(modelDir string, opt ModelLifecycleOptions, runtimeFactory func(string) (miniLMRuntime, error)) (*ONNXMiniLMProvider, error) {
	if err := EnsureMiniLMModel(modelDir, opt); err != nil {
		return nil, err
	}
	tokenizer, err := LoadWordPieceTokenizer(modelDir)
	if err != nil {
		return nil, err
	}
	if runtimeFactory == nil {
		return nil, errors.New("runtime factory is required")
	}
	return &ONNXMiniLMProvider{
		modelDir:       modelDir,
		tokenizer:      tokenizer,
		runtimeFactory: runtimeFactory,
	}, nil
}

func (p *ONNXMiniLMProvider) Name() string   { return "onnx-minilm-l6-v2" }
func (p *ONNXMiniLMProvider) Dimension() int { return MiniLMDimension }

func (p *ONNXMiniLMProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("text is required")
	}
	tokenized, err := p.tokenizer.Encode(text)
	if err != nil {
		return nil, err
	}
	rt, err := p.getRuntime()
	if err != nil {
		return nil, err
	}
	return rt.Embed(ctx, tokenized)
}

func (p *ONNXMiniLMProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	rt, err := p.getRuntime()
	if err != nil {
		return nil, err
	}
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			return nil, errors.New("text is required")
		}
		tokenized, err := p.tokenizer.Encode(text)
		if err != nil {
			return nil, err
		}
		vec, err := rt.Embed(ctx, tokenized)
		if err != nil {
			return nil, err
		}
		out = append(out, vec)
	}
	return out, nil
}

func (p *ONNXMiniLMProvider) getRuntime() (miniLMRuntime, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runtime != nil {
		return p.runtime, nil
	}
	rt, err := p.runtimeFactory(p.modelDir)
	if err != nil {
		return nil, fmt.Errorf("create onnx runtime: %w", err)
	}
	p.runtime = rt
	return p.runtime, nil
}
