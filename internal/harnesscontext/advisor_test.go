package harnesscontext

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// capturingDecision is a decision provider that records the question it was asked and
// answers with a scripted selection.
type capturingDecision struct {
	mu       sync.Mutex
	question harness.DecisionQuestion
	selected []string
	outcome  harness.Outcome
	conf     float64
}

func (c *capturingDecision) Probe(_ context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: "advisor", Scope: scope, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{"visibility": harness.AccessAvailable}}, nil
}
func (c *capturingDecision) Close() error { return nil }
func (c *capturingDecision) Decide(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.question = q
	outcome := c.outcome
	if outcome == "" {
		outcome = harness.OutcomeOK
	}
	return harness.DecisionAnswer{Envelope: q.Envelope, Outcome: outcome, Selected: c.selected, Confidence: c.conf}, nil
}

func decisionSession(t *testing.T, provider *capturingDecision) *harness.Session {
	t.Helper()
	registry := harness.NewRegistry()
	manifest := harness.Manifest{Version: harness.ContractVersion, ID: "advisor", Kind: harness.KindDecision, Capabilities: []harness.CapabilityID{"visibility"}}
	if err := registry.Register(manifest, func() (harness.Provider, error) { return provider, nil }); err != nil {
		t.Fatal(err)
	}
	session, err := registry.OpenSession(context.Background(), "advisor", harness.Scope{Workspace: ws, Run: "run-x", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestAJevDecisionSessionIsAskedOnlyForOpaqueMetadata(t *testing.T) {
	var chunks []Chunk
	for i := 0; i < 8; i++ {
		c := chunk(fmt.Sprintf("m%02d", i), 0.60-float64(i)*0.01, strings.Repeat("evidence ", 350))
		c.Text = "PRIVATE-BODY-" + c.Text
		c.Title, c.Ref = "PRIVATE-TITLE", "/private/path/file.go"
		chunks = append(chunks, c)
	}
	provider := &capturingDecision{selected: []string{"m07"}, conf: 0.9}
	advisor := SessionAdvisor{Session: decisionSession(t, provider), Capability: "visibility"}
	result := mustAssemble(t, fixedAssembler(Config{}), adviceRequest(), chunks, advisor)
	if result.Report.Advice != AdviceApplied {
		t.Fatalf("advice = %s", result.Report.Advice)
	}
	q := provider.question
	if q.Kind != "visibility" || len(q.Candidates) != 8 || len(q.Evidence) != 8 {
		t.Fatalf("question = %+v", q)
	}
	for i, id := range q.Candidates {
		ref := q.Evidence[i]
		if ref.ID != id || ref.Class != string(SourceMemory) || !strings.HasPrefix(ref.Revision, "t") {
			t.Fatalf("evidence %d = %+v", i, ref)
		}
	}
	wire := fmt.Sprintf("%+v", q)
	for _, private := range []string{"PRIVATE-BODY", "PRIVATE-TITLE", "/private/path", "evidence evidence"} {
		if strings.Contains(wire, private) {
			t.Fatalf("the decision question carried %q: %s", private, wire)
		}
	}
}

func TestAJevDecisionThatIsNotOKOrIsMalformedFallsBackCleanly(t *testing.T) {
	chunks := adviceFixture()
	baseline := Render(mustAssemble(t, fixedAssembler(Config{}), adviceRequest(), chunks, nil))
	for name, provider := range map[string]*capturingDecision{
		"denied":          {selected: []string{"m07"}, conf: 0.9, outcome: harness.OutcomeDenied},
		"failed":          {selected: []string{"m07"}, conf: 0.9, outcome: harness.OutcomeFailed},
		"partial":         {selected: []string{"m07"}, conf: 0.9, outcome: harness.OutcomePartial},
		"outside the set": {selected: []string{"ghost"}, conf: 0.9},
	} {
		advisor := SessionAdvisor{Session: decisionSession(t, provider), Capability: "visibility"}
		result := mustAssemble(t, fixedAssembler(Config{}), adviceRequest(), chunks, advisor)
		if result.Report.Advice == AdviceApplied || Render(result) != baseline {
			t.Errorf("%s: advice %s changed the assembly", name, result.Report.Advice)
		}
	}
	closed := decisionSession(t, &capturingDecision{selected: []string{"m07"}, conf: 0.9})
	_ = closed.Close()
	result := mustAssemble(t, fixedAssembler(Config{}), adviceRequest(), chunks, SessionAdvisor{Session: closed, Capability: "visibility"})
	if result.Report.Advice != AdviceFailed || Render(result) != baseline {
		t.Fatalf("a closed session = %s", result.Report.Advice)
	}
	none := mustAssemble(t, fixedAssembler(Config{}), adviceRequest(), chunks, SessionAdvisor{})
	if none.Report.Advice != AdviceFailed || Render(none) != baseline {
		t.Fatalf("no session = %s", none.Report.Advice)
	}
}
