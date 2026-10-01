package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
)

func TestLocalGraphConfigFileReadsOnlyAllowlistedGraphValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-memory.env")
	contents := "export AGENT_MEMORY_GRAPH_ENABLED=true\n" +
		"export AGENT_MEMORY_GRAPH_COMPLETION_PROVIDER=ollama_chat\n" +
		"export AGENT_MEMORY_GRAPH_COMPLETION_MODEL=qwen3:8b\n" +
		"export AGENT_MEMORY_GRAPH_EMBEDDING_PROVIDER=ollama\n" +
		"export AGENT_MEMORY_GRAPH_EMBEDDING_MODEL=qwen3-embedding:0.6b\n" +
		"export AGENT_MEMORY_GRAPH_ADAPTER=/Users/time/.venv/bin/adapter\n" +
		"export OPENAI_API_KEY=must-not-enter-process-env\n" +
		"export INDEX_COMPLETION_API_KEY=must-not-enter-process-env\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	graph := config.DefaultGraphConfig(t.TempDir())
	graph.Executable = "/opt/adapter/.venv/bin/agent-memory-graphrag"

	loaded, err := loadLocalGraphConfigFile(path, graph)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Enabled || loaded.CompletionProvider != "ollama_chat" || loaded.CompletionModel != "qwen3:8b" || loaded.EmbeddingProvider != "ollama" || loaded.EmbeddingModel != "qwen3-embedding:0.6b" {
		t.Fatalf("allow-listed local Graph route was not loaded: %#v", loaded)
	}
	if loaded.Executable != "/opt/adapter/.venv/bin/agent-memory-graphrag" {
		t.Fatalf("host adapter path overrode the container executable: %q", loaded.Executable)
	}
}

func TestLocalGraphConfigFileRejectsShellExpressionsWithoutExecutingThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-memory.env")
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	contents := "export AGENT_MEMORY_GRAPH_COMPLETION_MODEL=$(touch " + marker + ")\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadLocalGraphConfigFile(path, config.DefaultGraphConfig(t.TempDir()))
	if err == nil {
		t.Fatal("shell expression in an allow-listed setting was accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("configuration parser executed shell expression: %v", err)
	}
}

func TestLocalGraphConfigFileMissingKeepsGraphDisabled(t *testing.T) {
	graph := config.DefaultGraphConfig(t.TempDir())
	loaded, err := loadLocalGraphConfigFile(filepath.Join(t.TempDir(), "missing.env"), graph)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Enabled || loaded.CompletionModel != "" || loaded.EmbeddingModel != "" {
		t.Fatalf("missing optional file enabled or configured GraphRAG: %#v", loaded)
	}
}
