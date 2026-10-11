package harnesscontext

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

// positional is a decision provider that selects the candidates at fixed positions, which
// is how a provider that only sees aliases has to answer.
type positional struct {
	mu        sync.Mutex
	questions []harness.DecisionQuestion
	positions []int
	conf      float64
	outcome   harness.Outcome
}

func (p *positional) Probe(_ context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: "context-advice", Scope: scope, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{"decide": harness.AccessAvailable}}, nil
}
func (p *positional) Close() error { return nil }
func (p *positional) Decide(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
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

func decisionService(t *testing.T, p *positional, tweak ...func(*harnessdecide.Config)) *harnessdecide.Service {
	t.Helper()
	registry := harness.NewRegistry()
	manifest := harness.Manifest{Version: harness.ContractVersion, ID: "context-advice", Kind: harness.KindDecision, Capabilities: []harness.CapabilityID{"decide"}}
	if err := registry.Register(manifest, func() (harness.Provider, error) { return p, nil }); err != nil {
		t.Fatal(err)
	}
	session, err := registry.OpenSession(context.Background(), "context-advice", harness.Scope{Workspace: ws, Run: "run-x", Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	cfg := harnessdecide.Config{Asker: session, Capability: "decide", Enabled: harnessdecide.Kinds()}
	for _, fn := range tweak {
		fn(&cfg)
	}
	s, err := harnessdecide.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAServiceAdvisorPromotesThroughAliasesAndTheAssemblerStillOnlyNudges(t *testing.T) {
	chunks := adviceFixture()
	for i := range chunks {
		chunks[i].Text = "PRIVATE-BODY-" + chunks[i].Text
		chunks[i].Title, chunks[i].Ref = "PRIVATE-TITLE", "/private/path/file.go"
	}
	plain := mustAssemble(t, fixedAssembler(Config{}), adviceRequest(), chunks, nil)
	// The eighth candidate is offered last, so its alias is c7.
	p := &positional{positions: []int{7}, conf: 0.9}
	advised := mustAssemble(t, fixedAssembler(Config{}), adviceRequest(), chunks, ServiceAdvisor{Service: decisionService(t, p)})
	if advised.Report.Advice != AdviceApplied || !reflect.DeepEqual(advised.Report.AdviceApplied, []string{"m07"}) {
		t.Fatalf("advice = %s %v", advised.Report.Advice, advised.Report.AdviceApplied)
	}
	if reflect.DeepEqual(ids(plain.Items), ids(advised.Items)) {
		t.Fatalf("the advice changed nothing: %v", ids(advised.Items))
	}
	q := p.questions[0]
	if q.Kind != "visibility.v1" || !reflect.DeepEqual(q.Candidates, []string{"c0", "c1", "c2", "c3", "c4", "c5", "c6", "c7"}) {
		t.Fatalf("question = %+v", q)
	}
	for i, ref := range q.Evidence[:8] {
		if ref.ID != q.Candidates[i] || ref.Class != "memory" || !strings.Contains(ref.Revision, "r=") || !strings.Contains(ref.Revision, "t=") || ref.Note != "" {
			t.Fatalf("evidence %d = %+v", i, ref)
		}
	}
	wire := fmt.Sprintf("%+v", q)
	for _, private := range []string{"PRIVATE-BODY", "PRIVATE-TITLE", "/private/path", "m00", "m07", "evidence evidence"} {
		if strings.Contains(wire, private) {
			t.Fatalf("the decision question carried %q: %s", private, wire)
		}
	}
}

func TestEveryWayTheServiceCanDeclineLeavesTheAssemblyUnchanged(t *testing.T) {
	chunks := adviceFixture()
	baseline := Render(mustAssemble(t, fixedAssembler(Config{}), adviceRequest(), chunks, nil))
	for name, p := range map[string]*positional{
		"denied":            {positions: []int{7}, conf: 0.9, outcome: harness.OutcomeDenied},
		"unavailable":       {positions: []int{7}, conf: 0.9, outcome: harness.OutcomeUnavailable},
		"failed":            {positions: []int{7}, conf: 0.9, outcome: harness.OutcomeFailed},
		"low confidence":    {positions: []int{7}, conf: 0.1},
		"more than allowed": {positions: []int{0, 1, 2, 3, 4, 5, 6, 7}, conf: 0.9},
	} {
		result := mustAssemble(t, fixedAssembler(Config{}), adviceRequest(), chunks, ServiceAdvisor{Service: decisionService(t, p)})
		if result.Report.Advice == AdviceApplied || Render(result) != baseline {
			t.Errorf("%s: advice %s changed the assembly", name, result.Report.Advice)
		}
	}
	for name, advisor := range map[string]Advisor{
		"no service":      ServiceAdvisor{},
		"the kind is off": ServiceAdvisor{Service: decisionService(t, &positional{positions: []int{7}, conf: 0.9}, func(c *harnessdecide.Config) { c.Enabled = []harnessdecide.Kind{harnessdecide.KindCache} })},
	} {
		result := mustAssemble(t, fixedAssembler(Config{}), adviceRequest(), chunks, advisor)
		if result.Report.Advice != AdviceFailed || Render(result) != baseline {
			t.Errorf("%s: advice %s", name, result.Report.Advice)
		}
	}
}

func TestChunksAboveTheAdvisorsClassAreNeverOfferedThroughTheService(t *testing.T) {
	chunks := adviceFixture()
	for i := range chunks {
		chunks[i].Sensitivity = SensitivitySensitive // above the default advisor class
	}
	req := adviceRequest()
	req.Eligibility.MaxSensitivity = SensitivityRestricted
	p := &positional{positions: []int{0}, conf: 0.9}
	result := mustAssemble(t, fixedAssembler(Config{}), req, chunks, ServiceAdvisor{Service: decisionService(t, p)})
	if len(p.questions) != 0 || result.Report.Advice != AdviceNoEligible {
		t.Fatalf("%d questions, advice %s", len(p.questions), result.Report.Advice)
	}
}

func TestPercentIsAWholeNumberInRangeWhateverTheRelevance(t *testing.T) {
	for in, want := range map[float64]int{0: 0, 0.5: 50, 0.999: 99, 1: 100, 7: 100, -3: 0} {
		if got := percent(in); got != want {
			t.Errorf("percent(%v) = %d, want %d", in, got, want)
		}
	}
	var nan float64
	nan = nan / nan
	if percent(nan) != 0 {
		t.Error("percent(NaN) is not zero")
	}
}

func TestARunSourceAsksForTheAdvisorOfTheRunItIsAssemblingFor(t *testing.T) {
	var asked []string
	source := &RunSource{
		Assembler: fixedAssembler(Config{}), Eligibility: defaultRequest.Eligibility, Budget: adviceRequest().Budget,
		Gather: func(context.Context, harnessrun.Owner) ([]Chunk, error) { return adviceFixture(), nil },
		AdvisorFor: func(runID string) Advisor {
			asked = append(asked, runID)
			return &fakeAdvisor{ids: []string{"m07"}, confidence: 0.9}
		},
		Advisor: func() Advisor { t.Error("the shared advisor was used although a per-run one was given"); return nil },
	}
	owner := harnessrun.Owner{ClientID: "claude-desktop", Workspace: ws, GrantID: strings.Repeat("b", 32), GrantRevision: 1}
	if _, err := source.Context(context.Background(), harnessrun.Snapshot{RunID: "run-77", Owner: owner, Turn: 1}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(asked, []string{"run-77"}) {
		t.Fatalf("asked for %v", asked)
	}
	// Without a per-run advisor the shared one is used as before.
	used := 0
	shared := &RunSource{Assembler: fixedAssembler(Config{}), Eligibility: defaultRequest.Eligibility, Budget: adviceRequest().Budget,
		Gather:  func(context.Context, harnessrun.Owner) ([]Chunk, error) { return adviceFixture(), nil },
		Advisor: func() Advisor { used++; return nil }}
	if _, err := shared.Context(context.Background(), harnessrun.Snapshot{RunID: "run-78", Owner: owner, Turn: 1}); err != nil || used != 1 {
		t.Fatalf("%v %d", err, used)
	}
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// shifting returns the same chunks with their relevance rearranged, as it would be on a
// later turn, with enough text that only a few of them fit.
func shifting(shift int) []Chunk {
	var chunks []Chunk
	for i := 0; i < 8; i++ {
		chunks = append(chunks, chunk(fmt.Sprintf("m%02d", i), 0.60-float64((i+shift)%8)*0.02, strings.Repeat("evidence ", 350)))
	}
	return chunks
}

func TestAStableOrderChangesOnlyTheArrangementOfWhatWasSelected(t *testing.T) {
	req := adviceRequest()
	chunks := shifting(0)
	chunks[0].Pinned, chunks[0].Source, chunks[0].Trust, chunks[0].Sensitivity = true, SourceInstruction, TrustProject, SensitivityPublic
	relevance := mustAssemble(t, fixedAssembler(Config{}), req, chunks, nil)
	req.Order = OrderStable
	stable := mustAssemble(t, fixedAssembler(Config{}), req, chunks, nil)
	if stable.Report.Order != "stable" || relevance.Report.Order != "relevance" {
		t.Fatalf("orders reported as %q and %q", stable.Report.Order, relevance.Report.Order)
	}
	same := func(a, b Assembled) bool {
		x, y := ids(a.Items), ids(b.Items)
		sort.Strings(x)
		sort.Strings(y)
		return reflect.DeepEqual(x, y)
	}
	if !same(relevance, stable) {
		t.Fatalf("the selection changed: %v vs %v", ids(relevance.Items), ids(stable.Items))
	}
	levels := func(a Assembled) map[string]Visibility {
		out := map[string]Visibility{}
		for _, it := range a.Items {
			out[it.ID] = it.Visibility
		}
		return out
	}
	if !reflect.DeepEqual(levels(relevance), levels(stable)) {
		t.Fatalf("visibility changed: %v vs %v", levels(relevance), levels(stable))
	}
	if stable.Items[0].ID != "m00" || !stable.Items[0].Pinned {
		t.Fatalf("the pinned instruction is not first: %v", ids(stable.Items))
	}
	evidence := ids(stable.Items[1:])
	if !sort.StringsAreSorted(evidence) {
		t.Fatalf("the evidence is not in identifier order: %v", evidence)
	}
	if stable.Report.TokensUsed != relevance.Report.TokensUsed || stable.PrefixID != relevance.PrefixID {
		t.Fatalf("tokens %d vs %d, prefix %s vs %s", stable.Report.TokensUsed, relevance.Report.TokensUsed, stable.PrefixID, relevance.PrefixID)
	}
}

func TestAStableOrderKeepsMoreOfThePromptTheSameFromTurnToTurn(t *testing.T) {
	prompt := func(order Order, shift int) string {
		req := adviceRequest()
		req.Order = order
		return Render(mustAssemble(t, fixedAssembler(Config{}), req, shifting(shift), nil))
	}
	// The same chunks are selected on both turns; only their relevance moved.
	stable := commonPrefix(prompt(OrderStable, 0), prompt(OrderStable, 3))
	relevance := commonPrefix(prompt(OrderRelevance, 0), prompt(OrderRelevance, 3))
	if stable <= relevance {
		t.Fatalf("a stable order shares %d bytes between turns, a relevance order %d", stable, relevance)
	}
}

func TestAnUnknownOrderIsRefused(t *testing.T) {
	req := adviceRequest()
	req.Order = Order(9)
	if _, err := fixedAssembler(Config{}).Assemble(context.Background(), req, adviceFixture(), nil); err == nil {
		t.Fatal("an unknown order was accepted")
	}
	if OrderRelevance.String() != "relevance" || OrderStable.String() != "stable" || !OrderStable.valid() || Order(-1).valid() {
		t.Fatal("order names or validity are wrong")
	}
}

func TestTheCacheAdvisorAsksOnlyAboutMeasuredNumbersAndMapsTheStrategyToAnOrder(t *testing.T) {
	measured := func(string) CacheStats {
		return CacheStats{Measured: true, HitPercent: 140, Turns: 9, PrefixTokens: 4000}
	}
	// Position 0 is "stable", position 1 is "relevance".
	stable := &positional{positions: []int{0}, conf: 0.9}
	got, err := ServiceCacheAdvisor{Service: decisionService(t, stable), Stats: measured}.Order(context.Background(), "run-1")
	if err != nil || got != OrderStable || len(stable.questions) != 1 {
		t.Fatalf("%v %v %d", got, err, len(stable.questions))
	}
	if q := stable.questions[0]; q.Kind != "cache.v1" || !reflect.DeepEqual(q.Candidates, []string{"stable", "relevance"}) || q.Evidence[0].Revision != "h=100;n=9;p=4000" {
		t.Fatalf("question = %+v", q)
	}
	relevance := &positional{positions: []int{1}, conf: 0.9}
	if got, err := (ServiceCacheAdvisor{Service: decisionService(t, relevance), Stats: measured}).Order(context.Background(), "run-1"); err != nil || got != OrderRelevance {
		t.Fatalf("%v %v", got, err)
	}
	// Nothing measured, nothing asked.
	idle := &positional{positions: []int{0}, conf: 0.9}
	for name, stats := range map[string]func(string) CacheStats{
		"unmeasured":    func(string) CacheStats { return CacheStats{} },
		"too few turns": func(string) CacheStats { return CacheStats{Measured: true, HitPercent: 50, Turns: 2} },
	} {
		if got, err := (ServiceCacheAdvisor{Service: decisionService(t, idle), Stats: stats}).Order(context.Background(), "run-1"); err == nil || got != OrderRelevance {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
	if len(idle.questions) != 0 {
		t.Fatalf("%d questions were asked about numbers nobody had measured", len(idle.questions))
	}
	for name, advisor := range map[string]ServiceCacheAdvisor{
		"no service": {Stats: measured},
		"no stats":   {Service: decisionService(t, idle)},
		"denied":     {Service: decisionService(t, &positional{positions: []int{0}, conf: 0.9, outcome: harness.OutcomeDenied}), Stats: measured},
		"unsure":     {Service: decisionService(t, &positional{positions: []int{0}, conf: 0.1}), Stats: measured},
	} {
		if got, err := advisor.Order(context.Background(), "run-1"); err == nil || got != OrderRelevance {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
}

type orderAdvisor struct {
	order Order
	err   error
	panic bool
	delay time.Duration
}

func (a orderAdvisor) Order(ctx context.Context, _ string) (Order, error) {
	if a.panic {
		panic("advisor bug")
	}
	if a.delay > 0 {
		select {
		case <-time.After(a.delay):
		case <-ctx.Done():
		}
	}
	return a.order, a.err
}

func TestARunSourceUsesValidOrderingAdviceAndOtherwiseTheRelevanceOrder(t *testing.T) {
	owner := harnessrun.Owner{ClientID: "claude-desktop", Workspace: ws, GrantID: strings.Repeat("b", 32), GrantRevision: 1}
	source := func(advisor CacheAdvisor, nilAdvisor bool) *RunSource {
		s := &RunSource{Assembler: fixedAssembler(Config{}), Eligibility: defaultRequest.Eligibility, Budget: adviceRequest().Budget,
			Gather: func(context.Context, harnessrun.Owner) ([]Chunk, error) { return shifting(3), nil }}
		if advisor != nil || nilAdvisor {
			s.CacheFor = func(string) CacheAdvisor { return advisor }
		}
		return s
	}
	prompt := func(s *RunSource) string {
		pc, err := s.Context(context.Background(), harnessrun.Snapshot{RunID: "run-1", Owner: owner, Turn: 1})
		if err != nil {
			t.Fatal(err)
		}
		return pc.Prompt
	}
	relevance := prompt(source(nil, false))
	stable := prompt(source(orderAdvisor{order: OrderStable}, false))
	if stable == relevance {
		t.Fatal("valid advice did not change the order")
	}
	for name, s := range map[string]*RunSource{
		"an error":           source(orderAdvisor{order: OrderStable, err: errors.New("boom")}, false),
		"an unknown order":   source(orderAdvisor{order: Order(7)}, false),
		"a panic":            source(orderAdvisor{panic: true}, false),
		"no advisor for run": source(nil, true),
	} {
		if got := prompt(s); got != relevance {
			t.Errorf("%s changed the prompt", name)
		}
	}
	begin := time.Now()
	if got := prompt(source(orderAdvisor{order: OrderStable, delay: 10 * time.Second}, false)); got != relevance || time.Since(begin) > 5*time.Second {
		t.Errorf("a slow advisor changed the prompt or held the turn for %v", time.Since(begin))
	}
}

func TestTheVisibilityAdvisorNeedsAServiceAndClampsWhatItDescribes(t *testing.T) {
	if _, _, err := (ServiceAdvisor{}).Promote(context.Background(), []Candidate{{ID: "a"}, {ID: "b"}}); err == nil {
		t.Fatal("no service gave advice")
	}
	p := &positional{positions: []int{0}, conf: 0.9}
	_, _, err := ServiceAdvisor{Service: decisionService(t, p)}.Promote(context.Background(), []Candidate{
		{ID: "a", Source: SourceMemory, Tokens: -50, Relevance: 0.5}, {ID: "b", Source: SourceMemory, Tokens: 10, Relevance: 2}})
	if err != nil || len(p.questions) != 1 {
		t.Fatalf("%v %d", err, len(p.questions))
	}
	if ev := p.questions[0].Evidence; ev[0].Revision != "r=50;t=0" || ev[1].Revision != "r=100;t=10" {
		t.Fatalf("evidence = %+v", ev)
	}
}

func TestAStableOrderLeavesTheOrderOfPinnedInstructionsAlone(t *testing.T) {
	first := chunk("z-rule", 0.5, "rule one")
	first.Pinned, first.Source, first.Trust, first.Sensitivity = true, SourceInstruction, TrustProject, SensitivityPublic
	second := chunk("a-note", 0.5, "rule two")
	second.Pinned, second.Trust, second.Sensitivity = true, TrustProject, SensitivityPublic
	req := adviceRequest()
	req.Order = OrderStable
	got := ids(mustAssemble(t, fixedAssembler(Config{}), req, []Chunk{second, first, chunk("m-evidence", 0.4, "x")}, nil).Items)
	if got[0] != "z-rule" || got[1] != "a-note" {
		t.Fatalf("pinned instructions were reordered: %v", got)
	}
}
