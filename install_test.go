package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/bootstrap"
	amconfig "github.com/taimufuraiyaa/agent-memory/internal/config"
)

func TestRepositoryDoesNotContainDeveloperSpecificHomePath(t *testing.T) {
	forbiddenPath := strings.Join([]string{"", "Users", "ti" + "me"}, "/")
	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("get repository root: %v", err)
	}

	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}

		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(contents, []byte(forbiddenPath)) {
			relativePath, relErr := filepath.Rel(root, path)
			if relErr != nil {
				relativePath = path
			}
			t.Errorf("developer-specific home path found in %s", relativePath)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan repository: %v", err)
	}
}

func TestMergeEnvFileAddsAdaptiveTuningGuidance(t *testing.T) {
	merged, err := mergeEnvFile("/tmp/agent-memory.env", map[string]string{
		"AGENT_MEMORY_ENABLED": "1",
	})
	if err != nil {
		t.Fatalf("merge env file: %v", err)
	}
	if !strings.Contains(merged, "export AGENT_MEMORY_ENABLED=1") {
		t.Fatalf("expected base env assignment, got %q", merged)
	}
	if !strings.Contains(merged, amconfig.AdaptiveTuningEnvGuidanceHeader()) {
		t.Fatalf("expected adaptive tuning guidance, got %q", merged)
	}
	if !strings.Contains(merged, "agent-memory tuning") {
		t.Fatalf("expected tuning command hint, got %q", merged)
	}
}

func TestMergeEnvFileGuidanceIsIdempotent(t *testing.T) {
	first := amconfig.EnsureAdaptiveTuningEnvGuidance("export AGENT_MEMORY_ENABLED=\"1\"\n")
	second, err := mergeEnvFile("/tmp/agent-memory.env", map[string]string{
		"AGENT_MEMORY_ENABLED": "1",
	})
	if err != nil {
		t.Fatalf("merge env file: %v", err)
	}
	second = amconfig.EnsureAdaptiveTuningEnvGuidance(second)
	if strings.Count(first, amconfig.AdaptiveTuningEnvGuidanceHeader()) != 1 {
		t.Fatalf("expected one guidance header in first output, got %q", first)
	}
	if strings.Count(second, amconfig.AdaptiveTuningEnvGuidanceHeader()) != 1 {
		t.Fatalf("expected one guidance header in second output, got %q", second)
	}
}

func TestRunDashboardInstallRetriesAfterCleanup(t *testing.T) {
	dst := t.TempDir()
	t.Setenv("PATH", fakeInstallNPMScriptDir(t, `#!/bin/sh
set -eu
marker=".npm-ci-attempt"
if [ ! -f "$marker" ]; then
  touch "$marker"
  mkdir -p node_modules/esbuild
  echo "simulated failure" >&2
  exit 1
fi
if [ -d node_modules ]; then
  echo "node_modules should have been removed before retry" >&2
  exit 1
fi
touch npm-ci-success
`)+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := bootstrap.RunDashboardInstall(dst, io.Discard, io.Discard); err != nil {
		t.Fatalf("bootstrap.RunDashboardInstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "npm-ci-success")); err != nil {
		t.Fatalf("expected retry success marker: %v", err)
	}
}

func TestRunDashboardInstallReturnsFailureAfterRetry(t *testing.T) {
	dst := t.TempDir()
	t.Setenv("PATH", fakeInstallNPMScriptDir(t, `#!/bin/sh
echo "still broken" >&2
exit 1
`)+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := bootstrap.RunDashboardInstall(dst, io.Discard, io.Discard); err == nil {
		t.Fatalf("expected retry failure")
	}
}

func TestRunInitHereFallsBackToReinstallForExistingProject(t *testing.T) {
	cwd := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	binPath := filepath.Join(t.TempDir(), "agent-memory")
	markerPath := filepath.Join(cwd, "reinstall-ran")
	script := `#!/bin/sh
set -eu
cmd="$1"
shift
case "$cmd" in
  init)
    echo "project already exists" >&2
    exit 1
    ;;
  reinstall)
    : > "` + markerPath + `"
    exit 0
    ;;
  *)
    echo "unexpected command: $cmd" >&2
    exit 2
    ;;
esac
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent-memory: %v", err)
	}

	cfg := config{
		dataDir:     t.TempDir(),
		projectName: "existing-proj",
		quiet:       true,
	}
	if err := runInitHere(cfg, binPath); err != nil {
		t.Fatalf("runInitHere: %v", err)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("expected reinstall fallback marker: %v", err)
	}
}

func TestRunInitHerePassesIDEFlagsToReinstallFallback(t *testing.T) {
	cwd := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	binPath := filepath.Join(t.TempDir(), "agent-memory")
	markerPath := filepath.Join(cwd, "trae-flag-ran")
	script := `#!/bin/sh
set -eu
cmd="$1"
shift
case "$cmd" in
  init)
    echo "project already exists" >&2
    exit 1
    ;;
  reinstall)
    found="0"
    prev=""
    for arg in "$@"; do
      if [ "$prev" = "--ide" ] && [ "$arg" = "trae" ]; then
        found="1"
      fi
      prev="$arg"
    done
    if [ "$found" != "1" ]; then
      echo "missing --ide trae" >&2
      exit 3
    fi
    : > "` + markerPath + `"
    exit 0
    ;;
esac
`
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agent-memory: %v", err)
	}

	cfg := config{
		dataDir:     t.TempDir(),
		projectName: "existing-proj",
		quiet:       true,
		ideTargets:  stringSliceFlag{"trae"},
	}
	if err := runInitHere(cfg, binPath); err != nil {
		t.Fatalf("runInitHere: %v", err)
	}
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("expected ide flag marker: %v", err)
	}
}

func fakeInstallNPMScriptDir(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "npm")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake npm: %v", err)
	}
	return dir
}

func TestValidateModelDirAcceptsValidFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "all-MiniLM-L6-v2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir model dir: %v", err)
	}
	writeTestModelJSON(t, filepath.Join(dir, "config.json"), `{"hidden_size":384}`)
	writeTestModelJSON(t, filepath.Join(dir, "tokenizer.json"), `{"model":{"type":"WordPiece","vocab":{"[PAD]":0}}}`)
	writeTestModelJSON(t, filepath.Join(dir, "tokenizer_config.json"), `{"model_max_length":128}`)
	writeTestModelJSON(t, filepath.Join(dir, "special_tokens_map.json"), `{"cls_token":"[CLS]"}`)
	writeTestModelONNX(t, filepath.Join(dir, "model.onnx"))

	if err := bootstrap.ValidateModelDir(dir); err != nil {
		t.Fatalf("validate model dir: %v", err)
	}
}

func TestValidateModelFileRejectsHTML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("<html><title>Denied</title></html>"), 0o644); err != nil {
		t.Fatalf("write html: %v", err)
	}
	if err := bootstrap.ValidateModelFile("config.json", path); err == nil {
		t.Fatalf("expected html validation failure")
	}
}

func writeTestModelJSON(t *testing.T, path, payload string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeTestModelONNX(t *testing.T, path string) {
	t.Helper()
	payload := append([]byte("ONNX"), bytes.Repeat([]byte{0x1}, 1024*1024)...)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
