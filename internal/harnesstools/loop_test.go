package harnesstools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

var loopOwner = harnessrun.Owner{ClientID: "claude-desktop", Workspace: workspace, GrantID: strings.Repeat("b", 32), GrantRevision: 1}

type loopFixture struct {
	t       *testing.T
	dir     string
	manager *harnessrun.Manager
	model   *harnesstest.Counters
}

func newLoop(t *testing.T, e *env, script []harnesstest.Step, policy harnessrun.ToolPolicy) *loopFixture {
	t.Helper()
	registry := harness.NewRegistry()
	model, err := harnesstest.Register(registry, harnesstest.Manifest("fake-model", harness.KindModel, "generation"), harnesstest.Behavior{Script: script})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.provider.Register(registry); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	manager, err := harnessrun.NewManager(harnessrun.Config{DataDir: dir, Registry: registry,
		Model: harnessrun.Binding{Provider: "fake-model", Capability: "generation"}, Tool: &harnessrun.Binding{Provider: ProviderID, Capability: ToolReadFile},
		Policy: policy, Redact: redact})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return &loopFixture{t: t, dir: dir, manager: manager, model: model}
}

func (f *loopFixture) run() (harnessrun.Status, string, []string) {
	f.t.Helper()
	started, err := f.manager.Start(context.Background(), loopOwner, harnessrun.StartRequest{IdempotencyKey: "key-00000001", Goal: "explore the project"})
	if err != nil {
		f.t.Fatal(err)
	}
	var status harnessrun.Status
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if status, err = f.manager.Status(context.Background(), loopOwner, started.ID); err != nil {
			f.t.Fatal(err)
		}
		if status.State.Terminal() {
			break
		}
	}
	stored, err := os.ReadFile(filepath.Join(f.dir, "harness", "runs", started.ID+".json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var codes []string
	cursor := ""
	for {
		page, err := f.manager.Events(context.Background(), loopOwner, started.ID, cursor, 100)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, ev := range page.Events {
			codes = append(codes, ev.Kind+":"+ev.Code)
		}
		if len(page.Events) == 0 {
			break
		}
		cursor = page.Next
	}
	return status, string(stored), codes
}

func count(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}

func TestARunExploresAProjectWithTheReadToolsAndNothingProtectedReachesIt(t *testing.T) {
	e := newEnv(t)
	f := newLoop(t, e, []harnesstest.Step{
		{ToolID: ToolReadFile, Arguments: `{"path":"main.go"}`, Text: "reading main"},
		{ToolID: ToolListDir, Arguments: `{"path":"internal"}`},
		{ToolID: ToolSearch, Arguments: `{"query":"needle","path":"internal"}`},
		{ToolID: ToolReadFile, Arguments: `{"path":"leaky.go"}`},
		{ToolID: ToolReadFile, Arguments: `{"path":".env"}`},
		{ToolID: ToolReadFile, Arguments: `{"path":"../outside.txt"}`},
		{ToolID: ToolReadFile, Arguments: `{"path":"link-out.txt"}`},
		{Text: "done exploring"},
	}, ReadOnlyPolicy())
	status, stored, codes := f.run()
	if status.State != harnessrun.StateCompleted || status.Usage.ToolCalls != 7 {
		t.Fatalf("status = %+v", status)
	}
	if count(codes, "tool:tool_ok") < 3 || count(codes, "tool:tool_denied") < 2 {
		t.Fatalf("events = %v", codes)
	}
	for _, want := range []string{"package main", "internal/app/app.go", "needle"} {
		if !strings.Contains(stored, want) {
			t.Errorf("tool output %q never reached the run", want)
		}
	}
	for _, leaked := range []string{"hunter2hunter2", "DATABASE_PASSWORD", "AKIAIOSFODNN7EXAMPLE", "OUTSIDE-SECRET"} {
		if strings.Contains(stored, leaked) {
			t.Errorf("%q was persisted in the run", leaked)
		}
	}
	if status.Usage.OutputBytes <= 0 {
		t.Fatal("tool output was not counted against the output budget")
	}
}

func TestTheDefaultPolicyRunsNoToolEvenForASafeRead(t *testing.T) {
	e := newEnv(t)
	f := newLoop(t, e, []harnesstest.Step{{ToolID: ToolReadFile, Arguments: `{"path":"main.go"}`}, {Text: "done"}}, nil)
	status, stored, codes := f.run()
	if status.State != harnessrun.StateCompleted || count(codes, "tool:tool_denied") != 1 || strings.Contains(stored, "package main") {
		t.Fatalf("status=%+v events=%v", status, codes)
	}
}

func TestAModelCannotCallAToolThatIsNotOfferedSoItCannotWrite(t *testing.T) {
	e := newEnv(t)
	before, _ := os.ReadFile(filepath.Join(e.root, "main.go"))
	for _, tool := range []string{"write_file", "edit_file", "run_command", "git_push", "delete_file"} {
		f := newLoop(t, e, []harnesstest.Step{{ToolID: tool, Arguments: `{"path":"main.go","content":"pwned"}`}}, ReadOnlyPolicy())
		status, _, _ := f.run()
		if status.State != harnessrun.StateFailed || status.Code != "model_contract" {
			t.Errorf("%s: status = %+v", tool, status)
		}
	}
	if after, _ := os.ReadFile(filepath.Join(e.root, "main.go")); string(after) != string(before) {
		t.Fatal("a file changed")
	}
}

func TestWhenTheProjectRootIsUnavailableNoToolIsOffered(t *testing.T) {
	e := newEnv(t)
	e.current = func() string { return filepath.Join(e.root, "gone") }
	f := newLoop(t, e, []harnesstest.Step{{ToolID: ToolReadFile, Arguments: `{"path":"main.go"}`}}, ReadOnlyPolicy())
	if status, _, _ := f.run(); status.State != harnessrun.StateFailed || status.Code != "model_contract" {
		t.Fatalf("status = %+v", status)
	}
}

func TestAnOversizedToolResultCountsAgainstTheOutputBudget(t *testing.T) {
	e := newEnv(t)
	started := make([]harnesstest.Step, 0, 12)
	for i := 0; i < 12; i++ {
		started = append(started, harnesstest.Step{ToolID: ToolSearch, Arguments: `{"query":"needle","max_results":100}`})
	}
	f := newLoop(t, e, started, ReadOnlyPolicy())
	started2, err := f.manager.Start(context.Background(), loopOwner, harnessrun.StartRequest{IdempotencyKey: "key-00000002", Goal: "search a lot", Budget: harnessrun.Budget{MaxOutputBytes: 3000}})
	if err != nil {
		t.Fatal(err)
	}
	var status harnessrun.Status
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		status, _ = f.manager.Status(context.Background(), loopOwner, started2.ID)
		if status.State.Terminal() {
			break
		}
	}
	if status.State != harnessrun.StatePartial || status.Code != "budget_output" {
		t.Fatalf("status = %+v", status)
	}
}
