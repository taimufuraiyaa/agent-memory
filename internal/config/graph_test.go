package config

import (
	"path/filepath"
	"testing"
)

func TestLocalGraphRunnerConfigIsDisabledAndBoundedByDefault(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	graph := DefaultGraphConfig(dataDir)
	if graph.Enabled {
		t.Fatal("graph adapter enabled by default")
	}
	if err := graph.Validate(dataDir); err != nil {
		t.Fatal(err)
	}
}

func TestGraphDefaultJobRootTracksDataDirectoryOverride(t *testing.T) {
	config := DefaultConfig()
	newDataDir := t.TempDir()
	config.merge(&Config{DataDir: newDataDir}, map[string]bool{"data_dir": true})
	if config.Graph.JobRoot != filepath.Join(newDataDir, "graphrag-jobs") {
		t.Fatalf("default graph root did not follow data directory: %q", config.Graph.JobRoot)
	}
	explicit := filepath.Join(newDataDir, "custom-jobs")
	config.Graph.JobRoot = explicit
	t.Setenv("AGENT_MEMORY_DATA_DIR", t.TempDir())
	config.applyEnvOverrides()
	if config.Graph.JobRoot != explicit {
		t.Fatalf("explicit graph root was overwritten: %q", config.Graph.JobRoot)
	}
}

func TestLocalGraphRunnerConfigRejectsUncontainedPathsAndCredentialNames(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	graph := DefaultGraphConfig(dataDir)
	graph.Enabled = true
	graph.Executable = filepath.Join(dataDir, "adapter")
	graph.JobRoot = filepath.Dir(dataDir)
	if err := graph.Validate(dataDir); err == nil {
		t.Fatal("uncontained job root accepted")
	}
	graph.JobRoot = filepath.Join(dataDir, "jobs")
	graph.CredentialEnv = []string{"PATH"}
	if err := graph.Validate(dataDir); err == nil {
		t.Fatal("unreviewed credential environment accepted")
	}
}
