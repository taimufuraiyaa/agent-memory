package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type CodexAdapter struct{}

func NewCodexAdapter() CodexAdapter { return CodexAdapter{} }
func (CodexAdapter) Name() string   { return "codex" }

func (CodexAdapter) Detect(_ context.Context, options Options) (bool, error) {
	_, err := os.Stat(filepath.Join(options.Root, ".codex"))
	return err == nil, nil
}

func (CodexAdapter) Plan(_ context.Context, options Options) (Result, error) {
	return Result{Agent: "codex", Planned: codexPaths(options.Root)}, nil
}

func (CodexAdapter) Connect(ctx context.Context, options Options) (Result, error) {
	_ = ctx
	paths, err := workspace.WriteCodexProjectFiles(options.Root, options.Workspace, options.DataDir, options.Force)
	if err != nil {
		return Result{}, err
	}
	verified, err := verifyCodex(options.Root, true)
	return Result{Agent: "codex", Applied: paths, Verified: verified}, err
}

func (CodexAdapter) Disconnect(ctx context.Context, options Options) (Result, error) {
	_ = ctx
	paths, err := workspace.RemoveCodexProjectFiles(options.Root)
	if err != nil {
		return Result{}, err
	}
	verified, err := verifyCodex(options.Root, false)
	return Result{Agent: "codex", Removed: paths, Verified: verified}, err
}

func (CodexAdapter) Verify(_ context.Context, options Options) (Result, error) {
	verified, err := verifyCodex(options.Root, true)
	return Result{Agent: "codex", Verified: verified}, err
}

func codexPaths(root string) []string {
	return []string{filepath.Join(root, "AGENTS.md"), filepath.Join(root, ".codex", "config.toml"), filepath.Join(root, ".codex", "hooks.json")}
}

func verifyCodex(root string, connected bool) (bool, error) {
	config, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if err != nil {
		return false, err
	}
	hooks, err := os.ReadFile(filepath.Join(root, ".codex", "hooks.json"))
	if err != nil {
		return false, err
	}
	rules, rulesErr := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	hasManaged := strings.Contains(string(config), "agent-memory managed") && strings.Contains(string(hooks), "agent-memory managed")
	hasContract := rulesErr == nil && strings.Contains(string(rules), workspace.MemoryContractMarker)
	return hasManaged == connected && hasContract == connected, nil
}
