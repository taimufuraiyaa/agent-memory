package embeddings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/time/timebooks/agent-memory/internal/observability"
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
	runtimeFactory := newORTMiniLMRuntime
	if parseBoolEnv(os.Getenv("AGENT_MEMORY_TEST_FAKE_ONNX_RUNTIME")) {
		runtimeFactory = newFakeMiniLMRuntime
	}
	return newONNXMiniLMProviderWithRuntime(modelDir, opt, runtimeFactory)
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

func (p *ONNXMiniLMProvider) Name() string          { return "onnx-minilm-l6-v2" }
func (p *ONNXMiniLMProvider) ModelVersion() string  { return "minilm-l6-v2-fp32" }
func (p *ONNXMiniLMProvider) Dimension() int        { return MiniLMDimension }

func (p *ONNXMiniLMProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	ctx, span := observability.StartSpan(ctx, "agent-memory.embed")
	defer span.End()
	observability.SetSpanAttributes(ctx,
		observability.ProviderAttr(p.Name()),
		observability.BatchSizeAttr(1),
	)

	if strings.TrimSpace(text) == "" {
		err := errors.New("text is required")
		observability.RecordSpanError(ctx, err)
		return nil, err
	}
	tokenized, err := p.tokenizer.Encode(text)
	if err != nil {
		observability.RecordSpanError(ctx, err)
		return nil, err
	}
	rt, err := p.getRuntime()
	if err != nil {
		observability.RecordSpanError(ctx, err)
		return nil, err
	}
	vec, err := rt.Embed(ctx, tokenized)
	if err != nil {
		observability.RecordSpanError(ctx, err)
		return nil, err
	}
	return vec, nil
}

func (p *ONNXMiniLMProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	ctx, span := observability.StartSpan(ctx, "agent-memory.embed")
	defer span.End()
	observability.SetSpanAttributes(ctx,
		observability.ProviderAttr(p.Name()),
		observability.BatchSizeAttr(len(texts)),
	)

	rt, err := p.getRuntime()
	if err != nil {
		observability.RecordSpanError(ctx, err)
		return nil, err
	}
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			err := errors.New("text is required")
			observability.RecordSpanError(ctx, err)
			return nil, err
		}
		tokenized, err := p.tokenizer.Encode(text)
		if err != nil {
			observability.RecordSpanError(ctx, err)
			return nil, err
		}
		vec, err := rt.Embed(ctx, tokenized)
		if err != nil {
			observability.RecordSpanError(ctx, err)
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
