package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWorkspaceBoundAgentCommandsRequireExplicitWorkspaceFlag(t *testing.T) {
	t.Setenv("MEMORY_WORKSPACE", "inferred-workspace")
	t.Setenv("AGENT_MEMORY_ENABLED", "1")

	tests := []struct {
		name string
		args []string
	}{
		{name: "write", args: []string{"write", "--content", "durable fact"}},
		{name: "search", args: []string{"search", "--query", "durable fact"}},
		{name: "recall", args: []string{"recall", "--task", "continue task"}},
		{name: "feedback", args: []string{"feedback", "--request-id", "request-1", "--score", "5", "--reason", "useful", "--useful-count", "1", "--total-count", "1"}},
		{name: "session end", args: []string{"session-end", "--transcript", "done"}},
		{name: "work start", args: []string{"work", "start", "--goal", "goal", "--session", "session", "--principal", "principal", "--client", "client"}},
		{name: "blank workspace cannot fall back to environment", args: []string{"search", "--workspace", "", "--query", "durable fact"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := NewRootCommand()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(test.args)

			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "explicit --workspace is required") {
				t.Fatalf("Execute() error = %v, want explicit workspace requirement", err)
			}
		})
	}
}

func TestEveryWorkLeafInheritsExplicitWorkspaceRequirement(t *testing.T) {
	root := NewRootCommand()
	work, _, err := root.Find([]string{"work"})
	if err != nil {
		t.Fatalf("find work command: %v", err)
	}
	for _, child := range work.Commands() {
		if child.HasSubCommands() {
			continue
		}
		if !commandRequiresExplicitWorkspace(child) {
			t.Errorf("work leaf %q does not inherit explicit workspace requirement", child.Name())
		}
	}
}

func TestGlobalCommandDoesNotRequireWorkspaceFlag(t *testing.T) {
	t.Setenv("MEMORY_WORKSPACE", "")
	cmd := NewRootCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command should not require workspace: %v", err)
	}
}
