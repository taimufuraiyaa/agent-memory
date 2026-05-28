package embeddings

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// NewProvider resolves the shared production embedding provider selection chain.
// It prefers the real ONNX-backed provider and falls back to the local scaffold
// only when ONNX setup is unavailable.
func NewProvider(modelDir string) (Provider, error) {
	return newProviderWithFactories(modelDir, NewONNXMiniLMProvider, onnxProviderReady, NewLocalProvider)
}

func newProviderWithFactories(
	modelDir string,
	onnxFactory func(string, ModelLifecycleOptions) (*ONNXMiniLMProvider, error),
	onnxReady func(*ONNXMiniLMProvider) error,
	localFactory func(string) (*LocalProvider, error),
) (Provider, error) {
	if strings.TrimSpace(modelDir) == "" {
		return nil, fmt.Errorf("resolve embedding provider: model dir is required")
	}
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return nil, fmt.Errorf("resolve embedding provider: mkdir model dir: %w", err)
	}

	opt := ModelLifecycleOptions{
		AutoDownload: parseBoolEnv(os.Getenv("AGENT_MEMORY_MODEL_AUTODOWNLOAD")),
		URLs: map[string]string{
			"model.onnx":     strings.TrimSpace(os.Getenv("AGENT_MEMORY_MINILM_ONNX_URL")),
			"tokenizer.json": strings.TrimSpace(os.Getenv("AGENT_MEMORY_MINILM_TOKENIZER_URL")),
		},
	}

	onnxProvider, onnxErr := onnxFactory(modelDir, opt)
	if onnxErr == nil {
		if onnxReady == nil {
			return nil, fmt.Errorf("resolve embedding provider: onnx readiness check is required")
		}
		if err := onnxReady(onnxProvider); err == nil {
			return onnxProvider, nil
		} else {
			onnxErr = err
		}
	}

	localProvider, localErr := localFactory(modelDir)
	if localErr == nil {
		return localProvider, nil
	}

	return nil, fmt.Errorf("resolve embedding provider: onnx: %w; local: %v", onnxErr, localErr)
}

func onnxProviderReady(provider *ONNXMiniLMProvider) error {
	if provider == nil {
		return fmt.Errorf("onnx provider is nil")
	}
	if _, err := provider.Embed(context.Background(), "agent-memory provider warmup probe"); err != nil {
		return fmt.Errorf("warm onnx provider: %w", err)
	}
	return nil
}
