package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/bootstrap"
)

const localGraphEmbeddingModel = "qwen3-embedding:0.6b"

var (
	ensureOllamaGraphModel = bootstrap.EnsureOllamaPlanner
	checkLocalGraphAdapter = localGraphAdapterReadiness
)

func graphCompletionModelIDs() []string {
	options := defaultGraphModelOptions(nil)
	models := make([]string, 0, len(options))
	for _, option := range options {
		models = append(models, option.Model)
	}
	return models
}

func graphModelIDs() []string {
	models := graphCompletionModelIDs()
	return append(models, localGraphEmbeddingModel)
}

func localOllamaModelIDs() []string {
	models := append([]string(nil), localLLMModelIDs()...)
	return append(models, graphModelIDs()...)
}

func filterModelInventory(inventory map[string]bool, modelIDs []string) map[string]bool {
	filtered := make(map[string]bool, len(modelIDs))
	for _, model := range modelIDs {
		filtered[model] = inventory[model]
	}
	return filtered
}

func resolveHeadlessGraphModel(model string) (string, error) {
	model = strings.TrimSpace(model)
	if model == "" || strings.EqualFold(model, "none") {
		return "", nil
	}
	if !supportedGraphCompletionModel(model) {
		return "", fmt.Errorf("unsupported --local-graph-model %q; choose none, qwen3:8b, or qwen3:14b", model)
	}
	return model, nil
}

func localGraphEnvironment(modelsReady, adapterReady bool, completionModel, adapterPath string) map[string]string {
	adapterPath = strings.TrimSpace(adapterPath)
	if !modelsReady || !adapterReady || !filepath.IsAbs(adapterPath) || !supportedGraphCompletionModel(completionModel) {
		return nil
	}
	return map[string]string{
		"AGENT_MEMORY_GRAPH_ENABLED":             "true",
		"AGENT_MEMORY_GRAPH_ADAPTER":             filepath.Clean(adapterPath),
		"AGENT_MEMORY_GRAPH_COMPLETION_PROVIDER": "ollama_chat",
		"AGENT_MEMORY_GRAPH_COMPLETION_MODEL":    completionModel,
		"AGENT_MEMORY_GRAPH_EMBEDDING_PROVIDER":  "ollama",
		"AGENT_MEMORY_GRAPH_EMBEDDING_MODEL":     localGraphEmbeddingModel,
	}
}

func localGraphAdapterReadiness(ctx context.Context) (string, error) {
	executable := strings.TrimSpace(os.Getenv("AGENT_MEMORY_GRAPH_ADAPTER"))
	if !filepath.IsAbs(executable) {
		return "", fmt.Errorf("GraphRAG adapter is not configured with an absolute executable")
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("configured GraphRAG adapter is unavailable")
	}
	privateTemp, err := os.MkdirTemp("", "agent-memory-graph-readiness-")
	if err != nil {
		return "", fmt.Errorf("prepare GraphRAG readiness directory")
	}
	defer os.RemoveAll(privateTemp)

	readinessContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(readinessContext, executable, "readiness")
	command.Dir = filepath.Dir(executable)
	command.Env = []string{
		"HOME=" + privateTemp,
		"TMPDIR=" + privateTemp,
		"PATH=" + os.Getenv("PATH"),
		"PYTHONUNBUFFERED=1",
		"NO_COLOR=1",
	}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("configured GraphRAG adapter did not pass readiness")
	}
	return filepath.Clean(executable), nil
}
