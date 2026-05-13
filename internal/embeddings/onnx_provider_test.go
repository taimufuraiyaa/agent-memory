package embeddings

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestONNXMiniLMProvider(t *testing.T) {
	modelDir := filepath.Join(t.TempDir(), "all-MiniLM-L6-v2")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.onnx"), []byte("onnx"), 0o644); err != nil {
		t.Fatalf("write model.onnx: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "tokenizer.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write tokenizer.json: %v", err)
	}
	p, err := NewONNXMiniLMProvider(modelDir, ModelLifecycleOptions{})
	if err != nil {
		t.Fatalf("new onnx provider: %v", err)
	}
	if p.Name() != "onnx-minilm-scaffold" {
		t.Fatalf("unexpected provider name: %s", p.Name())
	}
	vec, err := p.Embed(context.Background(), "orders event")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vec) != MiniLMDimension {
		t.Fatalf("unexpected vector dimension: %d", len(vec))
	}
}
