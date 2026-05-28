package embeddings

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestONNXMiniLMProvider(t *testing.T) {
	modelDir := filepath.Join(t.TempDir(), "all-MiniLM-L6-v2")
	writeMiniLMTestTokenizer(t, modelDir)

	fake := &fakeMiniLMRuntime{
		embedFn: func(_ context.Context, in TokenizedInput) ([]float32, error) {
			wantTokens := []string{"[CLS]", "hello", "world", "##s", "[SEP]"}
			if len(in.Tokens) != len(wantTokens) {
				t.Fatalf("unexpected token count: got %v want %v", in.Tokens, wantTokens)
			}
			for i := range wantTokens {
				if in.Tokens[i] != wantTokens[i] {
					t.Fatalf("unexpected tokens: got %v want %v", in.Tokens, wantTokens)
				}
			}
			vec := make([]float32, MiniLMDimension)
			vec[0] = 3
			vec[1] = 4
			return normalize(vec), nil
		},
	}

	p, err := newONNXMiniLMProviderWithRuntime(modelDir, ModelLifecycleOptions{}, func(string) (miniLMRuntime, error) {
		return fake, nil
	})
	if err != nil {
		t.Fatalf("new onnx provider: %v", err)
	}
	if p.Name() != "onnx-minilm-l6-v2" {
		t.Fatalf("unexpected provider name: %s", p.Name())
	}
	vec, err := p.Embed(context.Background(), "Hello worlds")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vec) != MiniLMDimension {
		t.Fatalf("unexpected vector dimension: %d", len(vec))
	}
	if math.Abs(float64(vec[0])-0.6) > 1e-6 || math.Abs(float64(vec[1])-0.8) > 1e-6 {
		t.Fatalf("unexpected pooled vector prefix: %v", vec[:2])
	}
}

type fakeMiniLMRuntime struct {
	embedFn func(context.Context, TokenizedInput) ([]float32, error)
}

func (f *fakeMiniLMRuntime) Embed(ctx context.Context, input TokenizedInput) ([]float32, error) {
	return f.embedFn(ctx, input)
}

func (f *fakeMiniLMRuntime) Close() error { return nil }

func writeMiniLMTestModelFiles(t *testing.T, modelDir string, tokenizerJSON string) {
	t.Helper()
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.onnx"), []byte("onnx"), 0o644); err != nil {
		t.Fatalf("write model.onnx: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "tokenizer.json"), []byte(tokenizerJSON), 0o644); err != nil {
		t.Fatalf("write tokenizer.json: %v", err)
	}
}
