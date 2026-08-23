package embeddings

import (
	"context"
	"errors"
	"strings"
)

type miniLMRuntime interface {
	Embed(context.Context, TokenizedInput) ([]float32, error)
	Close() error
}

type fakeTestMiniLMRuntime struct{}

func newFakeMiniLMRuntime(modelDir string) (miniLMRuntime, error) {
	if strings.TrimSpace(modelDir) == "" {
		return nil, errors.New("model dir is required")
	}
	return &fakeTestMiniLMRuntime{}, nil
}

func (f *fakeTestMiniLMRuntime) Close() error { return nil }

func (f *fakeTestMiniLMRuntime) Embed(ctx context.Context, input TokenizedInput) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vec := make([]float32, MiniLMDimension)
	for _, token := range input.Tokens {
		if strings.TrimSpace(token) != "" {
			addTokenVector(vec, token, 1.0)
		}
	}
	return normalize(vec), nil
}
