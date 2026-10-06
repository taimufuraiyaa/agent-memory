package harnessdecide

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

func hub(t *testing.T, f *fakeAsker, maxRuns int, tweak ...func(*Config)) (*Hub, *clock) {
	t.Helper()
	c := newClock()
	cfg := Config{Asker: f, Capability: "decide", MaxClass: ClassInternal, Enabled: allKinds(), Now: c.now}
	for _, fn := range tweak {
		fn(&cfg)
	}
	h, err := NewHub(cfg, maxRuns)
	if err != nil {
		t.Fatal(err)
	}
	return h, c
}

func ask(s *Service) Status {
	_, out := s.Model(context.Background(), providers("a-model", "b-model"))
	return out.Status
}

func TestEachRunHasItsOwnBudgetAndTheSameRunTheSameService(t *testing.T) {
	h, _ := hub(t, &fakeAsker{script: picks(0.9, 0)}, 8, func(c *Config) { c.RunBudget = 2 })
	a, b := h.Service("run-a"), h.Service("run-b")
	if a == b || h.Service("run-a") != a || h.Service("run-b") != b || h.Service("") == a {
		t.Fatal("services are not one per run")
	}
	for i := 0; i < 2; i++ {
		if ask(a) != StatusApplied {
			t.Fatalf("run a request %d", i)
		}
	}
	if got := ask(a); got != StatusExhausted {
		t.Fatalf("run a over budget: %s", got)
	}
	if got := ask(b); got != StatusApplied {
		t.Fatalf("run b was charged for run a: %s", got)
	}
	if a.Used() != 2 || b.Used() != 1 {
		t.Fatalf("used %d and %d", a.Used(), b.Used())
	}
}

func TestEveryRunSharesTheProvidersHealth(t *testing.T) {
	f := &fakeAsker{script: failing(errors.New("down"))}
	h, _ := hub(t, f, 8)
	a, b := h.Service("run-a"), h.Service("run-b")
	for i := 0; i < failureThreshold; i++ {
		ask(a)
	}
	asked := len(f.asked())
	if got := ask(b); got != StatusPaused || len(f.asked()) != asked {
		t.Fatalf("run b asked a provider run a found down: %s", got)
	}
	if a.Health() != b.Health() || h.Health() != a.Health() {
		t.Fatal("health is not shared")
	}
}

func TestEveryRunSharesTheRateAndTheInFlightBound(t *testing.T) {
	h, _ := hub(t, &fakeAsker{script: picks(0.9, 0)}, 8, func(c *Config) { c.PerMinute = 3; c.RunBudget = 100 })
	a, b := h.Service("run-a"), h.Service("run-b")
	got := []Status{ask(a), ask(a), ask(b), ask(a), ask(b)}
	want := []Status{StatusApplied, StatusApplied, StatusApplied, StatusBusy, StatusBusy}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("requests %v, want %v", got, want)
		}
	}
	if a.Used() != 2 || b.Used() != 1 {
		t.Fatalf("refused requests were charged to a budget: %d and %d", a.Used(), b.Used())
	}

	gate := make(chan struct{})
	entered := make(chan struct{})
	slow := &fakeAsker{script: func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
		entered <- struct{}{}
		<-gate
		return answer(q, 0.9, 0), nil
	}}
	g, _ := hub(t, slow, 8, func(c *Config) { c.MaxInFlight = 1 })
	done := make(chan Status, 1)
	go func() { done <- ask(g.Service("run-a")) }()
	<-entered
	if got := ask(g.Service("run-b")); got != StatusBusy {
		t.Fatalf("a second run beyond the shared bound: %s", got)
	}
	close(gate)
	if got := <-done; got != StatusApplied {
		t.Fatalf("the admitted request: %s", got)
	}
}

func TestTheOldestRunsServiceIsForgottenBeyondTheBound(t *testing.T) {
	h, _ := hub(t, &fakeAsker{script: picks(0.9, 0)}, 2, func(c *Config) { c.RunBudget = 1 })
	a := h.Service("run-a")
	ask(a)
	h.Service("run-b")
	h.Service("run-c") // run-a is forgotten
	if again := h.Service("run-a"); again == a || again.Used() != 0 {
		t.Fatalf("a forgotten run kept its service: %v", again == a)
	}
	if len(h.services) != 2 || len(h.order) != 2 {
		t.Fatalf("%d services, %d in order", len(h.services), len(h.order))
	}
	d, err := NewHub(Config{Asker: &fakeAsker{script: picks(0.9, 0)}, Capability: "decide", Enabled: allKinds()}, -5)
	if err != nil || d.max != DefaultMaxRuns {
		t.Fatalf("%v %v", err, d)
	}
}

func TestAHubRefusesAConfigurationAServiceWouldRefuse(t *testing.T) {
	if _, err := NewHub(Config{Capability: "decide"}, 4); err == nil {
		t.Fatal("a hub without a session")
	}
	if _, err := NewHub(Config{Asker: &fakeAsker{}, Capability: "decide", Enabled: []Kind{KindModel}, Required: []Kind{KindCommandRisk}}, 4); err == nil {
		t.Fatal("a hub with a required kind that is not enabled")
	}
}

func TestManyRunsAskingAtOnceAreRaceFree(t *testing.T) {
	h, _ := hub(t, &fakeAsker{script: picks(0.9, 0)}, 16, func(c *Config) { c.MaxInFlight = 64; c.PerMinute = 10000; c.RunBudget = 10000 })
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := h.Service(fmt.Sprintf("run-%d", i%20))
			for j := 0; j < 5; j++ {
				if got := ask(s); got != StatusApplied {
					t.Errorf("run %d: %s", i, got)
				}
			}
		}(i)
	}
	wg.Wait()
}
