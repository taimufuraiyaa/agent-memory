package harnessdecide

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// fakeAsker is a decision session whose behavior a test scripts, including the ways a real
// provider misbehaves. It records every question it is asked.
type fakeAsker struct {
	mu        sync.Mutex
	questions []harness.DecisionQuestion
	script    func(ctx context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error)
	envErr    error
}

func (f *fakeAsker) Envelope(capability harness.CapabilityID, maxBytes int) (harness.Envelope, error) {
	if f.envErr != nil {
		return harness.Envelope{}, f.envErr
	}
	return harness.Envelope{Version: harness.ContractVersion, Provider: "fake-decision", Scope: harness.Scope{Workspace: "ws", Run: "run-1", Generation: 1},
		AccessRevision: 1, Capability: capability, MaxBytes: maxBytes}, nil
}

func (f *fakeAsker) Decide(ctx context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	f.mu.Lock()
	f.questions = append(f.questions, q)
	script := f.script
	f.mu.Unlock()
	return script(ctx, q)
}

func (f *fakeAsker) asked() []harness.DecisionQuestion {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]harness.DecisionQuestion(nil), f.questions...)
}

func (f *fakeAsker) last(t *testing.T) harness.DecisionQuestion {
	t.Helper()
	asked := f.asked()
	if len(asked) == 0 {
		t.Fatal("the provider was never asked")
	}
	return asked[len(asked)-1]
}

// answer builds a well-formed answer for q selecting the candidates at the given positions.
func answer(q harness.DecisionQuestion, confidence float64, positions ...int) harness.DecisionAnswer {
	selected := make([]string, len(positions))
	for i, p := range positions {
		selected[i] = q.Candidates[p]
	}
	return harness.DecisionAnswer{Envelope: q.Envelope, Outcome: harness.OutcomeOK, Selected: selected, Confidence: confidence}
}

// picks returns a script that always selects the same positions with the same confidence.
func picks(confidence float64, positions ...int) func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	return func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
		return answer(q, confidence, positions...), nil
	}
}

// clock is a fake clock a test advances by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func allKinds() []Kind { return Kinds() }

// service builds a service over a fake with every kind enabled and the internal class
// allowed, unless the test overrides the configuration.
func service(t *testing.T, f *fakeAsker, tweak ...func(*Config)) (*Service, *clock) {
	t.Helper()
	c := newClock()
	cfg := Config{Asker: f, Capability: "decide", MaxClass: ClassInternal, Enabled: allKinds(), Now: c.now}
	for _, fn := range tweak {
		fn(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func items(prefix string, n int) []Item {
	out := make([]Item, n)
	for i := range out {
		id := fmt.Sprintf("%s%d", prefix, i)
		if i < 26 {
			id = prefix + string(rune('a'+i))
		}
		out[i] = Item{ID: id, Class: "memory", Facts: Facts{"t": 100 + i, "r": 50}}
	}
	return out
}

func providers(ids ...string) []Item {
	out := make([]Item, len(ids))
	for i, id := range ids {
		out[i] = Item{ID: id}
	}
	return out
}
