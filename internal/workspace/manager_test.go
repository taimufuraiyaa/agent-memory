package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestValidateProjectName(t *testing.T) {
	got, err := ValidateProjectName(" My Project_1 ")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got != "my-project_1" {
		t.Fatalf("unexpected normalized name: %s", got)
	}
	if _, err := ValidateProjectName("archived"); err == nil {
		t.Fatalf("expected reserved-name error")
	}
}

func TestManagerInitListRenameDelete(t *testing.T) {
	base := t.TempDir()
	cwd := t.TempDir()
	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	initOut, err := mgr.Init(context.Background(), InitOptions{
		CWD:         cwd,
		ProjectName: "proj-a",
		RulePath:    filepath.Join(cwd, ".cursor", "rules", "agent-memory.mdc"),
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if initOut.Project != "proj-a" {
		t.Fatalf("unexpected project: %s", initOut.Project)
	}
	if _, err := os.Stat(initOut.DBPath); err != nil {
		t.Fatalf("db not created: %v", err)
	}
	if initOut.CursorRule == "" {
		t.Fatalf("expected cursor rule path")
	}
	b, err := os.ReadFile(initOut.CursorRule)
	if err != nil {
		t.Fatalf("read cursor rule: %v", err)
	}
	if !strings.Contains(string(b), "Default memory policy (MANDATORY)") {
		t.Fatalf("expected cursor rule to include memory policy")
	}

	list1, err := mgr.List(context.Background())
	if err != nil || len(list1) != 1 {
		t.Fatalf("list after init failed: %v len=%d", err, len(list1))
	}

	ren, err := mgr.Rename(context.Background(), RenameOptions{
		CWD: cwd,
		To:  "proj-b",
	})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if ren.To != "proj-b" {
		t.Fatalf("unexpected rename result: %+v", ren)
	}
	if _, err := os.Stat(ren.DB); err != nil {
		t.Fatalf("renamed db missing: %v", err)
	}

	del, err := mgr.Delete(context.Background(), DeleteOptions{
		ProjectName: "proj-b",
		KeepData:    true,
		Yes:         true,
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if del.ArchivedPath == "" {
		t.Fatalf("expected archived path")
	}
	if _, err := os.Stat(del.ArchivedPath); err != nil {
		t.Fatalf("archived db missing: %v", err)
	}
}

func TestManagerInitMultipleIDERules(t *testing.T) {
	base := t.TempDir()
	cwd := t.TempDir()
	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	out, err := mgr.Init(context.Background(), InitOptions{
		CWD:         cwd,
		ProjectName: "proj-ides",
		IDEs:        []string{"antigravity", "aierules"},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(out.RuleFiles) != 2 {
		t.Fatalf("expected 2 rule files, got %d", len(out.RuleFiles))
	}
	for _, p := range out.RuleFiles {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing rule file %s: %v", p, err)
		}
	}
}

func TestManagerInitReuseAndForce(t *testing.T) {
	base := t.TempDir()
	cwd := t.TempDir()
	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	_, err = mgr.Init(context.Background(), InitOptions{CWD: cwd, ProjectName: "proj-x"})
	if err != nil {
		t.Fatalf("init #1: %v", err)
	}
	if _, err := mgr.Init(context.Background(), InitOptions{CWD: cwd, ProjectName: "proj-x"}); err == nil {
		t.Fatalf("expected conflict without reuse/force")
	}
	if _, err := mgr.Init(context.Background(), InitOptions{CWD: cwd, ProjectName: "proj-x", Reuse: true}); err != nil {
		t.Fatalf("reuse init failed: %v", err)
	}
	if _, err := mgr.Init(context.Background(), InitOptions{CWD: cwd, ProjectName: "proj-x", Force: true}); err != nil {
		t.Fatalf("force init failed: %v", err)
	}
}

func TestManagerInitConcurrentSafety(t *testing.T) {
	base := t.TempDir()
	cwd := t.TempDir()
	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	failures := 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := mgr.Init(context.Background(), InitOptions{CWD: cwd, ProjectName: "concurrent"})
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				successes++
			} else {
				failures++
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("expected exactly one successful init, got %d (failures=%d)", successes, failures)
	}
}

func TestWriteAgentFilesUpsertsCursorRulesFile(t *testing.T) {
	cwd := t.TempDir()
	rulePath := filepath.Join(cwd, ".cursorrules")
	if err := os.WriteFile(rulePath, []byte("# AI Agent Rules - demo\n"), 0o644); err != nil {
		t.Fatalf("write .cursorrules: %v", err)
	}

	res, err := WriteAgentFiles(WriteAgentFilesOptions{
		CWD:       cwd,
		Workspace: "ws-demo",
		Force:     false,
	})
	if err != nil {
		t.Fatalf("write agent files: %v", err)
	}
	if res == nil {
		t.Fatalf("expected result")
	}

	after, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("read .cursorrules: %v", err)
	}
	s := string(after)
	if !strings.Contains(s, "## agent-memory (MANDATORY)") {
		t.Fatalf("expected agent-memory section in .cursorrules")
	}
	if !strings.Contains(s, "workspace: ws-demo") {
		t.Fatalf("expected workspace to be written in .cursorrules")
	}

	res2, err := WriteAgentFiles(WriteAgentFilesOptions{
		CWD:       cwd,
		Workspace: "ws-demo",
		Force:     false,
	})
	if err != nil {
		t.Fatalf("write agent files (2): %v", err)
	}
	if res2 == nil {
		t.Fatalf("expected result (2)")
	}
}
