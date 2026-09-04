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
