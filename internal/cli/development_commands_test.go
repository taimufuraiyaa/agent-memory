package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type recordedDevelopmentCommand struct {
	dir  string
	name string
	args []string
}

func TestDevelopmentCommandsAreRegistered(t *testing.T) {
	root := NewRootCommand()
	for _, name := range []string{"start", "stop", "restart", "build"} {
		if command, _, err := root.Find([]string{name}); err != nil || command == root || command.Name() != name {
			t.Fatalf("expected %q command to be registered", name)
		}
	}
}

func TestDevelopmentRootWalksUpFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	mustWriteDevelopmentFile(t, filepath.Join(root, developmentBaseComposePath))
	mustWriteDevelopmentFile(t, filepath.Join(root, developmentOverrideComposePath))
	nested := filepath.Join(root, "tools", "agent-memory", "dashboard")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	got, err := findDevelopmentRoot(nested)
	if err != nil {
		t.Fatalf("find development root: %v", err)
	}
	if got != root {
		t.Fatalf("development root %q, want %q", got, root)
	}
}

func TestDevelopmentLifecycleCommandSequences(t *testing.T) {
	want := map[string][][]string{
		"start": {
			{"compose", "-f", "BASE", "-f", "DEV", "up", "-d", "--build", "--wait", "--remove-orphans"},
		},
		"stop": {
			{"compose", "-f", "BASE", "-f", "DEV", "down"},
		},
		"restart": {
			{"compose", "-f", "BASE", "-f", "DEV", "restart", "api"},
			{"compose", "-f", "BASE", "-f", "DEV", "restart", "postgres", "minio", "nats"},
			{"compose", "-f", "BASE", "-f", "DEV", "up", "-d", "--wait", "postgres", "minio", "nats"},
			{"compose", "-f", "BASE", "-f", "DEV", "up", "-d", "--force-recreate", "--wait", "worker", "reconciler", "edge", "frontend"},
		},
		"build": {
			{"compose", "-f", "BASE", "-f", "DEV", "build", "api"},
			{"compose", "-f", "BASE", "-f", "DEV", "up", "-d", "--force-recreate", "--wait", "api"},
			{"compose", "-f", "BASE", "-f", "DEV", "restart", "postgres", "minio", "nats"},
			{"compose", "-f", "BASE", "-f", "DEV", "up", "-d", "--wait", "postgres", "minio", "nats"},
			{"compose", "-f", "BASE", "-f", "DEV", "up", "-d", "--force-recreate", "--wait", "worker", "reconciler", "edge", "frontend"},
		},
	}

	for operation, expected := range want {
		t.Run(operation, func(t *testing.T) {
			root := newDevelopmentFixture(t)
			var recorded []recordedDevelopmentCommand
			previous := runDevelopmentCommand
			runDevelopmentCommand = func(_ context.Context, dir, name string, args []string, _, _ io.Writer) error {
				recorded = append(recorded, recordedDevelopmentCommand{dir: dir, name: name, args: append([]string(nil), args...)})
				return nil
			}
			t.Cleanup(func() { runDevelopmentCommand = previous })

			command := newDevelopmentCommand(operation)
			command.SetOut(&bytes.Buffer{})
			command.SetErr(&bytes.Buffer{})
			command.SetContext(context.Background())
			command.SetArgs(nil)
			if err := executeDevelopmentLifecycle(command, root, operation); err != nil {
				t.Fatalf("execute %s: %v", operation, err)
			}
			if len(recorded) != len(expected) {
				t.Fatalf("recorded %d commands, want %d: %#v", len(recorded), len(expected), recorded)
			}
			for i, call := range recorded {
				if call.dir != root || call.name != "docker" {
					t.Fatalf("call %d = %#v", i, call)
				}
				args := normalizeDevelopmentComposePaths(root, call.args)
				if !reflect.DeepEqual(args, expected[i]) {
					t.Fatalf("call %d args = %#v, want %#v", i, args, expected[i])
				}
			}
		})
	}
}

func TestDevelopmentLifecycleStopsAfterFailure(t *testing.T) {
	root := newDevelopmentFixture(t)
	calls := 0
	previous := runDevelopmentCommand
	runDevelopmentCommand = func(_ context.Context, _ string, _ string, _ []string, _, _ io.Writer) error {
		calls++
		if calls == 2 {
			return errors.New("compose failed")
		}
		return nil
	}
	t.Cleanup(func() { runDevelopmentCommand = previous })

	command := newDevelopmentCommand("build")
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	err := executeDevelopmentLifecycle(command, root, "build")
	if err == nil || !strings.Contains(err.Error(), "recreate api") || !strings.Contains(err.Error(), "compose failed") {
		t.Fatalf("expected compose failure, got %v", err)
	}
	if strings.Contains(err.Error(), "build failed") {
		t.Fatalf("failure mislabeled as build failure: %v", err)
	}
	if calls != 2 {
		t.Fatalf("executed %d commands after failure, want 2", calls)
	}
}

func TestDevelopmentLifecyclePrintsFinalStatus(t *testing.T) {
	want := map[string]string{
		"start":   "Agent Memory started.\nFrontend: http://localhost:3100\n",
		"stop":    "Agent Memory stopped.\n",
		"restart": "Agent Memory restarted.\nFrontend: http://localhost:3100\n",
		"build":   "Agent Memory build complete.\nFrontend: http://localhost:3100\n",
	}
	for operation, expected := range want {
		t.Run(operation, func(t *testing.T) {
			root := newDevelopmentFixture(t)
			previous := runDevelopmentCommand
			runDevelopmentCommand = func(context.Context, string, string, []string, io.Writer, io.Writer) error {
				return nil
			}
			t.Cleanup(func() { runDevelopmentCommand = previous })

			var stdout bytes.Buffer
			command := newDevelopmentCommand(operation)
			command.SetOut(&stdout)
			command.SetErr(&bytes.Buffer{})
			if err := executeDevelopmentLifecycle(command, root, operation); err != nil {
				t.Fatalf("execute %s: %v", operation, err)
			}
			if stdout.String() != expected {
				t.Fatalf("status output %q, want %q", stdout.String(), expected)
			}
		})
	}
}

func mustWriteDevelopmentFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func newDevelopmentFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWriteDevelopmentFile(t, filepath.Join(root, developmentBaseComposePath))
	mustWriteDevelopmentFile(t, filepath.Join(root, developmentOverrideComposePath))
	return root
}

func normalizeDevelopmentComposePaths(root string, args []string) []string {
	normalized := append([]string(nil), args...)
	for i, arg := range normalized {
		switch arg {
		case filepath.Join(root, developmentBaseComposePath):
			normalized[i] = "BASE"
		case filepath.Join(root, developmentOverrideComposePath):
			normalized[i] = "DEV"
		}
	}
	return normalized
}
