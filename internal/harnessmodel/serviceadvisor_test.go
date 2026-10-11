package harnessmodel

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
)

// picker is a decision provider that selects fixed candidates with a fixed confidence.
type picker struct {
	mu        sync.Mutex
	questions []harness.DecisionQuestion
	selected  []string
	conf      float64
	outcome   harness.Outcome
}

func (p *picker) Probe(_ context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	return harness.LiveAccess{Version: harness.ContractVersion, Provider: "model-advice", Scope: scope, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{"decide": harness.AccessAvailable}}, nil
}
func (p *picker) Close() error { return nil }
func (p *picker) Decide(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.questions = append(p.questions, q)
	outcome := p.outcome
	if outcome == "" {
		outcome = harness.OutcomeOK
	}
	return harness.DecisionAnswer{Envelope: q.Envelope, Outcome: outcome, Selected: p.selected, Confidence: p.conf}, nil
}

func decisionService(t *testing.T, p *picker, tweak ...func(*harnessdecide.Config)) *harnessdecide.Service {
	t.Helper()
	registry := harness.NewRegistry()
	manifest := harness.Manifest{Version: harness.ContractVersion, ID: "model-advice", Kind: harness.KindDecision, Capabilities: []harness.CapabilityID{"decide"}}
	if err := registry.Register(manifest, func() (harness.Provider, error) { return p, nil }); err != nil {
		t.Fatal(err)
	}
	session, err := registry.OpenSession(context.Background(), "model-advice", harness.Scope{Workspace: "ws", Run: "run-x", Generation: 1})
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

func TestAServiceAdvisorReordersTheChainLikeAnyOtherAdvisor(t *testing.T) {
	p := &picker{selected: []string{"c-pricey"}, conf: 0.9}
	r := newRouter(t, &fakeProber{}, NewMeter(nil), ServiceAdvisor{Service: decisionService(t, p)}, adviceProfiles()...)
	chain, report, err := r.Plan(context.Background(), need)
	if err != nil || report.Advice != AdviceApplied || !reflect.DeepEqual(ids(chain), []string{"c-pricey", "a-cheap", "b-mid"}) || !chain[0].Preferred {
		t.Fatalf("chain=%v report=%+v err=%v", ids(chain), report, err)
	}
	// The question carried only the eligible providers' identifiers, under the model kind.
	q := p.questions[0]
	if q.Kind != "model.v1" || !reflect.DeepEqual(q.Candidates, []string{"a-cheap", "b-mid", "c-pricey"}) {
		t.Fatalf("question = %+v", q)
	}
	if strings.Contains(strings.Join(q.Candidates, ","), "d-public") {
		t.Fatal("a class-excluded provider was offered")
	}
}

func TestEveryWayTheServiceCanDeclineLeavesTheDeterministicChain(t *testing.T) {
	baseline, _, _ := newRouter(t, &fakeProber{}, NewMeter(nil), nil, adviceProfiles()...).Plan(context.Background(), need)
	for name, tc := range map[string]struct {
		advisor Advisor
		picker  *picker
	}{
		"a denied provider":      {nil, &picker{selected: []string{"c-pricey"}, conf: 0.9, outcome: harness.OutcomeDenied}},
		"an unavailable one":     {nil, &picker{selected: []string{"c-pricey"}, conf: 0.9, outcome: harness.OutcomeUnavailable}},
		"a failed one":           {nil, &picker{selected: []string{"c-pricey"}, conf: 0.9, outcome: harness.OutcomeFailed}},
		"low confidence":         {nil, &picker{selected: []string{"c-pricey"}, conf: 0.2}},
		"a provider not offered": {nil, &picker{selected: []string{"d-public"}, conf: 0.9}},
		"two providers":          {nil, &picker{selected: []string{"c-pricey", "a-cheap"}, conf: 0.9}},
		"none":                   {nil, &picker{selected: nil, conf: 0.9}},
	} {
		advisor := ServiceAdvisor{Service: decisionService(t, tc.picker)}
		r := newRouter(t, &fakeProber{}, NewMeter(nil), advisor, adviceProfiles()...)
		chain, report, err := r.Plan(context.Background(), need)
		if err != nil || report.Advice == AdviceApplied || !reflect.DeepEqual(ids(chain), ids(baseline)) {
			t.Errorf("%s: chain=%v advice=%s err=%v", name, ids(chain), report.Advice, err)
		}
	}
	// No service, or the model kind not enabled, is the same.
	for name, advisor := range map[string]Advisor{
		"no service":      ServiceAdvisor{},
		"the kind is off": ServiceAdvisor{Service: decisionService(t, &picker{selected: []string{"c-pricey"}, conf: 0.9}, func(c *harnessdecide.Config) { c.Enabled = []harnessdecide.Kind{harnessdecide.KindCache} })},
	} {
		r := newRouter(t, &fakeProber{}, NewMeter(nil), advisor, adviceProfiles()...)
		chain, report, err := r.Plan(context.Background(), need)
		if err != nil || report.Advice != AdviceFailed || !reflect.DeepEqual(ids(chain), ids(baseline)) {
			t.Errorf("%s: chain=%v advice=%s err=%v", name, ids(chain), report.Advice, err)
		}
	}
}

func TestAnAdvisorBehindTheServiceStillCannotWidenTheEligibleSet(t *testing.T) {
	// Whatever the provider selects, the chain only ever contains eligible providers.
	for _, choice := range []string{"a-cheap", "b-mid", "c-pricey", "d-public", "ghost"} {
		p := &picker{selected: []string{choice}, conf: 0.99}
		r := newRouter(t, &fakeProber{}, NewMeter(nil), ServiceAdvisor{Service: decisionService(t, p)}, adviceProfiles()...)
		chain, _, err := r.Plan(context.Background(), need)
		if err != nil || len(chain) != 3 || contains(ids(chain), "d-public") || contains(ids(chain), "ghost") {
			t.Errorf("choice %q: chain=%v err=%v", choice, ids(chain), err)
		}
	}
}

func TestTheAdvisorReturnsTheConfidenceTheProviderGaveAndErrorsOtherwise(t *testing.T) {
	p := &picker{selected: []string{"b-mid"}, conf: 0.83}
	id, conf, err := ServiceAdvisor{Service: decisionService(t, p)}.Prefer(context.Background(), []harness.ProviderID{"a-cheap", "b-mid"})
	if err != nil || id != "b-mid" || conf != 0.83 {
		t.Fatalf("%q %v %v", id, conf, err)
	}
	if _, _, err := (ServiceAdvisor{}).Prefer(context.Background(), []harness.ProviderID{"a-cheap", "b-mid"}); err == nil {
		t.Fatal("no service gave advice")
	}
	unsure := &picker{selected: []string{"b-mid"}, conf: 0.1}
	if id, _, err := (ServiceAdvisor{Service: decisionService(t, unsure)}).Prefer(context.Background(), []harness.ProviderID{"a-cheap", "b-mid"}); err == nil || id != "" {
		t.Fatalf("low confidence was passed on: %q %v", id, err)
	}
}
