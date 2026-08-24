package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type projectRulesAdapter struct {
	name       string
	detectPath string
	ownedPaths []string
}

func NewCursorAdapter() Adapter {
	return projectRulesAdapter{name: "cursor", detectPath: ".cursor", ownedPaths: []string{filepath.Join(".cursor", "rules", "agent-memory.mdc")}}
}

func NewKiroAdapter() Adapter {
	return projectRulesAdapter{name: "kiro", detectPath: ".kiro", ownedPaths: []string{
		filepath.Join(".kiro", "hooks", "memory-recall-gate.json"),
		filepath.Join(".kiro", "hooks", "memory-consolidation-gate.json"),
	}}
}

func (a projectRulesAdapter) Name() string { return a.name }

func (a projectRulesAdapter) Detect(_ context.Context, options Options) (bool, error) {
	_, err := os.Stat(filepath.Join(options.Root, a.detectPath))
	return err == nil, nil
}

func (a projectRulesAdapter) Plan(_ context.Context, options Options) (Result, error) {
	return Result{Agent: a.name, Planned: a.paths(options.Root)}, nil
}

func (a projectRulesAdapter) Connect(_ context.Context, options Options) (Result, error) {
	var paths []string
	var err error
	if a.name == "cursor" {
		paths, err = workspace.WriteCursorProjectFile(options.Root, options.Workspace)
	} else {
		paths, err = workspace.WriteKiroProjectFiles(options.Root)
	}
	if err != nil {
		return Result{}, err
	}
	verified, err := a.verify(options.Root, true)
	return Result{Agent: a.name, Applied: paths, Verified: verified}, err
}

func (a projectRulesAdapter) Disconnect(_ context.Context, options Options) (Result, error) {
	removed := make([]string, 0, len(a.ownedPaths))
	for _, relative := range a.ownedPaths {
		path := filepath.Join(options.Root, relative)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
		removed = append(removed, path)
	}
	verified, err := a.verify(options.Root, false)
	return Result{Agent: a.name, Removed: removed, Verified: verified}, err
}

func (a projectRulesAdapter) Verify(_ context.Context, options Options) (Result, error) {
	verified, err := a.verify(options.Root, true)
	return Result{Agent: a.name, Verified: verified}, err
}

func (a projectRulesAdapter) verify(root string, connected bool) (bool, error) {
	current := true
	for _, relative := range a.ownedPaths {
		content, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil || !strings.Contains(string(content), workspace.MemoryContractMarker) {
			current = false
		}
	}
	return current == connected, nil
}

func (a projectRulesAdapter) paths(root string) []string {
	paths := make([]string, 0, len(a.ownedPaths))
	for _, relative := range a.ownedPaths {
		paths = append(paths, filepath.Join(root, relative))
	}
	return paths
}
