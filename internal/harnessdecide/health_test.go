package harnessdecide

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

func TestABreakerOpensAfterRepeatedFailuresAndProbesOnceWithGrowingCooldowns(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var b breaker
	for i := 0; i < failureThreshold-1; i++ {
		b.failure(start)
		if ok, _ := b.allow(start); !ok {
			t.Fatalf("opened after %d failures", i+1)
		}
	}
	b.failure(start)
	if ok, _ := b.allow(start); ok {
		t.Fatal("did not open at the threshold")
	}
	if ok, _ := b.allow(start.Add(baseCooldown - time.Nanosecond)); ok {
		t.Fatal("allowed before the cooldown ended")
	}
	at := start.Add(baseCooldown)
	ok, probe := b.allow(at)
	if !ok || !probe {
		t.Fatalf("no probe after the cooldown: %v %v", ok, probe)
	}
	if again, _ := b.allow(at); again {
		t.Fatal("a second request went through while the probe was out")
	}
	b.failure(at) // the probe failed: the cooldown doubles
	if ok, _ := b.allow(at.Add(2*baseCooldown - time.Nanosecond)); ok {
		t.Fatal("allowed before the doubled cooldown ended")
	}
	if ok, probe := b.allow(at.Add(2 * baseCooldown)); !ok || !probe {
		t.Fatal("no probe after the doubled cooldown")
	}
	// The cooldown is capped.
	now := at.Add(2 * baseCooldown)
	for i := 0; i < 20; i++ {
		b.failure(now)
		now = now.Add(maxCooldown)
		if ok, probe := b.allow(now); !ok || !probe {
			t.Fatalf("round %d: no probe at the capped cooldown", i)
		}
	}
	if b.cooldown != maxCooldown {
		t.Fatalf("cooldown = %v", b.cooldown)
	}
	b.success()
	if ok, probe := b.allow(now); !ok || probe || b.open || b.failures != 0 {
		t.Fatalf("a success did not close the breaker: %+v", b)
	}
}

func TestAReleasedProbeLetsTheNextRequestTryAndAFailureCountIsNotInheritedAfterASuccess(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var b breaker
	for i := 0; i < failureThreshold; i++ {
		b.failure(start)
	}
	at := start.Add(baseCooldown)
	if ok, probe := b.allow(at); !ok || !probe {
		t.Fatal("no probe")
	}
	b.release()
	if ok, probe := b.allow(at); !ok || !probe {
		t.Fatal("a released probe was not offered again")
	}
	// Failures must be consecutive to count.
	var c breaker
	c.failure(start)
	c.failure(start)
	c.success()
	c.failure(start)
	c.failure(start)
	if ok, _ := c.allow(start); !ok {
		t.Fatal("non-consecutive failures opened the breaker")
	}
}

func failing(err error) func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	return func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error) {
		return harness.DecisionAnswer{}, err
	}
}

func TestRepeatedProviderFaultsPauseEveryKindAndAProbeRestoresThem(t *testing.T) {
	f := &fakeAsker{script: failing(errors.New("down"))}
	s, clk := service(t, f)
	ctx := context.Background()
	for i := 0; i < failureThreshold; i++ {
		if _, out := s.Model(ctx, providers("a-model", "b-model")); out.Status != StatusFailed {
			t.Fatalf("failure %d: %+v", i, out)
		}
	}
	asked := len(f.asked())
	for _, p := range probes() {
		if got, out := p.run(s); got != "" || out.Status != StatusPaused {
			t.Errorf("%s while paused: %q %+v", p.kind, got, out)
		}
	}
	if len(f.asked()) != asked {
		t.Fatal("a paused provider was asked")
	}
	snap := s.Health().Snapshot()
	if snap.ProviderPaused.IsZero() || !snap.ProviderPaused.Equal(clk.now().Add(baseCooldown)) {
		t.Fatalf("snapshot = %+v", snap)
	}
	// After the cooldown exactly one probe goes; if it succeeds everything resumes.
	clk.advance(baseCooldown)
	f.script = picks(0.9, 0)
	if _, out := s.Model(ctx, providers("a-model", "b-model")); out.Status != StatusApplied {
		t.Fatalf("the probe: %+v", out)
	}
	for _, p := range probes() {
		if _, out := p.run(s); out.Status != StatusApplied {
			t.Errorf("%s after recovery: %+v", p.kind, out)
		}
	}
	if !s.Health().Snapshot().ProviderPaused.IsZero() {
		t.Fatal("still reported paused")
	}
}

func TestAFailedProbeDoublesThePauseAndOnlyOneProbeIsOutAtATime(t *testing.T) {
	gate := make(chan struct{})
	var mu sync.Mutex
	mode := "fail"
	f := &fakeAsker{script: func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
		mu.Lock()
		m := mode
		mu.Unlock()
		if m == "hold" {
			<-gate
			return answer(q, 0.9, 0), nil
		}
		return harness.DecisionAnswer{}, errors.New("down")
	}}
	s, clk := service(t, f, func(c *Config) { c.RunBudget = 100; c.PerMinute = 100 })
	ctx := context.Background()
	for i := 0; i < failureThreshold; i++ {
		s.Model(ctx, providers("a-model", "b-model"))
	}
	clk.advance(baseCooldown)
	if _, out := s.Model(ctx, providers("a-model", "b-model")); out.Status != StatusFailed {
		t.Fatalf("the failed probe: %+v", out)
	}
	if _, out := s.Model(ctx, providers("a-model", "b-model")); out.Status != StatusPaused {
		t.Fatalf("right after a failed probe: %+v", out)
	}
	clk.advance(2*baseCooldown - time.Second)
	if _, out := s.Model(ctx, providers("a-model", "b-model")); out.Status != StatusPaused {
		t.Fatalf("before the doubled pause ended: %+v", out)
	}
	clk.advance(time.Second)
	mu.Lock()
	mode = "hold"
	mu.Unlock()
	probeDone := make(chan Outcome, 1)
	go func() { _, out := s.Model(ctx, providers("a-model", "b-model")); probeDone <- out }()
	for len(f.asked()) < failureThreshold+2 { // the probe has reached the provider
		time.Sleep(time.Millisecond)
	}
	if _, out := s.Model(ctx, providers("a-model", "b-model")); out.Status != StatusPaused {
		t.Fatalf("a second request while the probe was out: %+v", out)
	}
	close(gate)
	if out := <-probeDone; out.Status != StatusApplied {
		t.Fatalf("the probe: %+v", out)
	}
	if _, out := s.Model(ctx, providers("a-model", "b-model")); out.Status != StatusApplied {
		t.Fatalf("after the probe succeeded: %+v", out)
	}
}

func TestMalformedAnswersPauseOnlyTheKindThatGaveThem(t *testing.T) {
	f := &fakeAsker{script: func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
		a := answer(q, 0.9, 0)
		if q.Kind == string(KindVisibility) {
			a.Selected = []string{"not-offered"}
		}
		return a, nil
	}}
	s, clk := service(t, f)
	ctx := context.Background()
	for i := 0; i < failureThreshold; i++ {
		if _, out := s.Visibility(ctx, items("v-", 4)); out.Status != StatusInvalid {
			t.Fatalf("answer %d: %+v", i, out)
		}
	}
	before := len(f.asked())
	if _, out := s.Visibility(ctx, items("v-", 4)); out.Status != StatusPaused || len(f.asked()) != before {
		t.Fatalf("visibility after repeated bad answers: %+v", out)
	}
	if got, out := s.Model(ctx, providers("a-model", "b-model")); got == "" || out.Status != StatusApplied {
		t.Fatalf("another kind was paused too: %q %+v", got, out)
	}
	snap := s.Health().Snapshot()
	if !snap.ProviderPaused.IsZero() || !reflect.DeepEqual(snap.PausedKindList(), []Kind{KindVisibility}) {
		t.Fatalf("snapshot = %+v", snap)
	}
	clk.advance(baseCooldown)
	f.script = picks(0.9, 0)
	if _, out := s.Visibility(ctx, items("v-", 4)); out.Status != StatusApplied {
		t.Fatalf("the probe: %+v", out)
	}
	if _, out := s.Visibility(ctx, items("v-", 4)); out.Status != StatusApplied {
		t.Fatalf("the request after the probe: %+v", out)
	}
	if len(s.Health().Snapshot().PausedKinds) != 0 {
		t.Fatal("still paused after a good answer")
	}
}

func TestOnlyFaultsCountAgainstAProviderAndUnhelpfulAnswersDoNot(t *testing.T) {
	cases := map[string]func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error){
		"low confidence": picks(0.01, 0),
		"stale": func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
			a := answer(q, 0.9, 0)
			a.Outcome = harness.OutcomeStale
			return a, nil
		},
		"cancelled": failing(context.Canceled),
	}
	for name, script := range cases {
		f := &fakeAsker{script: script}
		s, _ := service(t, f, func(c *Config) { c.RunBudget = 100; c.PerMinute = 100 })
		for i := 0; i < 3*failureThreshold; i++ {
			if _, out := s.Model(context.Background(), providers("a-model", "b-model")); out.Status == StatusPaused {
				t.Fatalf("%s paused the provider after %d requests", name, i)
			}
		}
		if snap := s.Health().Snapshot(); !snap.ProviderPaused.IsZero() || len(snap.PausedKinds) != 0 {
			t.Errorf("%s: %+v", name, snap)
		}
	}
}

func TestEveryProviderFaultCountsAndRefusalsBeforeTheProviderDoNot(t *testing.T) {
	for name, script := range map[string]func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error){
		"timeout": failing(context.DeadlineExceeded),
		"unavailable": func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
			a := answer(q, 1, 0)
			a.Outcome = harness.OutcomeUnavailable
			return a, nil
		},
		"denied": func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
			a := answer(q, 1, 0)
			a.Outcome = harness.OutcomeDenied
			return a, nil
		},
		"failed": failing(errors.New("x")),
	} {
		s, _ := service(t, &fakeAsker{script: script})
		for i := 0; i < failureThreshold; i++ {
			s.Model(context.Background(), providers("a-model", "b-model"))
		}
		if _, out := s.Model(context.Background(), providers("a-model", "b-model")); out.Status != StatusPaused {
			t.Errorf("%s did not count: %+v", name, out)
		}
	}
	// Budget, rate, busy and malformed-request refusals are not provider faults.
	s, _ := service(t, &fakeAsker{script: picks(0.9, 0)}, func(c *Config) { c.RunBudget = 1 })
	for i := 0; i < 3*failureThreshold; i++ {
		s.Model(context.Background(), providers("a-model", "b-model"))
	}
	if snap := s.Health().Snapshot(); !snap.ProviderPaused.IsZero() {
		t.Fatalf("budget refusals paused the provider: %+v", snap)
	}
}

func TestOneProvidersHealthIsSharedByEveryRunThatUsesIt(t *testing.T) {
	shared := NewHealth(nil)
	f := &fakeAsker{script: failing(errors.New("down"))}
	first, _ := service(t, f, func(c *Config) { c.Health = shared; c.Now = shared.now })
	for i := 0; i < failureThreshold; i++ {
		first.Model(context.Background(), providers("a-model", "b-model"))
	}
	g := &fakeAsker{script: picks(0.9, 0)}
	second, _ := service(t, g, func(c *Config) { c.Health = shared; c.Now = shared.now })
	if _, out := second.Model(context.Background(), providers("a-model", "b-model")); out.Status != StatusPaused || len(g.asked()) != 0 {
		t.Fatalf("a second run asked a provider the first had found down: %+v", out)
	}
	if first.Health() != shared || second.Health() != shared {
		t.Fatal("the health record is not shared")
	}
}

func TestACallThatTheProviderNeverReturnsCannotGrowWithoutLimit(t *testing.T) {
	h := NewHealth(nil)
	for i := 0; i < maxAbandoned-1; i++ {
		h.abandon()
	}
	if _, ok := h.admit(KindModel); !ok {
		t.Fatal("refused below the bound")
	}
	h.abandon()
	if _, ok := h.admit(KindModel); ok {
		t.Fatal("admitted at the bound")
	}
	h.returned()
	if _, ok := h.admit(KindModel); !ok {
		t.Fatal("still refused after one returned")
	}
}

func TestTheJournalCountsEveryRequestAndKeepsOnlyContentFreeRecentEvents(t *testing.T) {
	secret := "SECRET-NAME-12345"
	f := &fakeAsker{script: picks(0.9, 0)}
	s, clk := service(t, f, func(c *Config) { c.RunBudget = 1000; c.PerMinute = 1000 })
	ctx := context.Background()
	for i := 0; i < maxEvents+10; i++ {
		s.Visibility(ctx, items(secret+"-", 4))
		clk.advance(time.Millisecond)
	}
	s.Model(ctx, providers("only-model")) // not needed
	s.CommandRisk(ctx, "run_command", Facts{"n": 1})
	snap := s.Health().Snapshot()
	if snap.Counts[KindVisibility][StatusApplied] != int64(maxEvents+10) || snap.Counts[KindModel][StatusNotNeeded] != 1 || snap.Counts[KindCommandRisk][StatusApplied] != 1 {
		t.Fatalf("counts = %v", snap.Counts)
	}
	if len(snap.Recent) != maxEvents {
		t.Fatalf("recent = %d", len(snap.Recent))
	}
	last := snap.Recent[len(snap.Recent)-1]
	if last.Kind != KindCommandRisk || last.Status != StatusApplied || last.Latency != "lt250ms" || last.Candidates != 3 || last.At.IsZero() {
		t.Fatalf("last = %+v", last)
	}
	for _, e := range snap.Recent {
		if strings.Contains(e.Latency+string(e.Kind)+string(e.Status), secret) {
			t.Fatalf("an event holds content: %+v", e)
		}
	}
	// The snapshot is a copy.
	snap.Counts[KindVisibility][StatusApplied] = 0
	snap.Recent[0].Status = "tampered"
	again := s.Health().Snapshot()
	if again.Counts[KindVisibility][StatusApplied] == 0 || again.Recent[0].Status == "tampered" {
		t.Fatal("a snapshot shares state with the journal")
	}
}

func TestLatencyBucketsAreCoarse(t *testing.T) {
	for d, want := range map[time.Duration]string{0: "lt250ms", 249 * time.Millisecond: "lt250ms", 250 * time.Millisecond: "lt1s", 999 * time.Millisecond: "lt1s",
		time.Second: "lt2s", 1999 * time.Millisecond: "lt2s", 2 * time.Second: "ge2s", time.Hour: "ge2s"} {
		if got := latencyBucket(d); got != want {
			t.Errorf("%v: %s, want %s", d, got, want)
		}
	}
}

func TestEverythingIsRecordedIncludingRefusalsBeforeTheProvider(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f, func(c *Config) { c.Enabled = []Kind{KindModel, KindFileSensitivity}; c.MaxClass = ClassOpaque })
	ctx := context.Background()
	s.Visibility(ctx, items("a", 3))              // disabled
	s.FileSensitivity(ctx, "a.txt", nil)          // ineligible
	s.Model(ctx, providers("a-model", "b-model")) // applied
	s.Model(ctx, providers("a-model", "a-model")) // invalid
	counts := s.Health().Snapshot().Counts
	if counts[KindVisibility][StatusDisabled] != 1 || counts[KindFileSensitivity][StatusIneligible] != 1 || counts[KindModel][StatusApplied] != 1 || counts[KindModel][StatusInvalid] != 1 {
		t.Fatalf("counts = %v", counts)
	}
	for _, st := range Statuses() {
		if st == "" {
			t.Fatal("an empty status")
		}
	}
	if len(Statuses()) != 15 {
		t.Fatalf("statuses = %v", Statuses())
	}
}

func openedHealth(t *testing.T) (*Health, *clock) {
	t.Helper()
	clk := newClock()
	h := NewHealth(clk.now)
	for i := 0; i < failureThreshold; i++ {
		h.provider.failure(clk.now())
	}
	clk.advance(baseCooldown)
	return h, clk
}

func TestAProviderProbeIsGivenBackWhenTheKindRefuses(t *testing.T) {
	h, clk := openedHealth(t)
	kb := &breaker{}
	for i := 0; i < failureThreshold; i++ {
		kb.failure(clk.now())
	}
	kb.until = clk.now().Add(time.Hour) // the kind is still paused when the provider may be tried
	h.kinds[KindVisibility] = kb
	if _, ok := h.admit(KindVisibility); ok {
		t.Fatal("a paused kind was admitted")
	}
	if h.provider.probing {
		t.Fatal("the provider's probe was kept by a request that never went out")
	}
	if p, ok := h.admit(KindModel); !ok || !p.providerProbe {
		t.Fatalf("another kind could not use the probe: %+v %v", p, ok)
	}
}

func TestSettlingTreatsEveryStatusAsWhatItShowsAboutTheProviderAndTheKind(t *testing.T) {
	// A usable or merely unconfident answer closes the breakers.
	for _, status := range []Status{StatusApplied, StatusLowConfidence} {
		h, _ := openedHealth(t)
		h.kinds[KindModel] = &breaker{open: true, failures: failureThreshold, cooldown: baseCooldown}
		p, ok := h.admit(KindModel)
		if !ok || !p.providerProbe || !p.kindProbe {
			t.Fatalf("%s: %+v %v", status, p, ok)
		}
		h.settle(KindModel, p, status)
		if h.provider.open || h.provider.probing || h.kinds[KindModel].open || h.kinds[KindModel].probing {
			t.Errorf("%s did not close the breakers: %+v %+v", status, h.provider, *h.kinds[KindModel])
		}
	}
	// A malformed answer shows the provider is alive and the kind is not.
	h, _ := openedHealth(t)
	p, _ := h.admit(KindModel)
	h.settle(KindModel, p, StatusInvalid)
	if h.provider.open || h.kinds[KindModel].failures != 1 {
		t.Errorf("invalid: %+v %+v", h.provider, *h.kinds[KindModel])
	}
	// A fault doubles the provider's pause and frees its probe.
	h, clk := openedHealth(t)
	p, _ = h.admit(KindModel)
	h.settle(KindModel, p, StatusTimeout)
	if !h.provider.open || h.provider.probing || h.provider.cooldown != 2*baseCooldown || !h.provider.until.Equal(clk.now().Add(2*baseCooldown)) {
		t.Errorf("timeout: %+v", h.provider)
	}
	// A request that reached no verdict only gives its probes back.
	for _, status := range []Status{StatusBusy, StatusExhausted, StatusCancelled, StatusStale, StatusNotNeeded} {
		h, _ := openedHealth(t)
		h.kinds[KindModel] = &breaker{open: true, failures: failureThreshold, cooldown: baseCooldown}
		p, ok := h.admit(KindModel)
		if !ok {
			t.Fatalf("%s: not admitted", status)
		}
		h.settle(KindModel, p, status)
		if !h.provider.open || h.provider.probing || !h.kinds[KindModel].open || h.kinds[KindModel].probing {
			t.Errorf("%s: probes were not given back or a breaker moved: %+v %+v", status, h.provider, *h.kinds[KindModel])
		}
		if _, ok := h.admit(KindModel); !ok {
			t.Errorf("%s: the next request could not try", status)
		}
	}
	// A kind that is probing and then answers well is not left stuck.
	g := newClock()
	sh := NewHealth(g.now)
	sh.kinds[KindModel] = &breaker{open: true, failures: failureThreshold, cooldown: baseCooldown, until: g.now()}
	p, ok := sh.admit(KindModel)
	if !ok || !p.kindProbe {
		t.Fatal("no kind probe")
	}
	sh.settle(KindModel, p, StatusApplied)
	if p2, ok := sh.admit(KindModel); !ok || p2.kindProbe {
		t.Fatalf("after a good answer the kind is still on trial: %+v %v", p2, ok)
	}
}
