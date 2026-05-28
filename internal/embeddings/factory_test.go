package embeddings

import (
	"errors"
	"testing"
)

func TestNewProviderFallsBackToLocal(t *testing.T) {
	modelDir := t.TempDir()

	got, err := newProviderWithFactories(
		modelDir,
		func(string, ModelLifecycleOptions) (*ONNXMiniLMProvider, error) {
			return nil, errors.New("runtime missing")
		},
		func(*ONNXMiniLMProvider) error {
			t.Fatal("onnx readiness should not run when construction fails")
			return nil
		},
		func(dir string) (*LocalProvider, error) {
			return &LocalProvider{modelDir: dir}, nil
		},
	)
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}
	if got.Name() != "local-minilm-scaffold" {
		t.Fatalf("unexpected provider: %s", got.Name())
	}
}

func TestNewProviderPrefersONNX(t *testing.T) {
	modelDir := t.TempDir()
	want := &ONNXMiniLMProvider{}

	got, err := newProviderWithFactories(
		modelDir,
		func(string, ModelLifecycleOptions) (*ONNXMiniLMProvider, error) {
			return want, nil
		},
		func(got *ONNXMiniLMProvider) error {
			if got != want {
				t.Fatal("readiness check received unexpected provider")
			}
			return nil
		},
		func(string) (*LocalProvider, error) {
			t.Fatal("local fallback should not be used when onnx succeeds")
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}
	if got != want {
		t.Fatalf("expected onnx provider instance")
	}
}

func TestNewProviderFallsBackToLocalWhenONNXWarmupFails(t *testing.T) {
	modelDir := t.TempDir()

	got, err := newProviderWithFactories(
		modelDir,
		func(string, ModelLifecycleOptions) (*ONNXMiniLMProvider, error) {
			return &ONNXMiniLMProvider{}, nil
		},
		func(*ONNXMiniLMProvider) error {
			return errors.New("runtime init failed")
		},
		func(dir string) (*LocalProvider, error) {
			return &LocalProvider{modelDir: dir}, nil
		},
	)
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}
	if got.Name() != "local-minilm-scaffold" {
		t.Fatalf("unexpected provider: %s", got.Name())
	}
}
