package harnesstools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessapproval"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
)

// decisionHub builds a hub over a real registry and session, from whichever decision
// provider the test registers.
func decisionHub(t *testing.T, register func(*harness.Registry) error, tweak ...func(*harnessdecide.Config)) *harnessdecide.Hub {
	t.Helper()
	registry := harness.NewRegistry()
	if err := register(registry); err != nil {
		t.Fatal(err)
	}
	session, err := registry.OpenSession(context.Background(), "risk-advice", harness.Scope{Workspace: "ws", Run: "run-1", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	cfg := harnessdecide.Config{Asker: session, Capability: "decide", Enabled: harnessdecide.Kinds()}
	for _, fn := range tweak {
		fn(&cfg)
	}
	hub, err := harnessdecide.NewHub(cfg, 8)
	if err != nil {
		t.Fatal(err)
	}
	return hub
}

func fakeProvider(b harnesstest.Behavior) func(*harness.Registry) error {
	return func(r *harness.Registry) error {
		_, err := harnesstest.Register(r, harnesstest.Manifest("risk-advice", harness.KindDecision, "decide"), b)
		return err
	}
}

func scriptedProvider(p *riskPicker) func(*harness.Registry) error {
	return func(r *harness.Registry) error {
		manifest := harness.Manifest{Version: harness.ContractVersion, ID: "risk-advice", Kind: harness.KindDecision, Capabilities: []harness.CapabilityID{"decide"}}
		return r.Register(manifest, func() (harness.Provider, error) { return p, nil })
	}
}

const appEditArgs = `{"path":"internal/app/app.go","edits":[{"old_text":"// TODO: fix the needle handling","new_text":"// fixed: needle handling"}]}`

// An edit goes through the real manager, the real tool and the real approval store with the
// advised policy in front, and ends only when a person approves it with the typed code.
func approvedEdit(t *testing.T, hub *harnessdecide.Hub) (harnessapproval.Record, *editLoop) {
	t.Helper()
	policy := NewAdvisedPolicy(ProjectPolicy(), ServiceRiskAdvisor{For: hub.Service})
	l := newEditLoop(t, []harnesstest.Step{{ToolID: ToolEditFile, Arguments: appEditArgs}, {Text: "done"}}, policy)
	original := l.file("internal/app/app.go")
	started := l.start("key-00000001")
	record := l.parked(started.ID)
	if l.file("internal/app/app.go") != original {
		t.Fatal("the file changed before anyone approved")
	}
	if record.Tool != ToolEditFile || record.State != harnessapproval.StatePending {
		t.Fatalf("record = %+v", record)
	}
	return record, l
}

func finishApproval(t *testing.T, l *editLoop, record harnessapproval.Record) {
	t.Helper()
	l.approve(record)
	status := l.wait(record.RunID, "completed", "failed")
	if status.State != "completed" || !strings.Contains(l.file("internal/app/app.go"), "// fixed: needle handling") {
		t.Fatalf("after approval: %+v", status)
	}
}

func TestAHostileDecisionProviderCannotLowerFrictionOrSkipTheApproval(t *testing.T) {
	// Every way a provider can answer "this is routine", or fail to answer: the edit still
	// parks with ordinary friction, nothing is written before a person approves, and nothing
	// new appears in the reasons.
	providers := map[string]func(*harness.Registry) error{
		"routine with confidence":   scriptedProvider(&riskPicker{positions: []int{0}, conf: 0.99}),
		"a denial":                  fakeProvider(harnesstest.Behavior{Outcome: harness.OutcomeDenied}),
		"unavailable":               fakeProvider(harnesstest.Behavior{Outcome: harness.OutcomeUnavailable}),
		"a failure":                 fakeProvider(harnesstest.Behavior{Outcome: harness.OutcomeFailed}),
		"a stale reply":             fakeProvider(harnesstest.Behavior{StaleReply: true}),
		"a transport error":         fakeProvider(harnesstest.Behavior{Err: errors.New("connection reset")}),
		"a panic":                   fakeProvider(harnesstest.Behavior{Panic: true}),
		"an unknown choice":         fakeProvider(harnesstest.Behavior{UnknownChoice: true}),
		"an oversize reply":         fakeProvider(harnesstest.Behavior{Oversize: true}),
		"nobody home (low trust)":   scriptedProvider(&riskPicker{positions: []int{2}, conf: 0.1}),
		"a healthy but routine one": fakeProvider(harnesstest.Behavior{}), // the fake always picks the first label
	}
	for name, register := range providers {
		record, l := approvedEdit(t, decisionHub(t, register))
		if record.Friction != harnessapproval.FrictionStandard || len(record.Reasons) != 0 || len(harnessapproval.Code(record)) != 4 {
			t.Errorf("%s: friction %q reasons %v code %q", name, record.Friction, record.Reasons, harnessapproval.Code(record))
		}
		finishApproval(t, l, record)
	}
}

func TestAHazardousVerdictOnlyEverAddsFrictionAndTheApprovalStillBindsTheSameChange(t *testing.T) {
	p := &riskPicker{positions: []int{2}, conf: 0.9}
	record, l := approvedEdit(t, decisionHub(t, scriptedProvider(p)))
	if record.Friction != harnessapproval.FrictionStrict || !contains(record.Reasons, ReasonJevHazardous) || len(harnessapproval.Code(record)) <= 4 {
		t.Fatalf("friction %q reasons %v code %q", record.Friction, record.Reasons, harnessapproval.Code(record))
	}
	// What the person reviews is the same exact change, and it applies only with their approval.
	if !strings.Contains(record.Preview, "+// fixed: needle handling") || !strings.HasPrefix(record.Digest, "sha256:") {
		t.Fatalf("preview %q digest %q", record.Preview, record.Digest)
	}
	// The provider never saw the edit: no path, no text.
	wire := ""
	for _, q := range p.questions {
		wire += strings.Join(q.Candidates, ",") + " "
		for _, e := range q.Evidence {
			wire += e.ID + e.Class + e.Revision + e.Note + " "
		}
	}
	for _, private := range []string{"app.go", "needle", "internal/", "fixed"} {
		if strings.Contains(wire, private) {
			t.Errorf("the decision provider was told %q: %s", private, wire)
		}
	}
	finishApproval(t, l, record)
}

func TestARequiredAdvisorThatCannotAnswerAddsCautionAndNeverBlocksTheRun(t *testing.T) {
	hub := decisionHub(t, fakeProvider(harnesstest.Behavior{Outcome: harness.OutcomeUnavailable}), func(c *harnessdecide.Config) {
		c.Required = []harnessdecide.Kind{harnessdecide.KindCommandRisk}
	})
	record, l := approvedEdit(t, hub)
	if record.Friction != harnessapproval.FrictionStrict || !contains(record.Reasons, ReasonJevCautious) {
		t.Fatalf("friction %q reasons %v", record.Friction, record.Reasons)
	}
	finishApproval(t, l, record)
}

func TestASlowDecisionProviderCostsOneDeadlineAndNeverStopsTheApproval(t *testing.T) {
	hub := decisionHub(t, fakeProvider(harnesstest.Behavior{Delay: 10 * time.Second, IgnoreContext: true}))
	begin := time.Now()
	record, l := approvedEdit(t, hub)
	if time.Since(begin) > 6*time.Second {
		t.Fatalf("the slow provider held the run for %v", time.Since(begin))
	}
	if record.Friction != harnessapproval.FrictionStandard || len(record.Reasons) != 0 {
		t.Fatalf("friction %q reasons %v", record.Friction, record.Reasons)
	}
	finishApproval(t, l, record)
}

func TestAdviceNeverTurnsADenialIntoAQuestionOrAnApproval(t *testing.T) {
	// A hidden file is denied by the tool before policy is asked, whatever any advisor says.
	hub := decisionHub(t, scriptedProvider(&riskPicker{positions: []int{0}, conf: 0.99}), func(c *harnessdecide.Config) {
		c.Required = []harnessdecide.Kind{harnessdecide.KindCommandRisk}
	})
	policy := NewAdvisedPolicy(ProjectPolicy(), ServiceRiskAdvisor{For: hub.Service})
	l := newEditLoop(t, []harnesstest.Step{
		{ToolID: ToolEditFile, Arguments: `{"path":".env","edits":[{"old_text":"DATABASE","new_text":"X"}]}`},
		{ToolID: ToolDeleteFile, Arguments: `{"path":".git/config"}`},
		{Text: "done"},
	}, policy)
	before := l.file(".env")
	started := l.start("key-00000002")
	status := l.wait(started.ID, "completed", "needs_attention", "failed")
	if status.State != "completed" || len(l.allApprovals()) != 0 || l.file(".env") != before {
		t.Fatalf("state %s, %d approvals, file changed %v", status.State, len(l.allApprovals()), l.file(".env") != before)
	}
}

func TestAnActionTheTierTableLeavesUnclassifiedIsStillDeniedWhateverTheAdvisorSays(t *testing.T) {
	hub := decisionHub(t, scriptedProvider(&riskPicker{positions: []int{0}, conf: 0.99}))
	policy := NewAdvisedPolicy(ReadOnlyPolicy(), ServiceRiskAdvisor{For: hub.Service})
	l := newEditLoop(t, []harnesstest.Step{{ToolID: ToolEditFile, Arguments: appEditArgs}, {Text: "done"}}, policy)
	original := l.file("internal/app/app.go")
	started := l.start("key-00000003")
	l.wait(started.ID, "completed", "failed")
	if l.file("internal/app/app.go") != original || len(l.allApprovals()) != 0 {
		t.Fatalf("a tool the policy does not offer was run or asked about: %d approvals", len(l.allApprovals()))
	}
}
