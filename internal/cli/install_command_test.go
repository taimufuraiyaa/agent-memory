package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/bootstrap"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestInstallCommandBasic(t *testing.T) {
	originalPlannerInstaller := ensureOllamaPlanner
	selectedPlannerModel := ""
	ensureOllamaPlanner = func(_ context.Context, options bootstrap.OllamaPlannerOptions) (bootstrap.OllamaPlannerResult, error) {
		selectedPlannerModel = options.Model
		return bootstrap.OllamaPlannerResult{Endpoint: bootstrap.DefaultOllamaEndpoint, Model: options.Model, RuntimeReused: true, ModelAvailable: true}, nil
	}
	t.Cleanup(func() { ensureOllamaPlanner = originalPlannerInstaller })
	dataDir := t.TempDir()
	binDir := t.TempDir()
	cwd := t.TempDir()
	codexHome := t.TempDir()
	codexConfig := filepath.Join(codexHome, "config.toml")
	const originalCodexConfig = "model = \"test-model\"\n"
	if err := os.WriteFile(codexConfig, []byte(originalCodexConfig), 0o644); err != nil {
		t.Fatalf("seed Codex config: %v", err)
	}
	t.Setenv("CODEX_HOME", codexHome)

	// Switch working directory to cwd for workspace init testing
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(oldCwd) }()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	cmd := newInstallCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	cmd.SetArgs([]string{
		"--data-dir", dataDir,
		"--bin-dir", binDir,
		"--src", "", // skip building
		"--no-model",
		"--skip-onnx-runtime",
		"--no-dashboard",
		"--local-llm-model", "qwen3:14b",
		"--write-env",
		"--project-name", "test-install-proj",
		"--ide", "cursor",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute failed: %v, stderr: %s", err, stderr.String())
	}

	// Verify data directories created
	for _, sub := range []string{"models", "logs", "onnxruntime"} {
		path := filepath.Join(dataDir, sub)
		if st, err := os.Stat(path); err != nil || !st.IsDir() {
			t.Fatalf("expected data dir %s: %v", path, err)
		}
	}

	// Verify env file created
	envFile := filepath.Join(dataDir, "agent-memory.env")
	if _, err := os.Stat(envFile); err != nil {
		t.Fatalf("expected env file: %v", err)
	}
	b, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("read env file: %v", err)
	}
	envContent := string(b)
	if !strings.Contains(envContent, "AGENT_MEMORY_ENABLED") {
		t.Fatalf("expected AGENT_MEMORY_ENABLED in env file, got: %s", envContent)
	}
	if !strings.Contains(envContent, `AGENT_MEMORY_TERM_BLOOM_MODE="shadow"`) {
		t.Fatalf("expected safe term Bloom rollout mode in env file, got: %s", envContent)
	}
	for _, expected := range []string{
		`AGENT_MEMORY_QUERY_PLANNER_ENABLED="true"`,
		`AGENT_MEMORY_QUERY_PLANNER_ENDPOINT="http://127.0.0.1:11434"`,
		`AGENT_MEMORY_QUERY_PLANNER_MODEL="qwen3:14b"`,
		`AGENT_MEMORY_QUERY_PLANNER_TIMEOUT="15s"`,
		`AGENT_MEMORY_QUERY_PLANNER_WARMUP_ENABLED="true"`,
		`AGENT_MEMORY_QUERY_PLANNER_WARMUP_TIMEOUT="30s"`,
		`AGENT_MEMORY_QUERY_PLANNER_KEEP_ALIVE="30m"`,
		`AGENT_MEMORY_QUERY_PLANNER_CACHE_TTL="10m"`,
		`AGENT_MEMORY_QUERY_PLANNER_CACHE_CAPACITY="256"`,
	} {
		if !strings.Contains(envContent, expected) {
			t.Fatalf("expected planner setting %s in env file, got: %s", expected, envContent)
		}
	}
	if selectedPlannerModel != "qwen3:14b" {
		t.Fatalf("planner installer received model %q", selectedPlannerModel)
	}

	store, err := sqlite.Open(context.Background(), filepath.Join(dataDir, "test-install-proj.db"))
	if err != nil {
		t.Fatalf("open installed workspace db: %v", err)
	}
	defer func() { _ = store.Close() }()
	state, err := store.GetTermIndexState(context.Background(), "test-install-proj")
	if err != nil || state == nil || state.State != sqlite.TermIndexReady {
		t.Fatalf("install did not prepare ready term index: state=%#v err=%v", state, err)
	}

	// An explicit non-Codex IDE selection must not mutate the user's global
	// Codex configuration.
	gotCodexConfig, err := os.ReadFile(codexConfig)
	if err != nil {
		t.Fatalf("read Codex config: %v", err)
	}
	if string(gotCodexConfig) != originalCodexConfig {
		t.Fatalf("non-Codex install changed global Codex config: %s", gotCodexConfig)
	}

	// Verify cursor rule created
	cursorRule := filepath.Join(cwd, ".cursor", "rules", "agent-memory.mdc")
	if _, err := os.Stat(cursorRule); err != nil {
		t.Fatalf("expected cursor rule file: %v", err)
	}
	ruleBytes, err := os.ReadFile(cursorRule)
	if err != nil {
		t.Fatalf("read cursor rule: %v", err)
	}
	ruleContent := string(ruleBytes)
	if !strings.Contains(ruleContent, "workspace: test-install-proj") {
		t.Fatalf("expected workspace name in cursor rule, got: %s", ruleContent)
	}
}

func TestResolveHeadlessPlannerModelPreservesAliasAndValidatesCatalog(t *testing.T) {
	for _, test := range []struct {
		withLocal bool
		model     string
		want      string
		wantErr   bool
	}{
		{withLocal: false, model: "", want: ""},
		{withLocal: true, model: "", want: "qwen3:8b"},
		{withLocal: false, model: "qwen3:4b", want: "qwen3:4b"},
		{withLocal: false, model: "qwen3:14b", want: "qwen3:14b"},
		{withLocal: false, model: "none", want: ""},
		{withLocal: false, model: "remote/custom", wantErr: true},
		{withLocal: true, model: "qwen3:4b", wantErr: true},
	} {
		got, err := resolveHeadlessPlannerModel(test.withLocal, test.model)
		if (err != nil) != test.wantErr || got != test.want {
			t.Fatalf("with=%v model=%q got=%q err=%v", test.withLocal, test.model, got, err)
		}
	}
}

func TestPlannerEnvironmentUsesExactModelOrExplicitlyDisables(t *testing.T) {
	ready := plannerEnvironment(true, true, "qwen3:4b")
	if ready["AGENT_MEMORY_QUERY_PLANNER_ENABLED"] != "true" || ready["AGENT_MEMORY_QUERY_PLANNER_MODEL"] != "qwen3:4b" {
		t.Fatalf("ready environment=%v", ready)
	}
	disabled := plannerEnvironment(false, true, "")
	if len(disabled) != 1 || disabled["AGENT_MEMORY_QUERY_PLANNER_ENABLED"] != "false" {
		t.Fatalf("disabled environment=%v", disabled)
	}
	if untouched := plannerEnvironment(false, false, ""); len(untouched) != 0 {
		t.Fatalf("implicit parser-only changed planner environment=%v", untouched)
	}
}

func TestInstallOrCopyBinaryBuildsAbsoluteSourceOutsideClientWorkspace(t *testing.T) {
	repositoryCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get repository cwd: %v", err)
	}
	repositoryRoot := findSourceRoot(repositoryCWD)
	if repositoryRoot == "" {
		t.Fatal("locate repository root")
	}

	clientDir := t.TempDir()
	if err := os.Chdir(clientDir); err != nil {
		t.Fatalf("chdir to client workspace: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(repositoryCWD) })

	binDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	installed, err := installOrCopyBinary(&stdout, &stderr, binDir, filepath.Join(repositoryRoot, "cmd", "agent-memory"))
	if err != nil {
		t.Fatalf("install from absolute source outside module: %v, stderr: %s", err, stderr.String())
	}
	if installed != filepath.Join(binDir, binNameWithExt("agent-memory")) {
		t.Fatalf("unexpected installed path %q", installed)
	}
	if info, err := os.Stat(installed); err != nil || info.IsDir() {
		t.Fatalf("expected installed binary at %s: %v", installed, err)
	}
	alias := filepath.Join(binDir, binNameWithExt("am"))
	if info, err := os.Stat(alias); err != nil || info.IsDir() {
		t.Fatalf("expected concise executable at %s: %v", alias, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(binDir, ".agent-memory-install.*"))
	if err != nil {
		t.Fatalf("scan install artifacts: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("installer left temporary build artifacts: %v", leftovers)
	}
}
