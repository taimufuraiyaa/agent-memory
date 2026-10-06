package application

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
)

func TestLocalGraphRunnerMissingAdapterDegradesOnlyGraph(t *testing.T) {
	t.Parallel()
	configuration := graphRunnerTestConfig(t, filepath.Join(t.TempDir(), "missing"))
	result, err := NewLocalGraphRunner(configuration).Run(context.Background(), LocalGraphReadiness, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "unavailable" || result.ReasonCode != "adapter_unavailable" {
		t.Fatalf("missing optional adapter = %#v", result)
	}
}

func TestLocalGraphRunnerUsesFixedArgumentsAndPrivateJobDirectory(t *testing.T) {
	t.Parallel()
	script := graphRunnerScript(t, `printf '{"state":"completed","command":"%s","flag":"%s","request_path":"%s"}\n' "$1" "$2" "$3"`)
	configuration := graphRunnerTestConfig(t, script)
	result, err := NewLocalGraphRunner(configuration).Run(context.Background(), LocalGraphFullIndex, map[string]string{"payload": `; touch /tmp/escaped`})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "completed" || result.Response["command"] != "full-index" || result.Response["flag"] != "--request" {
		t.Fatalf("fixed adapter argv lost: %#v", result)
	}
	requestPath, _ := result.Response["request_path"].(string)
	if filepath.Dir(requestPath) != result.JobDir || filepath.Base(requestPath) != "request.json" {
		t.Fatalf("request path escaped private job: %q / %q", requestPath, result.JobDir)
	}
	info, err := os.Stat(result.JobDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("job directory permissions = %v", info.Mode().Perm())
	}
}

func TestLocalGraphRunnerDeadlineTerminatesProcessGroup(t *testing.T) {
	t.Parallel()
	script := graphRunnerScript(t, `/bin/sleep 30 & child=$!; printf '%s\n' "$child" > "$PWD/child.pid"; wait "$child"`)
	configuration := graphRunnerTestConfig(t, script)
	configuration.TimeoutSeconds = 1
	configuration.CancelGraceSeconds = 1
	result, err := NewLocalGraphRunner(configuration).Run(context.Background(), LocalGraphFullIndex, map[string]string{"id": "job"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReasonCode != "deadline_exceeded" {
		t.Fatalf("deadline result = %#v", result)
	}
}

func TestLocalGraphRunnerBoundsStructuredOutput(t *testing.T) {
	t.Parallel()
	script := graphRunnerScript(t, `printf '%02048d' 0`)
	configuration := graphRunnerTestConfig(t, script)
	configuration.MaxOutputBytes = 1024
	result, err := NewLocalGraphRunner(configuration).Run(context.Background(), LocalGraphFullIndex, map[string]string{"id": "job"})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReasonCode != "output_limit_exceeded" || result.OutputBytes != 1024 {
		t.Fatalf("output limit result = %#v", result)
	}
}

func TestLocalGraphRunnerOnlyPassesBooleanOllamaHostGatewayFlag(t *testing.T) {
	runner := NewLocalGraphRunner(config.DefaultGraphConfig(t.TempDir()))

	t.Setenv("AGENT_MEMORY_GRAPH_OLLAMA_HOST_GATEWAY", "true")
	if !containsEnvironmentVariable(runner.adapterEnvironment(t.TempDir()), "AGENT_MEMORY_GRAPH_OLLAMA_HOST_GATEWAY=true") {
		t.Fatal("explicit dev host-gateway flag was not passed to the adapter")
	}

	for _, value := range []string{"false", "http://remote.example:11434", "TRUE"} {
		t.Setenv("AGENT_MEMORY_GRAPH_OLLAMA_HOST_GATEWAY", value)
		if containsEnvironmentVariable(runner.adapterEnvironment(t.TempDir()), "AGENT_MEMORY_GRAPH_OLLAMA_HOST_GATEWAY=") {
			t.Fatalf("non-true setting %q was passed to the adapter", value)
		}
	}
}

func TestLocalGraphRunnerPassesPinnedLockfileWithoutForwardingAmbientSecrets(t *testing.T) {
	lockfile := filepath.Join(t.TempDir(), "uv.lock")
	if err := os.WriteFile(lockfile, []byte("version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENT_MEMORY_GRAPHRAG_LOCK_FILE", lockfile)
	t.Setenv("OPENAI_API_KEY", "must-not-be-forwarded")

	script := graphRunnerScript(t, `printf '{"state":"ready","lock_file":"%s","openai":"%s"}\n' "${AGENT_MEMORY_GRAPHRAG_LOCK_FILE-}" "${OPENAI_API_KEY-unset}"`)
	result, err := NewLocalGraphRunner(graphRunnerTestConfig(t, script)).Run(context.Background(), LocalGraphReadiness, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "ready" || result.Response["lock_file"] != lockfile {
		t.Fatalf("adapter did not receive pinned dependency lockfile path: %#v", result)
	}
	if result.Response["openai"] != "unset" {
		t.Fatalf("ambient cloud credential was forwarded to adapter: %#v", result.Response)
	}
}

func containsEnvironmentVariable(environment []string, expected string) bool {
	for _, entry := range environment {
		if entry == expected {
			return true
		}
	}
	return false
}

func graphRunnerTestConfig(t *testing.T, executable string) config.GraphConfig {
	t.Helper()
	dataDir := t.TempDir()
	configuration := config.DefaultGraphConfig(dataDir)
	configuration.Enabled = true
	configuration.Executable = executable
	configuration.JobRoot = filepath.Join(dataDir, "jobs")
	configuration.TimeoutSeconds = 5
	configuration.CancelGraceSeconds = 1
	return configuration
}

func graphRunnerScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adapter")
	contents := "#!/bin/sh\nset -eu\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
