package modelgateway

import (
	"context"
	"errors"
	"strings"
)

var ErrGenerationUnsupported = errors.New("generation is not supported by this provider")

// EmbeddingProvider adapts the shared embedding contract to the hosted model
// gateway so callers cannot bypass tenant policy and usage metering.
type EmbeddingProvider struct {
	provider  EmbeddingBackend
	retention string
}

type EmbeddingBackend interface {
	Name() string
	ModelVersion() string
	Dimension() int
	Embed(context.Context, string) ([]float32, error)
	EmbedBatch(context.Context, []string) ([][]float32, error)
}

func NewEmbeddingProvider(provider EmbeddingBackend, retention string) (*EmbeddingProvider, error) {
	if provider == nil || strings.TrimSpace(retention) == "" {
		return nil, errors.New("embedding provider and retention policy are required")
	}
	return &EmbeddingProvider{provider: provider, retention: retention}, nil
}

func (p *EmbeddingProvider) Name() string            { return p.provider.Name() }
func (p *EmbeddingProvider) ModelVersion() string    { return p.provider.ModelVersion() }
func (p *EmbeddingProvider) RetentionPolicy() string { return p.retention }
func (p *EmbeddingProvider) Dimension() int          { return p.provider.Dimension() }
func (p *EmbeddingProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	return p.provider.EmbedBatch(ctx, texts)
}
func (*EmbeddingProvider) Generate(context.Context, string) (string, error) {
	return "", ErrGenerationUnsupported
}

type RedactorFunc func(string) string

func (f RedactorFunc) Redact(value string) string { return f(value) }
