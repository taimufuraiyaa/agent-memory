package embeddings

import (
	"errors"
	"strings"
	"testing"
)

func TestNewProviderFailsWhenONNXConstructionFails(t *testing.T) {
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
	)
	if err == nil {
		t.Fatal("expected provider resolution to fail")
	}
	if got != nil {
		t.Fatalf("expected nil provider, got %#v", got)
	}
	if !strings.Contains(err.Error(), "onnx provider unavailable") {
		t.Fatalf("unexpected error: %v", err)
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
	)
	if err != nil {
		t.Fatalf("resolve provider: %v", err)
	}
	if got != want {
		t.Fatalf("expected onnx provider instance")
	}
}

func TestNewProviderFailsWhenONNXWarmupFails(t *testing.T) {
	modelDir := t.TempDir()

	got, err := newProviderWithFactories(
		modelDir,
		func(string, ModelLifecycleOptions) (*ONNXMiniLMProvider, error) {
			return &ONNXMiniLMProvider{}, nil
		},
		func(*ONNXMiniLMProvider) error {
			return errors.New("runtime init failed")
		},
	)
	if err == nil {
		t.Fatal("expected provider resolution to fail")
	}
	if got != nil {
		t.Fatalf("expected nil provider, got %#v", got)
	}
	if !strings.Contains(err.Error(), "onnx provider not ready") {
		t.Fatalf("unexpected error: %v", err)
	}
}
