package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/config"
)

func TestLocalGraphRunnerRejectsUserControlledCommandAndPath(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	executable := filepath.Join(dataDir, "adapter")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '{\"state\":\"completed\"}\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	configuration := config.DefaultGraphConfig(dataDir)
	configuration.Enabled = true
	configuration.Executable = executable
	configuration.JobRoot = filepath.Join(dataDir, "jobs")
	runner := application.NewLocalGraphRunner(configuration)
	if _, err := runner.Run(context.Background(), application.LocalGraphCommand("--request=/outside;rm"), map[string]string{"job_root": "/outside"}); err == nil {
		t.Fatal("user-controlled command accepted")
	}
	result, err := runner.Run(context.Background(), application.LocalGraphFullIndex, map[string]string{"job_root": "/outside", "flags": "--unsafe"})
	if err != nil || result.State != "completed" {
		t.Fatalf("reviewed command failed: result=%#v err=%v", result, err)
	}
	if filepath.Dir(result.JobDir) != configuration.JobRoot {
		t.Fatalf("user-controlled job path escaped root: %q", result.JobDir)
	}
}
