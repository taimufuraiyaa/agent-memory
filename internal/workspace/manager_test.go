package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func init() {
	os.Setenv("ANTIGRAVITY_AGENT", "")
}

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

func TestManagerInitAutoDetectsTraeWhenNoIDEFlagsProvided(t *testing.T) {
	base := t.TempDir()
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".trae"), 0o755); err != nil {
		t.Fatalf("mkdir .trae: %v", err)
	}
	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	out, err := mgr.Init(context.Background(), InitOptions{
		CWD:         cwd,
		ProjectName: "proj-trae",
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(out.RuleFiles) != 1 {
		t.Fatalf("expected 1 rule file, got %d", len(out.RuleFiles))
	}
	traeRule := filepath.Join(cwd, ".trae", "rules", "project_rules.md")
	if out.RuleFiles[0] != traeRule {
		t.Fatalf("expected trae rule path %s, got %+v", traeRule, out.RuleFiles)
	}
	b, err := os.ReadFile(traeRule)
	if err != nil {
		t.Fatalf("read trae rule: %v", err)
	}
	if !strings.Contains(string(b), "workspace: proj-trae") {
		t.Fatalf("expected workspace in trae rule")
	}
	if _, err := os.Stat(filepath.Join(cwd, ".cursor", "rules", "agent-memory.mdc")); !os.IsNotExist(err) {
		t.Fatalf("expected init without IDE flags to avoid writing cursor rule in a Trae-only repo")
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

func TestManagerReinstallKeepsDBAndProjectName(t *testing.T) {
	base := t.TempDir()
	cwd := t.TempDir()
	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	initOut, err := mgr.Init(context.Background(), InitOptions{
		CWD:         cwd,
		ProjectName: "proj-r",
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(cwd, ".kiro", "hooks"), 0o755); err != nil {
		t.Fatalf("mkdir .kiro/hooks: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".kiro", "hooks", "memory-recall-gate.json"), []byte(`{"bad":true}`), 0o644); err != nil {
		t.Fatalf("write bad hook: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".aierules"), []byte("# AI Agent Rules\n"), 0o644); err != nil {
		t.Fatalf("write .aierules: %v", err)
	}

	sub := filepath.Join(cwd, "cmd")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	out, err := mgr.Reinstall(context.Background(), ReinstallOptions{
		CWD:   sub,
		Force: true,
	})
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if out.Project != "proj-r" {
		t.Fatalf("unexpected project: %s", out.Project)
	}
	if out.DBPath != initOut.DBPath {
		t.Fatalf("db path changed: %s != %s", out.DBPath, initOut.DBPath)
	}
	if out.AgentFiles == nil {
		t.Fatalf("expected agent files result")
	}

	cursorRule := filepath.Join(cwd, ".cursor", "rules", "agent-memory.mdc")
	b, err := os.ReadFile(cursorRule)
	if err != nil {
		t.Fatalf("read cursor rule: %v", err)
	}
	if !strings.Contains(string(b), "workspace: proj-r") {
		t.Fatalf("expected cursor rule workspace to remain proj-r")
	}

	hook, err := os.ReadFile(filepath.Join(cwd, ".kiro", "hooks", "memory-recall-gate.json"))
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	if !strings.Contains(string(hook), "agent-memory search") {
		t.Fatalf("expected hook to be rewritten")
	}
	if !strings.Contains(string(hook), "only if one of these is true") {
		t.Fatalf("expected staged recall guidance in hook")
	}

	rules, err := os.ReadFile(filepath.Join(cwd, ".aierules"))
	if err != nil {
		t.Fatalf("read .aierules: %v", err)
	}
	if !strings.Contains(string(rules), "workspace: proj-r") {
		t.Fatalf("expected workspace in .aierules")
	}
}

func TestManagerReinstallCanCreateTraeFilesWhenExplicitlyRequested(t *testing.T) {
	base := t.TempDir()
	cwd := t.TempDir()
	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if _, err := mgr.Init(context.Background(), InitOptions{
		CWD:         cwd,
		ProjectName: "proj-trae-reinstall",
		NoRule:      true,
	}); err != nil {
		t.Fatalf("init: %v", err)
	}

	out, err := mgr.Reinstall(context.Background(), ReinstallOptions{
		CWD:         cwd,
		ProjectName: "proj-trae-reinstall",
		Force:       true,
		IDEs:        []string{"trae"},
	})
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if out.AgentFiles == nil {
		t.Fatalf("expected agent files result")
	}
	traeRule := filepath.Join(cwd, ".trae", "rules", "project_rules.md")
	b, err := os.ReadFile(traeRule)
	if err != nil {
		t.Fatalf("read trae rule: %v", err)
	}
	if !strings.Contains(string(b), "workspace: proj-trae-reinstall") {
		t.Fatalf("expected workspace in trae rule")
	}
}

func TestManagerInitAndReinstallWriteStagedRetrievalPolicyAcrossFiles(t *testing.T) {
	base := t.TempDir()
	cwd := t.TempDir()
	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	out, err := mgr.Init(context.Background(), InitOptions{
		CWD:         cwd,
		ProjectName: "proj-stage",
		IDEs:        []string{"all"},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(out.RuleFiles) == 0 {
		t.Fatalf("expected rule files to be written")
	}

	stagedChecks := map[string][]string{
		filepath.Join(cwd, ".cursor", "rules", "agent-memory.mdc"): {
			"Run a focused memory search for the key terms and entities you're about to research.",
			"Run a recall for the current task only when",
			"`continue`",
		},
		filepath.Join(cwd, ".agents", "rules", "agent-memory.md"): {
			"run memory `search` first",
			"Run task `recall` only when the task is about continuing previous work",
		},
		filepath.Join(cwd, ".aierules"): {
			"run memory `search` first",
			"Run task `recall` only when the task is about continuing previous work",
		},
		filepath.Join(cwd, ".cursorrules"): {
			"run memory `search` first",
			"Run task `recall` only when the task is about continuing previous work",
		},
		filepath.Join(cwd, ".windsurfrules"): {
			"run memory `search` first",
			"Run task `recall` only when the task is about continuing previous work",
		},
		filepath.Join(cwd, "CLAUDE.md"): {
			"run memory `search` first",
			"Run task `recall` only when the task is about continuing previous work",
		},
		filepath.Join(cwd, "AGENTS.md"): {
			"run memory `search` first",
			"Run task `recall` only when the task is about continuing previous work",
		},
		filepath.Join(cwd, ".trae", "rules", "project_rules.md"): {
			"workspace: proj-stage",
			"run memory `search` first",
			"Run task `recall` only when the task is about continuing previous work",
		},
	}

	for path, wants := range stagedChecks {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read generated file %s: %v", path, err)
		}
		content := string(b)
		for _, want := range wants {
			if !strings.Contains(content, want) {
				t.Fatalf("expected %q in %s, got %q", want, path, content)
			}
		}
	}

	if err := os.MkdirAll(filepath.Join(cwd, ".kiro", "hooks"), 0o755); err != nil {
		t.Fatalf("mkdir .kiro/hooks: %v", err)
	}

	corrupted := map[string]string{
		filepath.Join(cwd, ".aierules"):                                 "# AI Agent Rules\n",
		filepath.Join(cwd, ".cursor", "rules", "agent-memory.mdc"):      "broken",
		filepath.Join(cwd, ".kiro", "hooks", "memory-recall-gate.json"): `{"bad":true}`,
	}
	for path, content := range corrupted {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("corrupt %s: %v", path, err)
		}
	}

	sub := filepath.Join(cwd, "pkg", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}
	reinstalled, err := mgr.Reinstall(context.Background(), ReinstallOptions{
		CWD:   sub,
		Force: true,
	})
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if reinstalled.Project != "proj-stage" {
		t.Fatalf("unexpected reinstall project: %s", reinstalled.Project)
	}

	for path, wants := range map[string][]string{
		filepath.Join(cwd, ".aierules"): {
			"workspace: proj-stage",
			"run memory `search` first",
		},
		filepath.Join(cwd, ".cursor", "rules", "agent-memory.mdc"): {
			"workspace: proj-stage",
			"Run a recall for the current task only when",
		},
		filepath.Join(cwd, ".kiro", "hooks", "memory-recall-gate.json"): {
			"only if one of these is true",
			"avoid unnecessary recall when search is already enough",
		},
	} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read repaired file %s: %v", path, err)
		}
		content := string(b)
		for _, want := range wants {
			if !strings.Contains(content, want) {
				t.Fatalf("expected %q in repaired file %s, got %q", want, path, content)
			}
		}
	}
}

func TestHippocampusRecallHookUsesStagedRetrieval(t *testing.T) {
	hooks := HippocampusHooks()
	found := false
	for _, hook := range hooks {
		if hook.Name != "memory-recall-gate.json" {
			continue
		}
		found = true
		for _, want := range []string{
			"agent-memory search",
			"only if one of these is true",
			"continue, resume, or recall previous work",
			"avoid unnecessary recall when search is already enough",
		} {
			if !strings.Contains(hook.Content, want) {
				t.Fatalf("expected %q in recall hook, got %q", want, hook.Content)
			}
		}
	}
	if !found {
		t.Fatalf("expected memory-recall-gate hook")
	}
}

func TestCursorRuleContentUsesStagedRetrieval(t *testing.T) {
	content := cursorRuleContent("ws-demo")
	for _, want := range []string{
		"Run a focused memory search for the key terms and entities you're about to research.",
		"Run a recall for the current task only when",
		"`continue`",
		"`resume`",
		"`what were we doing`",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected %q in cursor rule content, got %q", want, content)
		}
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

func TestWriteAgentFilesDeploysPredefinedSkills(t *testing.T) {
	cwd := t.TempDir()

	// Create .agents directory to trigger antigravity rule generation
	if err := os.MkdirAll(filepath.Join(cwd, ".agents"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	res, err := WriteAgentFiles(WriteAgentFilesOptions{
		CWD:       cwd,
		Workspace: "skill-deploy-test",
		Force:     true,
	})
	if err != nil {
		t.Fatalf("WriteAgentFiles: %v", err)
	}
	if res == nil {
		t.Fatalf("expected result")
	}

	skills := []string{
		"skill-packager",
		"spec-driven-development",
		"planning-and-task-breakdown",
		"incremental-implementation",
		"test-driven-development",
		"code-simplification",
		"debugging-and-error-recovery",
	}

	for _, name := range skills {
		skillPath := filepath.Join(cwd, ".agents", "skills", name, "SKILL.md")
		if _, err := os.Stat(skillPath); err != nil {
			t.Errorf("missing %s SKILL.md: %v", name, err)
			continue
		}

		b, err := os.ReadFile(skillPath)
		if err != nil {
			t.Errorf("read %s SKILL.md: %v", name, err)
			continue
		}
		s := string(b)
		expectedNameLine := "name: " + name
		if !strings.Contains(s, expectedNameLine) {
			t.Errorf("expected '%s' in SKILL.md frontmatter, got: %s", expectedNameLine, s)
		}
	}
}

func TestDebugList(t *testing.T) {
	mgr, err := NewManager("/Users/time/.agent-memory")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	items, err := mgr.List(context.Background())
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	t.Logf("List succeeded: %d items", len(items))
}

func TestNormalizeRuleTargetsAcceptsZcode(t *testing.T) {
	// "zcode" is a valid standalone target.
	got, err := normalizeRuleTargets(t.TempDir(), []string{"zcode"})
	if err != nil {
		t.Fatalf("zcode should validate, got error: %v", err)
	}
	if len(got) != 1 || got[0] != "zcode" {
		t.Fatalf("expected [zcode], got %+v", got)
	}

	// "all" expansion must include zcode alongside the other concrete IDEs.
	allOut, err := normalizeRuleTargets(t.TempDir(), []string{"all"})
	if err != nil {
		t.Fatalf("all should validate: %v", err)
	}
	if !contains(allOut, "zcode") {
		t.Fatalf("expected 'all' to expand to include zcode, got %+v", allOut)
	}

	// "generic" must NOT include zcode — generic is for vendor-agnostic dotfiles only.
	genOut, err := normalizeRuleTargets(t.TempDir(), []string{"generic"})
	if err != nil {
		t.Fatalf("generic should validate: %v", err)
	}
	if contains(genOut, "zcode") {
		t.Fatalf("generic must not expand to zcode, got %+v", genOut)
	}

	// Unknown ide still errors.
	if _, err := normalizeRuleTargets(t.TempDir(), []string{"nonsense"}); err == nil {
		t.Fatalf("expected error for invalid ide")
	}
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func TestManagerInitAutoDetectsZcodeWhenNoIDEFlagsProvided(t *testing.T) {
	base := t.TempDir()
	cwd := t.TempDir()
	// Seed an existing AGENTS.md so auto-detection picks zcode.
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("# existing\n"), 0o644); err != nil {
		t.Fatalf("seed AGENTS.md: %v", err)
	}
	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	out, err := mgr.Init(context.Background(), InitOptions{
		CWD:         cwd,
		ProjectName: "proj-zcode",
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(out.RuleFiles) != 1 {
		t.Fatalf("expected 1 rule file, got %d (%+v)", len(out.RuleFiles), out.RuleFiles)
	}
	agentsMd := filepath.Join(cwd, "AGENTS.md")
	if out.RuleFiles[0] != agentsMd {
		t.Fatalf("expected AGENTS.md rule path %s, got %+v", agentsMd, out.RuleFiles)
	}
	b, err := os.ReadFile(agentsMd)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "workspace: proj-zcode") {
		t.Fatalf("expected workspace in AGENTS.md, got: %s", content)
	}
	if !strings.Contains(content, "## agent-memory (MANDATORY)") {
		t.Fatalf("expected MANDATORY marker in AGENTS.md, got: %s", content)
	}
	// Cursor rule must NOT be written in a ZCode-only repo.
	if _, err := os.Stat(filepath.Join(cwd, ".cursor", "rules", "agent-memory.mdc")); !os.IsNotExist(err) {
		t.Fatalf("expected cursor rule NOT to be written in a ZCode-only repo")
	}
}

func TestManagerReinstallWritesZcodeAgentsMd(t *testing.T) {
	base := t.TempDir()
	cwd := t.TempDir()
	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if _, err := mgr.Init(context.Background(), InitOptions{
		CWD:         cwd,
		ProjectName: "proj-zcode-reinstall",
		NoRule:      true,
	}); err != nil {
		t.Fatalf("init: %v", err)
	}

	out, err := mgr.Reinstall(context.Background(), ReinstallOptions{
		CWD:         cwd,
		ProjectName: "proj-zcode-reinstall",
		Force:       true,
		IDEs:        []string{"zcode"},
	})
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if out.AgentFiles == nil {
		t.Fatalf("expected agent files result")
	}
	agentsMd := filepath.Join(cwd, "AGENTS.md")
	b, err := os.ReadFile(agentsMd)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.Contains(string(b), "workspace: proj-zcode-reinstall") {
		t.Fatalf("expected workspace in AGENTS.md, got: %s", string(b))
	}
}

func TestNormalizeRuleTargetsIncludesCodex(t *testing.T) {
	targets, err := normalizeRuleTargets(t.TempDir(), []string{"codex"})
	if err != nil {
		t.Fatalf("normalize codex: %v", err)
	}
	if !contains(targets, "codex") {
		t.Fatalf("expected codex target, got %+v", targets)
	}

	all, err := normalizeRuleTargets(t.TempDir(), []string{"all"})
	if err != nil {
		t.Fatalf("normalize all: %v", err)
	}
	if !contains(all, "codex") {
		t.Fatalf("expected all to include codex, got %+v", all)
	}
}

func TestManagerInitWritesCodexArtifacts(t *testing.T) {
	base := filepath.Join(t.TempDir(), "agent memory data")
	cwd := t.TempDir()
	mgr, err := NewManager(base)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	out, err := mgr.Init(context.Background(), InitOptions{
		CWD:         cwd,
		ProjectName: "proj-codex",
		IDEs:        []string{"codex"},
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}

	for _, path := range []string{
		filepath.Join(cwd, "AGENTS.md"),
		filepath.Join(cwd, ".codex", "config.toml"),
		filepath.Join(cwd, ".codex", "hooks.json"),
	} {
		if !contains(out.RuleFiles, path) {
			t.Fatalf("expected %s in generated files: %+v", path, out.RuleFiles)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected generated file %s: %v", path, err)
		}
	}

	config, err := os.ReadFile(filepath.Join(cwd, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(config), filepath.ToSlash(base)) {
		t.Fatalf("expected data dir in Codex config, got %s", config)
	}

	hooks, err := os.ReadFile(filepath.Join(cwd, ".codex", "hooks.json"))
	if err != nil {
		t.Fatalf("read hooks: %v", err)
	}
	for _, want := range []string{"UserPromptSubmit", "Stop", "agent-memory"} {
		if !strings.Contains(string(hooks), want) {
			t.Fatalf("expected %q in Codex hooks, got %s", want, hooks)
		}
	}
}

func TestWriteAgentFilesPreservesCodexConfigAndHooks(t *testing.T) {
	cwd := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "memory")
	if err := os.MkdirAll(filepath.Join(cwd, ".codex"), 0o755); err != nil {
		t.Fatalf("mkdir codex: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".codex", "config.toml"), []byte("model = \"gpt-test\"\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".codex", "hooks.json"), []byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"custom-hook"}]}]}}`), 0o644); err != nil {
		t.Fatalf("seed hooks: %v", err)
	}

	for range 2 {
		if _, err := WriteAgentFiles(WriteAgentFilesOptions{
			CWD:       cwd,
			Workspace: "proj-codex-preserve",
			DataDir:   dataDir,
			IDEs:      []string{"codex"},
		}); err != nil {
			t.Fatalf("write Codex files: %v", err)
		}
	}

	config, _ := os.ReadFile(filepath.Join(cwd, ".codex", "config.toml"))
	if !strings.Contains(string(config), `model = "gpt-test"`) || strings.Count(string(config), dataDir) != 1 {
		t.Fatalf("expected preserved config and one data root, got %s", config)
	}
	hooks, _ := os.ReadFile(filepath.Join(cwd, ".codex", "hooks.json"))
	if !strings.Contains(string(hooks), "custom-hook") || strings.Count(string(hooks), codexHookMarker) != 2 {
		t.Fatalf("expected preserved custom hook and two managed hooks, got %s", hooks)
	}
}

func TestWriteCodexGlobalFilesPreservesExistingSettings(t *testing.T) {
	codexHome := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "agent-memory")
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"gpt-test\"\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "hooks.json"), []byte(`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"custom-hook"}]}]}}`), 0o644); err != nil {
		t.Fatalf("seed hooks: %v", err)
	}

	for range 2 {
		if _, err := WriteCodexGlobalFiles(codexHome, dataDir); err != nil {
			t.Fatalf("write global Codex files: %v", err)
		}
	}
	config, _ := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if !strings.Contains(string(config), `model = "gpt-test"`) || strings.Count(string(config), dataDir) != 1 {
		t.Fatalf("expected preserved config and one data root, got %s", config)
	}
	hooks, _ := os.ReadFile(filepath.Join(codexHome, "hooks.json"))
	if !strings.Contains(string(hooks), "custom-hook") || strings.Count(string(hooks), codexHookMarker) != 2 {
		t.Fatalf("expected preserved custom and managed hooks, got %s", hooks)
	}
}

func TestWriteCodexGlobalFilesPreservesExistingWritableRoots(t *testing.T) {
	codexHome := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "agent-memory")
	existingRoot := "/existing/writable/root"
	seed := "[sandbox_workspace_write]\nwritable_roots = [\"" + existingRoot + "\"]\n"
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	for range 2 {
		if _, err := WriteCodexGlobalFiles(codexHome, dataDir); err != nil {
			t.Fatalf("write global Codex files: %v", err)
		}
	}
	config, _ := os.ReadFile(filepath.Join(codexHome, "config.toml"))
	if strings.Count(string(config), existingRoot) != 1 || strings.Count(string(config), dataDir) != 1 {
		t.Fatalf("expected both writable roots exactly once, got %s", config)
	}
}
