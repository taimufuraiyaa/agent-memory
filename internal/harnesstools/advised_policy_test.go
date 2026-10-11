package harnesstools

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

type stubRisk struct {
	risk  harnessdecide.Risk
	out   harnessdecide.Outcome
	calls atomic.Int32
}

func (s *stubRisk) Risk(context.Context, harness.PreparedAction) (harnessdecide.Risk, harnessdecide.Outcome) {
	s.calls.Add(1)
	return s.risk, s.out
}

// strictness orders decisions from least to most permissive: nothing runs, a person must
// look closely, a person must approve, and it runs without asking.
func strictness(d harnessrun.Decision) int {
	switch d {
	case harnessrun.DecisionDeny:
		return 3
	case harnessrun.DecisionAskStrict:
		return 2
	case harnessrun.DecisionAsk:
		return 1
	}
	return 0
}

func act(capability string, digest string, paths []string, escalate ...string) harness.PreparedAction {
	return harness.PreparedAction{Envelope: harness.Envelope{Capability: harness.CapabilityID(capability), Scope: harness.Scope{Workspace: "ws", Run: "run-1", Generation: 1}},
		Outcome: harness.OutcomeOK, Digest: "sha256:" + digest + strings.Repeat("0", 64-len(digest)), Paths: paths, Escalate: escalate}
}

func policyActions() map[string]harness.PreparedAction {
	failed := act("edit_file", "f1", []string{"a.go"})
	failed.Outcome = harness.OutcomeFailed
	nodigest := act("edit_file", "f2", []string{"a.go"})
	nodigest.Digest = ""
	return map[string]harness.PreparedAction{
		"a read":                   act("read_file", "01", []string{"a.go"}),
		"an edit":                  act("edit_file", "02", []string{"a.go"}),
		"an edit of a manifest":    act("edit_file", "03", []string{"go.mod"}),
		"a catalogued command":     act("run_command", "04", []string{"."}),
		"a recipe":                 act("run_command", "05", []string{"."}, "runs_recipes"),
		"a deletion":               act("delete_file", "06", []string{"a.go"}),
		"a stage":                  act("git_stage", "07", []string{"a.go"}),
		"a tool nobody classified": act("mystery_tool", "08", nil),
		"a failed preparation":     failed,
		"no digest":                nodigest,
	}
}

func TestAdviceCanOnlyAddCautionToWhatTheBasePolicyDecided(t *testing.T) {
	base := ProjectPolicy()
	advice := []struct {
		name string
		risk harnessdecide.Risk
		out  harnessdecide.Outcome
	}{
		{"routine", harnessdecide.RiskRoutine, harnessdecide.Outcome{Status: harnessdecide.StatusApplied}},
		{"careful", harnessdecide.RiskCareful, harnessdecide.Outcome{Status: harnessdecide.StatusApplied}},
		{"hazardous", harnessdecide.RiskHazardous, harnessdecide.Outcome{Status: harnessdecide.StatusApplied}},
		{"an unknown risk", "perfectly-safe", harnessdecide.Outcome{Status: harnessdecide.StatusApplied}},
		{"no advice", "", harnessdecide.Outcome{Status: harnessdecide.StatusTimeout}},
		{"cautious", harnessdecide.RiskCareful, harnessdecide.Outcome{Status: harnessdecide.StatusFailed, Cautious: true}},
	}
	for actionName, action := range policyActions() {
		want := base.Decide(context.Background(), harnessrunOwner(), action)
		for _, a := range advice {
			stub := &stubRisk{risk: a.risk, out: a.out}
			got := NewAdvisedPolicy(base, stub).Decide(context.Background(), harnessrunOwner(), action)
			if strictness(got) < strictness(want) {
				t.Errorf("%s with advice %s: %v is more permissive than the base's %v", actionName, a.name, got, want)
			}
			if want != decisionAsk && got != want {
				t.Errorf("%s with advice %s: the base's %v became %v", actionName, a.name, want, got)
			}
			if want != decisionAsk && stub.calls.Load() != 0 {
				t.Errorf("%s: the advisor was asked about a %v", actionName, want)
			}
			if want == decisionAsk {
				raises := a.name == "careful" || a.name == "hazardous" || a.name == "cautious"
				if raises != (got == decisionAskStrict) {
					t.Errorf("%s with advice %s: decided %v from %v", actionName, a.name, got, want)
				}
			}
		}
	}
}

func TestTheReasonShownIsTheOneTheAdviceJustified(t *testing.T) {
	base := ProjectPolicy()
	action := act("run_command", "0a", []string{"."}) // a catalogued command: an ordinary ask
	for name, tc := range map[string]struct {
		risk harnessdecide.Risk
		out  harnessdecide.Outcome
		want string
	}{
		"careful":   {harnessdecide.RiskCareful, harnessdecide.Outcome{Status: harnessdecide.StatusApplied}, ReasonJevCareful},
		"hazardous": {harnessdecide.RiskHazardous, harnessdecide.Outcome{Status: harnessdecide.StatusApplied}, ReasonJevHazardous},
		"cautious":  {harnessdecide.RiskCareful, harnessdecide.Outcome{Status: harnessdecide.StatusTimeout, Cautious: true}, ReasonJevCautious},
		"routine":   {harnessdecide.RiskRoutine, harnessdecide.Outcome{Status: harnessdecide.StatusApplied}, ""},
	} {
		p := NewAdvisedPolicy(base, &stubRisk{risk: tc.risk, out: tc.out})
		if reasons := p.Reasons(action); !reflect.DeepEqual(reasons, base.Reasons(action)) {
			t.Errorf("%s: reasons before any decision = %v", name, reasons)
		}
		p.Decide(context.Background(), harnessrunOwner(), action)
		reasons := p.Reasons(action)
		want := append([]string(nil), base.Reasons(action)...)
		if tc.want != "" {
			want = append(want, tc.want)
		}
		if !reflect.DeepEqual(reasons, want) {
			t.Errorf("%s: reasons = %v, want %v", name, reasons, want)
		}
		for _, r := range reasons {
			if len(r) > 32 {
				t.Errorf("%s: a reason longer than the contract allows: %q", name, r)
			}
		}
	}
}

func TestTheAdvisorIsAskedOnceForAnActionAndTheMemoryIsBounded(t *testing.T) {
	stub := &stubRisk{risk: harnessdecide.RiskCareful, out: harnessdecide.Outcome{Status: harnessdecide.StatusApplied}}
	p := NewAdvisedPolicy(ProjectPolicy(), stub)
	action := act("edit_file", "0b", []string{"a.go"})
	for i := 0; i < 3; i++ {
		if got := p.Decide(context.Background(), harnessrunOwner(), action); got != decisionAskStrict {
			t.Fatalf("decision %d = %v", i, got)
		}
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("the advisor was asked %d times for one action", stub.calls.Load())
	}
	p.Decide(context.Background(), harnessrunOwner(), act("edit_file", "0c", []string{"a.go"}))
	if stub.calls.Load() != 2 {
		t.Fatalf("another action did not ask: %d", stub.calls.Load())
	}
	// More actions than the memory holds forget the oldest, which is then asked about again.
	for i := 0; i < adviceMemory+1; i++ {
		p.Decide(context.Background(), harnessrunOwner(), act("edit_file", fmt.Sprintf("1%03x", i), []string{"a.go"}))
	}
	if len(p.advice) > adviceMemory || len(p.order) > adviceMemory {
		t.Fatalf("remembered %d and %d", len(p.advice), len(p.order))
	}
	before := stub.calls.Load()
	p.Decide(context.Background(), harnessrunOwner(), action)
	if stub.calls.Load() != before+1 {
		t.Fatal("an action that was forgotten was not asked about again")
	}
	if len(p.Reasons(act("edit_file", "ffff", []string{"a.go"}))) != 0 {
		t.Fatal("reasons were invented for an action that was never decided")
	}
}

func TestWithoutAnAdvisorThePolicyIsTheBasePolicy(t *testing.T) {
	base := ProjectPolicy()
	p := NewAdvisedPolicy(base, nil)
	for name, action := range policyActions() {
		if got, want := p.Decide(context.Background(), harnessrunOwner(), action), base.Decide(context.Background(), harnessrunOwner(), action); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
		if got, want := p.Reasons(action), base.Reasons(action); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: reasons %v, want %v", name, got, want)
		}
	}
}

func TestAdvisedDecisionsAreRaceFree(t *testing.T) {
	p := NewAdvisedPolicy(ProjectPolicy(), &stubRisk{risk: harnessdecide.RiskCareful, out: harnessdecide.Outcome{Status: harnessdecide.StatusApplied}})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				a := act("edit_file", fmt.Sprintf("2%03x", (i*50+j)%90), []string{"a.go"})
				p.Decide(context.Background(), harnessrunOwner(), a)
				p.Reasons(a)
			}
		}(i)
	}
	wg.Wait()
}

func TestAnActionIsDescribedToTheAdvisorByNumbersAndFixedFlagsOnly(t *testing.T) {
	for name, tc := range map[string]struct {
		action harness.PreparedAction
		want   harnessdecide.Facts
	}{
		"a command":          {act("run_command", "01", []string{"."}), harnessdecide.Facts{"n": 1}},
		"every flag":         {act("run_command", "02", []string{"."}, "not_cataloged", "project_program", "runs_recipes", "inline_code", "large_change"), harnessdecide.Facts{"n": 1, "np": 1, "pp": 1, "rr": 1, "ic": 1, "lg": 1}},
		"an unknown code":    {act("run_command", "03", []string{"."}, "something_new"), harnessdecide.Facts{"n": 1}},
		"a deletion":         {act("delete_file", "04", []string{"a.go"}), harnessdecide.Facts{"n": 1, "de": 1}},
		"a manifest edit":    {act("edit_file", "05", []string{"a.go", "go.mod"}), harnessdecide.Facts{"n": 2, "cp": 1}},
		"a plain edit":       {act("edit_file", "06", []string{"a.go"}), harnessdecide.Facts{"n": 1}},
		"a command's folder": {act("run_command", "07", []string{"ci"}), harnessdecide.Facts{"n": 1}},
		"no paths":           {act("git_commit", "08", nil), harnessdecide.Facts{"n": 0}},
	} {
		if got := riskFacts(tc.action); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
}

// riskPicker is a decision provider for the service-backed advisor.
type riskPicker struct {
	mu        sync.Mutex
	positions []int
	conf      float64
	outcome   harness.Outcome
	questions []harness.DecisionQuestion
}

func (p *riskPicker) Probe(_ context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: "risk-advice", Scope: scope, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{"decide": harness.AccessAvailable}}, nil
}
func (p *riskPicker) Close() error { return nil }
func (p *riskPicker) Decide(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.questions = append(p.questions, q)
	outcome := p.outcome
	if outcome == "" {
		outcome = harness.OutcomeOK
	}
	var selected []string
	for _, i := range p.positions {
		if i < len(q.Candidates) {
			selected = append(selected, q.Candidates[i])
		}
	}
	return harness.DecisionAnswer{Envelope: q.Envelope, Outcome: outcome, Selected: selected, Confidence: p.conf}, nil
}

func riskService(t *testing.T, p *riskPicker, tweak ...func(*harnessdecide.Config)) ServiceRiskAdvisor {
	t.Helper()
	registry := harness.NewRegistry()
	manifest := harness.Manifest{Version: harness.ContractVersion, ID: "risk-advice", Kind: harness.KindDecision, Capabilities: []harness.CapabilityID{"decide"}}
	if err := registry.Register(manifest, func() (harness.Provider, error) { return p, nil }); err != nil {
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
	return ServiceRiskAdvisor{For: hub.Service}
}

func TestTheDecisionServiceRaisesAnOrdinaryAskAndSeesNoPathOrPreview(t *testing.T) {
	// Labels are routine, careful, hazardous: position 2 is hazardous.
	p := &riskPicker{positions: []int{2}, conf: 0.9}
	policy := NewAdvisedPolicy(ProjectPolicy(), riskService(t, p))
	action := act("run_command", "0d", []string{"secret-dir"}) // a catalogued command: an ordinary ask
	action.Preview = "PRIVATE-PREVIEW rm -rf /secret"
	action.Summary = "PRIVATE-SUMMARY run curl"
	if got := policy.Decide(context.Background(), harnessrunOwner(), action); got != decisionAskStrict {
		t.Fatalf("decision = %v", got)
	}
	if reasons := policy.Reasons(action); !contains(reasons, ReasonJevHazardous) {
		t.Fatalf("reasons = %v", reasons)
	}
	wire := fmt.Sprintf("%+v", p.questions)
	for _, private := range []string{"secret-dir", "PRIVATE", "rm -rf", "curl"} {
		if strings.Contains(wire, private) {
			t.Fatalf("the question carried %q: %s", private, wire)
		}
	}
	if q := p.questions[0]; q.Kind != "command_risk.v1" || q.Evidence[0].Class != "run_command" || q.Evidence[0].Revision != "n=1" {
		t.Fatalf("question = %+v", q)
	}
	// A routine judgment changes nothing.
	calm := NewAdvisedPolicy(ProjectPolicy(), riskService(t, &riskPicker{positions: []int{0}, conf: 0.9}))
	if got := calm.Decide(context.Background(), harnessrunOwner(), action); got != decisionAsk {
		t.Fatalf("a routine judgment decided %v", got)
	}
}

func TestAnAdvisoryServiceThatCannotAnswerChangesNothingAndARequiredOneAddsCaution(t *testing.T) {
	action := act("edit_file", "0e", []string{"a.go"})
	for name, p := range map[string]*riskPicker{
		"a denial":        {positions: []int{2}, conf: 0.9, outcome: harness.OutcomeDenied},
		"low confidence":  {positions: []int{2}, conf: 0.1},
		"an unknown pick": {positions: []int{9}, conf: 0.9},
	} {
		if got := NewAdvisedPolicy(ProjectPolicy(), riskService(t, p)).Decide(context.Background(), harnessrunOwner(), action); got != decisionAsk {
			t.Errorf("advisory, %s: %v", name, got)
		}
		required := NewAdvisedPolicy(ProjectPolicy(), riskService(t, p, func(c *harnessdecide.Config) { c.Required = []harnessdecide.Kind{harnessdecide.KindCommandRisk} }))
		if got := required.Decide(context.Background(), harnessrunOwner(), action); got != decisionAskStrict {
			t.Errorf("required, %s: %v", name, got)
		}
		if reasons := required.Reasons(action); !contains(reasons, ReasonJevCautious) {
			t.Errorf("required, %s: reasons %v", name, reasons)
		}
	}
	// No service, or one that is turned off, is no advice.
	for name, advisor := range map[string]ServiceRiskAdvisor{
		"no source":       {},
		"no service":      {For: func(string) *harnessdecide.Service { return nil }},
		"the kind is off": riskService(t, &riskPicker{positions: []int{2}, conf: 0.9}, func(c *harnessdecide.Config) { c.Enabled = []harnessdecide.Kind{harnessdecide.KindCache} }),
	} {
		if got := NewAdvisedPolicy(ProjectPolicy(), advisor).Decide(context.Background(), harnessrunOwner(), action); got != decisionAsk {
			t.Errorf("%s: %v", name, got)
		}
	}
	// Advice never touches what the base policy denies or allows, even when required.
	hostile := NewAdvisedPolicy(ProjectPolicy(), riskService(t, &riskPicker{positions: []int{0}, conf: 0.99}, func(c *harnessdecide.Config) { c.Required = []harnessdecide.Kind{harnessdecide.KindCommandRisk} }))
	if got := hostile.Decide(context.Background(), harnessrunOwner(), act("mystery_tool", "0f", nil)); got != decisionDeny {
		t.Fatalf("a tool nobody classified: %v", got)
	}
	if got := hostile.Decide(context.Background(), harnessrunOwner(), act("read_file", "10", []string{"a.go"})); got != decisionAllow {
		t.Fatalf("a read: %v", got)
	}
	if got := hostile.Decide(context.Background(), harnessrunOwner(), act("edit_file", "11", []string{"a.go"})); got != decisionAsk {
		t.Fatalf("a routine verdict lowered an ordinary ask: %v", got)
	}
}

func TestAnActionWithoutADigestIsAskedAboutEveryTimeAndNothingIsRemembered(t *testing.T) {
	stub := &stubRisk{risk: harnessdecide.RiskCareful, out: harnessdecide.Outcome{Status: harnessdecide.StatusApplied}}
	p := NewAdvisedPolicy(ProjectPolicy(), stub)
	a := act("edit_file", "ff", []string{"a.go"})
	a.Digest = ""
	for i := 0; i < 2; i++ {
		p.Decide(context.Background(), harnessrunOwner(), a)
	}
	if len(p.advice) != 0 || len(p.order) != 0 {
		t.Fatalf("remembered %d for an action with no digest", len(p.advice))
	}
	if _, ok := p.recall(""); ok || len(p.Reasons(a)) != 0 {
		t.Fatal("advice was recalled for no digest")
	}
	p.remember("sha256:x", ReasonJevCareful)
	p.remember("sha256:x", ReasonJevHazardous)
	if len(p.order) != 1 {
		t.Fatalf("one action was listed %d times", len(p.order))
	}
}

func TestTheAdvisorIsAskedForTheRunTheActionBelongsTo(t *testing.T) {
	var asked []string
	advisor := ServiceRiskAdvisor{For: func(run string) *harnessdecide.Service { asked = append(asked, run); return nil }}
	advisor.Risk(context.Background(), act("edit_file", "ee", []string{"a.go"}))
	if !reflect.DeepEqual(asked, []string{"run-1"}) {
		t.Fatalf("asked for %v", asked)
	}
	if got := riskFacts(act("run_command", "dd", []string{"go.mod"})); got["cp"] != 0 {
		t.Fatalf("a command's folder was treated as a changed control-plane file: %v", got)
	}
}

func TestNoAdviceIsKeptOrFoundUnderAnEmptyDigest(t *testing.T) {
	p := NewAdvisedPolicy(ProjectPolicy(), nil)
	p.remember("", ReasonJevCareful)
	if len(p.advice) != 0 || len(p.order) != 0 {
		t.Fatal("advice was kept under an empty digest")
	}
	p.advice[""] = ReasonJevHazardous
	if reason, ok := p.recall(""); ok || reason != "" {
		t.Fatalf("advice was found under an empty digest: %q", reason)
	}
}
