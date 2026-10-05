package harnesscontext

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

func runManager(t *testing.T, source harnessrun.ContextSource) (*harnessrun.Manager, *harnesstest.Counters) {
	t.Helper()
	registry := harness.NewRegistry()
	model, err := harnesstest.Register(registry, harnesstest.Manifest("fake-model", harness.KindModel, "generation"), harnesstest.Behavior{Script: []harnesstest.Step{{Text: "done"}}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := harnessrun.NewManager(harnessrun.Config{DataDir: t.TempDir(), Registry: registry,
		Model: harnessrun.Binding{Provider: "fake-model", Capability: "generation"}, Context: source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m, model
}

func runOnce(t *testing.T, m *harnessrun.Manager) harnessrun.Status {
	t.Helper()
	owner := harnessrun.Owner{ClientID: "claude-desktop", Workspace: ws, GrantID: strings.Repeat("b", 32), GrantRevision: 1}
	started, err := m.Start(context.Background(), owner, harnessrun.StartRequest{IdempotencyKey: "key-00000001", Goal: "use the project rules"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		status, err := m.Status(context.Background(), owner, started.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.State.Terminal() {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %+v", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func refIDs(c *harnesstest.Counters) []string {
	refs, _ := c.Refs.Load().([]harness.EvidenceRef)
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = r.ID
	}
	return out
}

func TestTheRunLoopUsesAssembledAuthorizedEvidence(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "project rules")
	write(t, root, "main.go", "package main")
	source := &RunSource{
		Assembler:   New(Config{}),
		Eligibility: Eligibility{MaxSensitivity: SensitivityInternal, AdvisorMaxSensitivity: SensitivityInternal},
		Budget:      Budget{Total: 4000, ToolSchema: 100, ReserveOutput: 200},
		Gather: func(_ context.Context, owner harnessrun.Owner) ([]Chunk, error) {
			rules, _ := InstructionChunks(owner.Workspace, root)
			files, _ := RepositoryChunks(owner.Workspace, root, []PathHint{{Path: "main.go", Relevance: 0.7}, {Path: ".env"}})
			foreign := chunk("foreign", 1, "OTHER-WORKSPACE")
			foreign.Workspace = "another-project"
			secret := chunk("secret", 1, "RESTRICTED")
			secret.Sensitivity = SensitivityRestricted
			return append(append(rules, files...), foreign, secret), nil
		},
	}
	m, model := runManager(t, source)
	if status := runOnce(t, m); status.State != harnessrun.StateCompleted {
		t.Fatalf("status = %+v", status)
	}
	got := refIDs(model)
	want := map[string]bool{"run-goal": true, "instr-CLAUDE.md": true, repoID("main.go"): true}
	if len(got) != len(want) {
		t.Fatalf("refs = %v", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("an unauthorized or unexpected chunk reached the model request: %q in %v", id, got)
		}
	}
}

type failingSource struct{ err error }

func (f failingSource) Context(context.Context, harnessrun.Snapshot) (harnessrun.PromptContext, error) {
	return harnessrun.PromptContext{}, f.err
}

type emptySource struct{}

func (emptySource) Context(context.Context, harnessrun.Snapshot) (harnessrun.PromptContext, error) {
	return harnessrun.PromptContext{}, nil
}

func TestAFaultyContextSourceNeverStopsARun(t *testing.T) {
	for name, source := range map[string]harnessrun.ContextSource{
		"error":        failingSource{err: errors.New("assembler exploded with a secret")},
		"no refs":      emptySource{},
		"no budget":    &RunSource{Assembler: New(Config{}), Budget: Budget{Total: 5}},
		"no assembler": &RunSource{},
	} {
		t.Run(name, func(t *testing.T) {
			m, model := runManager(t, source)
			status := runOnce(t, m)
			if status.State != harnessrun.StateCompleted {
				t.Fatalf("status = %+v", status)
			}
			if got := refIDs(model); len(got) == 0 || got[0] != "goal" {
				t.Fatalf("the loop did not fall back to its own references: %v", got)
			}
		})
	}
}

type allowTool struct{}

func (allowTool) Decide(context.Context, harnessrun.Owner, harness.PreparedAction) harnessrun.Decision {
	return harnessrun.DecisionAllow
}

func TestTheLoopSendsARealPromptWithAStablePrefixAcrossTurns(t *testing.T) {
	root := t.TempDir()
	write(t, root, "CLAUDE.md", "always run the tests")
	source := &RunSource{
		Assembler:   New(Config{}),
		Eligibility: Eligibility{MaxSensitivity: SensitivityRestricted, AdvisorMaxSensitivity: SensitivityInternal},
		Budget:      Budget{Total: 6000, ToolSchema: 100, ReserveOutput: 300},
		Gather: func(_ context.Context, owner harnessrun.Owner) ([]Chunk, error) {
			rules, _ := InstructionChunks(owner.Workspace, root)
			secret := chunk("secret-note", 1, "RESTRICTED-NOTE")
			secret.Sensitivity = SensitivityRestricted
			return append(rules, secret), nil
		},
	}
	registry := harness.NewRegistry()
	model, err := harnesstest.Register(registry, harnesstest.Manifest("fake-model", harness.KindModel, "generation"),
		harnesstest.Behavior{Script: []harnesstest.Step{{ToolID: "read", Text: "step one", Arguments: "{}"}, {ToolID: "read", Text: "step two", Arguments: "{}"}, {Text: "done"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harnesstest.Register(registry, harnesstest.Manifest("fake-tool", harness.KindTool, "read"), harnesstest.Behavior{}); err != nil {
		t.Fatal(err)
	}
	m, err := harnessrun.NewManager(harnessrun.Config{DataDir: t.TempDir(), Registry: registry,
		Model: harnessrun.Binding{Provider: "fake-model", Capability: "generation"}, Tool: &harnessrun.Binding{Provider: "fake-tool", Capability: "read"},
		Policy: allowTool{}, Context: source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if status := runOnce(t, m); status.State != harnessrun.StateCompleted {
		t.Fatalf("status = %+v", status)
	}
	history := model.History()
	if len(history) != 3 {
		t.Fatalf("the model was called %d times", len(history))
	}
	for i, req := range history {
		if !strings.HasPrefix(req.Prompt, PolicyText) || !strings.Contains(req.Prompt, "│ always run the tests") || !strings.Contains(req.Prompt, "│ use the project rules") {
			t.Fatalf("turn %d prompt is not the assembled context: %.200s", i, req.Prompt)
		}
		if req.PrefixID == "" || req.PrefixID != history[0].PrefixID {
			t.Fatalf("turn %d prefix %q differs from turn 0 %q", i, req.PrefixID, history[0].PrefixID)
		}
		cut := strings.Index(req.Prompt, "=== BEGIN EVIDENCE")
		if cut < 0 || req.Prompt[:cut] != history[0].Prompt[:strings.Index(history[0].Prompt, "=== BEGIN EVIDENCE")] {
			t.Fatalf("turn %d: the leading part of the prompt changed, so the prefix identity is a false claim", i)
		}
	}
	// Later turns carry the earlier model output as quoted evidence.
	if !strings.Contains(history[2].Prompt, "│ step one") {
		t.Fatalf("an earlier turn's output is missing from a later prompt")
	}
	if history[2].Prompt == history[0].Prompt {
		t.Fatal("the prompt did not grow with the run")
	}
}

func TestRunSourceReportsTheDataClassAndTokenSizeOfWhatItAssembled(t *testing.T) {
	secret := chunk("secret-note", 1, "RESTRICTED-NOTE")
	secret.Sensitivity = SensitivityRestricted
	build := func(max Sensitivity) harnessrun.PromptContext {
		source := &RunSource{Assembler: fixedAssembler(Config{}), Eligibility: Eligibility{MaxSensitivity: max, AdvisorMaxSensitivity: SensitivityInternal},
			Budget: Budget{Total: 4000, ReserveOutput: 200}, Gather: func(context.Context, harnessrun.Owner) ([]Chunk, error) { return []Chunk{secret}, nil }}
		pc, err := source.Context(context.Background(), harnessrun.Snapshot{RunID: "run_x", Owner: harnessrun.Owner{ClientID: "claude-desktop", Workspace: ws},
			Chunks: []harnessrun.Chunk{{ID: "goal", Kind: "goal", Text: "do the thing"}}})
		if err != nil {
			t.Fatal(err)
		}
		return pc
	}
	internal, restricted := build(SensitivityInternal), build(SensitivityRestricted)
	if internal.Class != int(SensitivityInternal) || strings.Contains(internal.Prompt, "RESTRICTED-NOTE") {
		t.Fatalf("internal class = %d", internal.Class)
	}
	if restricted.Class != int(SensitivityRestricted) || !strings.Contains(restricted.Prompt, "RESTRICTED-NOTE") {
		t.Fatalf("restricted class = %d", restricted.Class)
	}
	if internal.InputTokens <= 0 || internal.InputTokens > 4000-200 || len(internal.Refs) == 0 || internal.PrefixID == "" {
		t.Fatalf("pc = %+v", internal)
	}
	if _, err := (&RunSource{}).Context(context.Background(), harnessrun.Snapshot{}); err == nil {
		t.Fatal("a source with no assembler must fail")
	}
}
