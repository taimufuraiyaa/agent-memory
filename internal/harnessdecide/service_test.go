package harnessdecide

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// call runs one request of a kind with inputs that are valid for it, and returns the advice
// as a comparable string ("" when there is none) with the outcome.
type probe struct {
	kind Kind
	run  func(s *Service) (string, Outcome)
	// advice is what a provider selecting candidate position 0 should produce.
	advice string
}

func probes() []probe {
	ctx := context.Background()
	return []probe{
		{KindVisibility, func(s *Service) (string, Outcome) {
			got, out := s.Visibility(ctx, items("chunk-", 6))
			return strings.Join(got, ","), out
		}, "chunk-a"},
		{KindModel, func(s *Service) (string, Outcome) {
			got, out := s.Model(ctx, providers("alpha-model", "beta-model", "gamma-model"))
			return got, out
		}, "alpha-model"},
		{KindTools, func(s *Service) (string, Outcome) {
			got, out := s.Tools(ctx, providers("read_file", "list_dir", "search", "git_status"), 2)
			return strings.Join(got, ","), out
		}, "read_file"},
		{KindCache, func(s *Service) (string, Outcome) {
			got, out := s.Cache(ctx, Facts{"h": 70, "n": 9, "p": 4000})
			return string(got), out
		}, "stable"},
		{KindCommandRisk, func(s *Service) (string, Outcome) {
			got, out := s.CommandRisk(ctx, "run_command", Facts{"np": 1, "n": 2})
			return string(got), out
		}, "routine"},
		{KindFileSensitivity, func(s *Service) (string, Outcome) {
			got, out := s.FileSensitivity(ctx, "config/settings.yaml", Facts{"d": 2, "z": 3})
			return string(got), out
		}, "ordinary"},
		{KindSubgoals, func(s *Service) (string, Outcome) {
			got, out := s.Subgoals(ctx, []Item{{ID: "sg-1", Note: "tidy the parser"}, {ID: "sg-2", Note: "add tests"}}, Item{ID: "new", Note: "clean up the parser"})
			return got, out
		}, "sg-1"},
	}
}

func TestEveryKindAsksAWellFormedQuestionAndReturnsTheChoiceItWasGiven(t *testing.T) {
	for _, p := range probes() {
		f := &fakeAsker{script: picks(0.9, 0)}
		s, _ := service(t, f)
		got, out := p.run(s)
		if out.Status != StatusApplied || out.Kind != p.kind || got != p.advice || out.Confidence != 0.9 || out.Asked < 2 {
			t.Errorf("%s: %q %+v", p.kind, got, out)
			continue
		}
		q := f.last(t)
		spec, _ := SpecOf(p.kind)
		if q.Kind != string(p.kind) || q.Envelope.Capability != "decide" || q.Envelope.MaxBytes != spec.MaxReplyBytes || len(q.Candidates) != out.Asked {
			t.Errorf("%s: question %+v", p.kind, q)
		}
		if err := harness.ValidateDecisionAnswer(q, answer(q, 0.9, 0)); err != nil {
			t.Errorf("%s: the question and a matching answer are not valid together: %v", p.kind, err)
		}
	}
}

func TestEveryOutcomeAProviderCanGiveLeavesTheCallersChoiceInPlace(t *testing.T) {
	outcomes := map[harness.Outcome]Status{
		harness.OutcomeDenied:      StatusDenied,
		harness.OutcomeStale:       StatusStale,
		harness.OutcomeTimeout:     StatusTimeout,
		harness.OutcomeCancelled:   StatusCancelled,
		harness.OutcomeUnavailable: StatusUnavailable,
		harness.OutcomeUnsupported: StatusUnavailable,
		harness.OutcomeFailed:      StatusFailed,
		harness.OutcomePartial:     StatusFailed,
		harness.Outcome("novel"):   StatusFailed,
	}
	for _, p := range probes() {
		for outcome, want := range outcomes {
			f := &fakeAsker{script: func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
				a := answer(q, 0.99, 0)
				a.Outcome = outcome
				return a, nil
			}}
			s, _ := service(t, f)
			got, out := p.run(s)
			if got != "" || out.Status != want || out.Cautious {
				t.Errorf("%s with outcome %s: %q %+v", p.kind, outcome, got, out)
			}
		}
	}
}

func TestEveryErrorAProviderOrSessionCanReturnLeavesTheCallersChoiceInPlace(t *testing.T) {
	errs := map[string]struct {
		err  error
		want Status
	}{
		"stale":            {harness.ErrStale, StatusStale},
		"wrapped stale":    {fmt.Errorf("x: %w", harness.ErrStale), StatusStale},
		"closed":           {harness.ErrClosed, StatusCancelled},
		"invalid":          {harness.ErrInvalid, StatusInvalid},
		"deadline":         {context.DeadlineExceeded, StatusTimeout},
		"canceled":         {context.Canceled, StatusCancelled},
		"anything else":    {errors.New("boom"), StatusFailed},
		"a wrapped detail": {fmt.Errorf("provider said %w", errors.New("secret service text")), StatusFailed},
	}
	for _, p := range probes() {
		for name, tc := range errs {
			f := &fakeAsker{script: func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error) {
				return harness.DecisionAnswer{}, tc.err
			}}
			s, _ := service(t, f)
			if got, out := p.run(s); got != "" || out.Status != tc.want {
				t.Errorf("%s with %s: %q %+v", p.kind, name, got, out)
			}
		}
	}
}

func TestAnAnswerThatIsNotExactlyWhatWasAskedIsIgnored(t *testing.T) {
	type tamper func(q harness.DecisionQuestion, a *harness.DecisionAnswer)
	cases := map[string]struct {
		change tamper
		want   Status
	}{
		"an unknown candidate": {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Selected = []string{"not-offered"} }, StatusInvalid},
		"an empty candidate":   {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Selected = []string{""} }, StatusInvalid},
		"a duplicated candidate": {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) {
			a.Selected = []string{q.Candidates[0], q.Candidates[0]}
		}, StatusInvalid},
		"NaN confidence":        {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Confidence = math.NaN() }, StatusInvalid},
		"infinite confidence":   {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Confidence = math.Inf(1) }, StatusInvalid},
		"confidence above one":  {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Confidence = 1.01 }, StatusInvalid},
		"negative confidence":   {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Confidence = -0.1 }, StatusInvalid},
		"another envelope":      {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Envelope.Scope.Run = "another-run" }, StatusStale},
		"another capability":    {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Envelope.Capability = "other" }, StatusStale},
		"another revision":      {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Envelope.AccessRevision = 99 }, StatusStale},
		"nothing selected":      {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Selected = nil }, StatusInvalid},
		"far too many selected": {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Selected = q.Candidates }, StatusInvalid},
		"a reply over its bound": {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) {
			a.Selected = []string{strings.Repeat("x", 4096)}
		}, StatusInvalid},
		"confidence below its bar": {func(q harness.DecisionQuestion, a *harness.DecisionAnswer) { a.Confidence = 0.49 }, StatusLowConfidence},
	}
	for _, p := range probes() {
		for name, tc := range cases {
			f := &fakeAsker{script: func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
				a := answer(q, 0.95, 0)
				tc.change(q, &a)
				return a, nil
			}}
			s, _ := service(t, f)
			got, out := p.run(s)
			want := tc.want
			if name == "nothing selected" && p.kind == KindVisibility {
				want = StatusApplied // promoting nothing is a valid answer for the one kind that may select none
			}
			if want == StatusApplied {
				if out.Status != StatusApplied || got != "" {
					t.Errorf("%s with %s: %q %+v", p.kind, name, got, out)
				}
				continue
			}
			if got != "" || out.Status != want {
				t.Errorf("%s with %s: %q %+v", p.kind, name, got, out)
			}
			if want != StatusLowConfidence && out.Confidence != 0 {
				t.Errorf("%s with %s: a confidence was reported for an answer that was not used: %+v", p.kind, name, out)
			}
		}
	}
}

func TestEachKindsConfidenceFloorIsTheFloor(t *testing.T) {
	for _, p := range probes() {
		spec, _ := SpecOf(p.kind)
		for _, tc := range []struct {
			confidence float64
			want       Status
		}{{spec.MinConfidence - 0.001, StatusLowConfidence}, {spec.MinConfidence, StatusApplied}, {1, StatusApplied}, {0, StatusLowConfidence}} {
			f := &fakeAsker{script: picks(tc.confidence, 0)}
			s, _ := service(t, f)
			got, out := p.run(s)
			if out.Status != tc.want || (tc.want == StatusApplied) != (got != "") {
				t.Errorf("%s at %.3f: %q %+v", p.kind, tc.confidence, got, out)
			}
			if tc.want == StatusLowConfidence && out.Confidence != tc.confidence {
				t.Errorf("%s: a low confidence was not reported: %+v", p.kind, out)
			}
		}
	}
}

func TestAProviderThatPanicsIsContainedAndCounted(t *testing.T) {
	for _, p := range probes() {
		f := &fakeAsker{script: func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error) { panic("provider bug") }}
		s, _ := service(t, f)
		if got, out := p.run(s); got != "" || out.Status != StatusFailed {
			t.Errorf("%s: %q %+v", p.kind, got, out)
		}
	}
}

func TestADeadlineIsEnforcedEvenWhenTheProviderIgnoresItsContext(t *testing.T) {
	release := make(chan struct{})
	var started atomic.Int32
	f := &fakeAsker{script: func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
		started.Add(1)
		<-release // never looks at the context
		return answer(q, 0.9, 0), nil
	}}
	s, _ := service(t, f)
	begin := time.Now()
	got, out := s.CommandRisk(context.Background(), "run_command", Facts{"n": 1})
	elapsed := time.Since(begin)
	spec, _ := SpecOf(KindCommandRisk)
	if got != "" || out.Status != StatusTimeout || elapsed < spec.Deadline-50*time.Millisecond || elapsed > spec.Deadline+time.Second {
		t.Fatalf("%q %+v after %v", got, out, elapsed)
	}
	if snap := s.Health().Snapshot(); snap.Abandoned != 1 {
		t.Fatalf("abandoned = %d", snap.Abandoned)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for s.Health().Snapshot().Abandoned != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if snap := s.Health().Snapshot(); snap.Abandoned != 0 {
		t.Fatalf("a returned provider is still counted: %d", snap.Abandoned)
	}
	if started.Load() != 1 {
		t.Fatalf("the provider was asked %d times", started.Load())
	}
}

func TestACancelledCallerIsCancelledNotTimedOut(t *testing.T) {
	f := &fakeAsker{script: func(ctx context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
		<-ctx.Done()
		return harness.DecisionAnswer{}, ctx.Err()
	}}
	s, _ := service(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	begin := time.Now()
	got, out := s.Model(ctx, providers("a-model", "b-model"))
	if got != "" || out.Status != StatusCancelled || time.Since(begin) > time.Second {
		t.Fatalf("%q %+v after %v", got, out, time.Since(begin))
	}
	// Cancelled before the call, the provider is never reached.
	f2 := &fakeAsker{script: picks(0.9, 0)}
	s2, _ := service(t, f2)
	pre, stop := context.WithCancel(context.Background())
	stop()
	if got, out := s2.Model(pre, providers("a-model", "b-model")); got != "" || out.Status != StatusCancelled || len(f2.asked()) != 0 || s2.Used() != 0 {
		t.Fatalf("a call cancelled in advance: %q %+v, asked %d, used %d", got, out, len(f2.asked()), s2.Used())
	}
}

func TestAPartialSelectionIsStillBoundedByWhatTheCallerCanUse(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0, 1, 2)}
	s, _ := service(t, f)
	// Tools: three offered of five, only two fit, so a three-tool answer is invalid.
	got, out := s.Tools(context.Background(), providers("t_a", "t_b", "t_c", "t_d", "t_e"), 2)
	if got != nil || out.Status != StatusInvalid {
		t.Fatalf("%v %+v", got, out)
	}
	// Visibility: six chunks allow at most three promoted; four is invalid, three is fine.
	f.script = picks(0.9, 0, 1, 2, 3)
	if got, out := s.Visibility(context.Background(), items("v-", 6)); got != nil || out.Status != StatusInvalid {
		t.Fatalf("%v %+v", got, out)
	}
	f.script = picks(0.9, 5, 0, 3)
	got, out = s.Visibility(context.Background(), items("v-", 6))
	if out.Status != StatusApplied || !reflect.DeepEqual(got, []string{"v-f", "v-a", "v-d"}) {
		t.Fatalf("%v %+v", got, out)
	}
}

func TestAnAnswerCanOnlyNameWhatTheCallerOfferedAndAliasesAreMappedBack(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 2, 0)}
	s, _ := service(t, f)
	offered := items("private-path/", 5)
	got, out := s.Visibility(context.Background(), offered)
	if out.Status != StatusApplied || !reflect.DeepEqual(got, []string{"private-path/c", "private-path/a"}) {
		t.Fatalf("%v %+v", got, out)
	}
	q := f.last(t)
	if !reflect.DeepEqual(q.Candidates, []string{"c0", "c1", "c2", "c3", "c4"}) {
		t.Fatalf("candidates = %v", q.Candidates)
	}
	// Tools and providers are named by the harness, so they go as they are.
	f.script = picks(0.9, 1)
	got, _ = s.Tools(context.Background(), providers("read_file", "list_dir", "search"), 1)
	if !reflect.DeepEqual(got, []string{"list_dir"}) || !reflect.DeepEqual(f.last(t).Candidates, []string{"read_file", "list_dir", "search"}) {
		t.Fatalf("%v %v", got, f.last(t).Candidates)
	}
}

func TestNothingThatNamesTheProjectIsSentByAnOpaqueKind(t *testing.T) {
	secret := "SECRET-PROJECT-PATH-token-abc123"
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f, func(c *Config) { c.MaxClass = ClassOpaque })
	offered := items(secret+"/", 6)
	if _, out := s.Visibility(context.Background(), offered); out.Status != StatusApplied {
		t.Fatalf("%+v", out)
	}
	if _, out := s.CommandRisk(context.Background(), "run_command", Facts{"np": 1}); out.Status != StatusApplied {
		t.Fatalf("%+v", out)
	}
	for _, q := range f.asked() {
		text := fmt.Sprintf("%+v", q)
		if strings.Contains(text, secret) {
			t.Errorf("%s leaked a project name: %s", q.Kind, text)
		}
		for _, ref := range q.Evidence {
			if ref.Note != "" {
				t.Errorf("%s: an opaque question carried a note: %+v", q.Kind, ref)
			}
		}
	}
}

func TestInternalKindsAreNotAskedAtTheOpaqueClassAndNothingIsAssembled(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f, func(c *Config) { c.MaxClass = ClassOpaque })
	if got, out := s.FileSensitivity(context.Background(), "secrets/prod.env", Facts{"d": 1}); got != "" || out.Status != StatusIneligible {
		t.Fatalf("%q %+v", got, out)
	}
	if got, out := s.Subgoals(context.Background(), []Item{{ID: "a", Note: "one"}}, Item{ID: "b", Note: "two"}); got != "" || out.Status != StatusIneligible {
		t.Fatalf("%q %+v", got, out)
	}
	if len(f.asked()) != 0 {
		t.Fatalf("the provider was reached: %v", f.asked())
	}
}

func TestInternalKindsSendTheirNoteOnlyWhenTheClassAllowsIt(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 1)}
	s, _ := service(t, f)
	got, out := s.FileSensitivity(context.Background(), "config/prod.env", Facts{"d": 2})
	if got != SensitivitySensitive || out.Status != StatusApplied {
		t.Fatalf("%q %+v", got, out)
	}
	q := f.last(t)
	if len(q.Evidence) != 1 || q.Evidence[0].ID != "subject" || q.Evidence[0].Note != "config/prod.env" || q.Evidence[0].Class != "env" || q.Evidence[0].Revision != "d=2" {
		t.Fatalf("evidence = %+v", q.Evidence)
	}
	if !reflect.DeepEqual(q.Candidates, []string{"ordinary", "sensitive"}) {
		t.Fatalf("candidates = %v", q.Candidates)
	}
	f.script = picks(0.9, 1)
	got2, out := s.Subgoals(context.Background(), []Item{{ID: "sg-1", Note: "tidy"}, {ID: "sg-2", Note: "test"}}, Item{ID: "x", Note: "clean"})
	if got2 != "sg-2" || out.Status != StatusApplied {
		t.Fatalf("%q %+v", got2, out)
	}
	q = f.last(t)
	if !reflect.DeepEqual(q.Candidates, []string{"c0", "c1", "none"}) || len(q.Evidence) != 3 || q.Evidence[2].ID != "subject" || q.Evidence[2].Note != "clean" || q.Evidence[0].Note != "tidy" {
		t.Fatalf("question = %+v", q)
	}
	// "none" is a real answer and is not an identifier.
	f.script = picks(0.9, 1) // with one existing subgoal the candidates are it and "none"
	if got, out := s.Subgoals(context.Background(), []Item{{ID: "sg-1", Note: "tidy"}}, Item{ID: "x", Note: "clean"}); got != "" || out.Status != StatusApplied {
		t.Fatalf("none: %q %+v", got, out)
	}
}

func TestOnlyEnabledKindsAreAskedAndTheOthersNeverReachTheProvider(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f, func(c *Config) { c.Enabled = []Kind{KindModel} })
	for _, p := range probes() {
		got, out := p.run(s)
		if p.kind == KindModel {
			if out.Status != StatusApplied || got == "" {
				t.Errorf("model: %q %+v", got, out)
			}
			continue
		}
		if got != "" || out.Status != StatusDisabled {
			t.Errorf("%s: %q %+v", p.kind, got, out)
		}
	}
	if n := len(f.asked()); n != 1 {
		t.Fatalf("the provider was asked %d times", n)
	}
	none, _ := service(t, &fakeAsker{script: picks(0.9, 0)}, func(c *Config) { c.Enabled = nil })
	for _, p := range probes() {
		if _, out := p.run(none); out.Status != StatusDisabled {
			t.Errorf("%s with nothing enabled: %+v", p.kind, out)
		}
	}
}

func TestThereIsNothingToDecideWhenTheChoiceIsTrivialOrUnmeasured(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f)
	ctx := context.Background()
	for name, run := range map[string]func() (Outcome, bool){
		"one chunk":            func() (Outcome, bool) { _, o := s.Visibility(ctx, items("c", 1)); return o, true },
		"no chunks":            func() (Outcome, bool) { _, o := s.Visibility(ctx, nil); return o, true },
		"one provider":         func() (Outcome, bool) { _, o := s.Model(ctx, providers("only-model")); return o, true },
		"every tool fits":      func() (Outcome, bool) { _, o := s.Tools(ctx, providers("a_tool", "b_tool"), 2); return o, true },
		"more room than tools": func() (Outcome, bool) { _, o := s.Tools(ctx, providers("a_tool", "b_tool"), 9); return o, true },
		"no room for a tool": func() (Outcome, bool) {
			_, o := s.Tools(ctx, providers("a_tool", "b_tool", "c_tool"), 0)
			return o, true
		},
		"a cache that is unmeasured": func() (Outcome, bool) { _, o := s.Cache(ctx, nil); return o, true },
		"too few measured turns": func() (Outcome, bool) {
			_, o := s.Cache(ctx, Facts{"h": 50, "n": MinCacheObservations - 1})
			return o, true
		},
		"no hit rate":          func() (Outcome, bool) { _, o := s.Cache(ctx, Facts{"n": 50}); return o, true },
		"no existing subgoals": func() (Outcome, bool) { _, o := s.Subgoals(ctx, nil, Item{ID: "x", Note: "a"}); return o, true },
	} {
		out, _ := run()
		if out.Status != StatusNotNeeded {
			t.Errorf("%s: %+v", name, out)
		}
	}
	if len(f.asked()) != 0 {
		t.Fatalf("the provider was asked about nothing: %d", len(f.asked()))
	}
	if n := s.Health().Snapshot().Counts[KindTools][StatusNotNeeded]; n != 3 {
		t.Fatalf("tools that needed no decision were recorded %d times, want 3", n)
	}
}

func TestASubsetQuestionTellsTheProviderHowManyMayBeSelected(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f)
	s.Visibility(context.Background(), items("v-", 6))
	q := f.last(t)
	if last := q.Evidence[len(q.Evidence)-1]; last.ID != "subject" || last.Revision != "k=3" {
		t.Fatalf("visibility evidence = %+v", q.Evidence)
	}
	s.Tools(context.Background(), providers("a_tool", "b_tool", "c_tool", "d_tool"), 2)
	q = f.last(t)
	if last := q.Evidence[len(q.Evidence)-1]; last.ID != "subject" || last.Revision != "k=2" {
		t.Fatalf("tools evidence = %+v", q.Evidence)
	}
	s.Model(context.Background(), providers("a-model", "b-model"))
	for _, ref := range f.last(t).Evidence {
		if ref.ID == "subject" {
			t.Fatalf("a one-answer question carried a selection cap: %+v", ref)
		}
	}
}

func TestMalformedRequestsAreInvalidAndNeverAssembled(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f)
	ctx := context.Background()
	dup := []Item{{ID: "same"}, {ID: "same"}}
	for name, out := range map[string]Outcome{
		"duplicate chunk ids":                  second(s.Visibility(ctx, dup)),
		"an empty chunk id":                    second(s.Visibility(ctx, []Item{{ID: "a"}, {ID: ""}})),
		"a fact outside the kind":              second(s.Visibility(ctx, []Item{{ID: "a", Facts: Facts{"zz": 1}}, {ID: "b"}})),
		"a path as a provider id":              second(s.Model(ctx, providers("a-model", "../escape"))),
		"duplicate providers":                  second(s.Model(ctx, providers("a-model", "a-model"))),
		"more providers than a question holds": second(s.Model(ctx, manyProviders(MaxQuestionItems+1))),
		"a note on an opaque kind":             second(s.Visibility(ctx, []Item{{ID: "a", Note: "text"}, {ID: "b"}})),
		"a bad cache fact":                     second(s.Cache(ctx, Facts{"h": 50, "n": 9, "zz": 1})),
		"a bad risk class":                     second(s.CommandRisk(ctx, "Run/Command", nil)),
		"a bad risk fact":                      second(s.CommandRisk(ctx, "run_command", Facts{"nope": 1})),
		"a long file path":                     second(s.FileSensitivity(ctx, strings.Repeat("p", maxNoteBytes+1), nil)),
		"a control character":                  second(s.FileSensitivity(ctx, "a\x1b[31m.txt", nil)),
		"an empty file path":                   second(s.FileSensitivity(ctx, "", nil)),
	} {
		if out.Status != StatusInvalid {
			t.Errorf("%s: %+v", name, out)
		}
	}
	if len(f.asked()) != 0 {
		t.Fatalf("a malformed request reached the provider: %d", len(f.asked()))
	}
}

func second[T any](_ T, o Outcome) Outcome { return o }

func TestMoreItemsThanAQuestionCanHoldAreCutFromTheEnd(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f)
	got, out := s.Visibility(context.Background(), items("big-", 40))
	if out.Status != StatusApplied || out.Asked != MaxQuestionItems || len(got) != 1 || got[0] != "big-a" {
		t.Fatalf("%v %+v", got, out)
	}
	if n := len(f.last(t).Candidates); n != MaxQuestionItems {
		t.Fatalf("candidates = %d", n)
	}
	f.script = picks(0.9, 0)
	tools := make([]Item, 40)
	for i := range tools {
		tools[i] = Item{ID: fmt.Sprintf("tool_%02d", i)}
	}
	if _, out := s.Tools(context.Background(), tools, 3); out.Status != StatusApplied || out.Asked != MaxQuestionItems {
		t.Fatalf("%+v", out)
	}
}

func TestTheRunBudgetBoundsRequestsAndRefusalsCostNothing(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f, func(c *Config) { c.RunBudget = 3 })
	for i := 0; i < 3; i++ {
		if _, out := s.Model(context.Background(), providers("a-model", "b-model")); out.Status != StatusApplied {
			t.Fatalf("request %d: %+v", i, out)
		}
	}
	for i := 0; i < 5; i++ {
		if got, out := s.Model(context.Background(), providers("a-model", "b-model")); got != "" || out.Status != StatusExhausted {
			t.Fatalf("over budget: %q %+v", got, out)
		}
	}
	if s.Used() != 3 || len(f.asked()) != 3 {
		t.Fatalf("used %d, asked %d", s.Used(), len(f.asked()))
	}
	// Requests that never reach the provider do not spend the budget.
	g, _ := service(t, &fakeAsker{script: picks(0.9, 0)}, func(c *Config) { c.RunBudget = 1; c.Enabled = []Kind{KindModel} })
	g.Visibility(context.Background(), items("c", 4))
	g.Model(context.Background(), providers("only-model"))
	if g.Used() != 0 {
		t.Fatalf("refused requests spent the budget: %d", g.Used())
	}
}

func TestTheRateLimitIsPerMinuteAndSlides(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, clk := service(t, f, func(c *Config) { c.PerMinute = 2; c.RunBudget = 100 })
	ask := func() Status {
		_, out := s.Model(context.Background(), providers("a-model", "b-model"))
		return out.Status
	}
	if ask() != StatusApplied || ask() != StatusApplied {
		t.Fatal("the first two should go")
	}
	if got := ask(); got != StatusBusy {
		t.Fatalf("third within the minute: %s", got)
	}
	clk.advance(59 * time.Second)
	if got := ask(); got != StatusBusy {
		t.Fatalf("still within the minute: %s", got)
	}
	clk.advance(2 * time.Second)
	if got := ask(); got != StatusApplied {
		t.Fatalf("after the minute: %s", got)
	}
}

func TestTheRateWindowIsExactlyOneMinute(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, clk := service(t, f, func(c *Config) { c.PerMinute = 2; c.RunBudget = 100 })
	ask := func() Status {
		_, out := s.Model(context.Background(), providers("a-model", "b-model"))
		return out.Status
	}
	ask()
	ask()
	clk.advance(time.Minute - time.Nanosecond)
	if got := ask(); got != StatusBusy {
		t.Fatalf("just inside the minute: %s", got)
	}
	clk.advance(time.Nanosecond)
	if got := ask(); got != StatusApplied {
		t.Fatalf("exactly a minute later: %s", got)
	}
}

func TestRequestsBeyondTheInFlightBoundAreBusyNotQueued(t *testing.T) {
	gate := make(chan struct{})
	entered := make(chan struct{}, 4)
	f := &fakeAsker{script: func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
		entered <- struct{}{}
		<-gate
		return answer(q, 0.9, 0), nil
	}}
	s, _ := service(t, f, func(c *Config) { c.MaxInFlight = 2 })
	var wg sync.WaitGroup
	results := make(chan Status, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, out := s.Model(context.Background(), providers("a-model", "b-model"))
			results <- out.Status
		}()
	}
	<-entered
	<-entered
	begin := time.Now()
	if got, out := s.Model(context.Background(), providers("a-model", "b-model")); got != "" || out.Status != StatusBusy || time.Since(begin) > 500*time.Millisecond {
		t.Fatalf("a third request: %q %+v after %v", got, out, time.Since(begin))
	}
	close(gate)
	wg.Wait()
	close(results)
	for status := range results {
		if status != StatusApplied {
			t.Fatalf("an admitted request ended %s", status)
		}
	}
	// A slot is free again afterwards.
	f.script = picks(0.9, 0)
	if _, out := s.Model(context.Background(), providers("a-model", "b-model")); out.Status != StatusApplied {
		t.Fatalf("%+v", out)
	}
}

func TestAnEnvelopeThatCannotBeMadeIsAFailure(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0), envErr: errors.New("capability not declared")}
	s, _ := service(t, f)
	if got, out := s.Model(context.Background(), providers("a-model", "b-model")); got != "" || out.Status != StatusFailed {
		t.Fatalf("%q %+v", got, out)
	}
	if len(f.asked()) != 0 {
		t.Fatal("a provider was asked without an envelope")
	}
}

func TestConfigurationsThatCouldNotBehaveAsStatedAreRefused(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	good := Config{Asker: f, Capability: "decide", MaxClass: ClassInternal, Enabled: allKinds()}
	if _, err := New(good); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"no session":                                   func(c *Config) { c.Asker = nil },
		"no capability":                                func(c *Config) { c.Capability = "" },
		"an unknown class":                             func(c *Config) { c.MaxClass = 7 },
		"an unknown enabled kind":                      func(c *Config) { c.Enabled = []Kind{"visibility.v9"} },
		"an unknown required kind":                     func(c *Config) { c.Required = []Kind{"nope"} },
		"a kind that cannot be required":               func(c *Config) { c.Required = []Kind{KindModel} },
		"another that cannot":                          func(c *Config) { c.Required = []Kind{KindVisibility} },
		"a required kind not enabled":                  func(c *Config) { c.Enabled = []Kind{KindModel}; c.Required = []Kind{KindCommandRisk} },
		"a required internal kind at the opaque class": func(c *Config) { c.MaxClass = ClassOpaque; c.Required = []Kind{KindFileSensitivity} },
		"a negative in-flight bound":                   func(c *Config) { c.MaxInFlight = -1 },
		"a negative budget":                            func(c *Config) { c.RunBudget = -1 },
		"a negative rate":                              func(c *Config) { c.PerMinute = -1 },
	} {
		c := good
		mutate(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	ok := good
	ok.Required = []Kind{KindCommandRisk, KindFileSensitivity}
	if _, err := New(ok); err != nil {
		t.Errorf("the two raise-only kinds could not be required: %v", err)
	}
	opaque := good
	opaque.MaxClass = ClassOpaque
	opaque.Required = []Kind{KindCommandRisk}
	if _, err := New(opaque); err != nil {
		t.Errorf("an opaque kind could not be required at the opaque class: %v", err)
	}
	s, _ := New(good)
	if !s.Enabled(KindModel) || s.Enabled("nope") {
		t.Fatal("Enabled is wrong")
	}
	d, _ := New(Config{Asker: f, Capability: "decide"})
	if d.cfg.MaxInFlight != DefaultMaxInFlight || d.cfg.RunBudget != DefaultRunBudget || d.cfg.PerMinute != DefaultPerMinute || d.Enabled(KindModel) {
		t.Fatalf("defaults = %+v", d.cfg)
	}
}

func TestRequiredKindsFallBackToCautionOnEveryFailureAndOnlyThen(t *testing.T) {
	ctx := context.Background()
	failures := map[string]func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error){
		"an error": func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error) {
			return harness.DecisionAnswer{}, errors.New("down")
		},
		"a denial": func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
			a := answer(q, 1, 0)
			a.Outcome = harness.OutcomeDenied
			return a, nil
		},
		"unavailable": func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
			a := answer(q, 1, 0)
			a.Outcome = harness.OutcomeUnavailable
			return a, nil
		},
		"low confidence": picks(0.1, 0),
		"an unknown pick": func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
			a := answer(q, 1, 0)
			a.Selected = []string{"zzz"}
			return a, nil
		},
		"a panic": func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error) { panic("x") },
	}
	for name, script := range failures {
		f := &fakeAsker{script: script}
		s, _ := service(t, f, func(c *Config) { c.Required = []Kind{KindCommandRisk, KindFileSensitivity} })
		risk, out := s.CommandRisk(ctx, "run_command", Facts{"n": 1})
		if risk != RiskCareful || !out.Cautious || out.Status == StatusApplied {
			t.Errorf("risk with %s: %q %+v", name, risk, out)
		}
		sens, out := s.FileSensitivity(ctx, "a/b.txt", nil)
		if sens != SensitivitySensitive || !out.Cautious || out.Status == StatusApplied {
			t.Errorf("sensitivity with %s: %q %+v", name, sens, out)
		}
		// Advisory kinds in the same service never fall back to caution; they just give no advice.
		if risk, out := second2(service(t, f)).CommandRisk(ctx, "run_command", Facts{"n": 1}); risk != "" || out.Cautious {
			t.Errorf("an advisory risk with %s: %q %+v", name, risk, out)
		}
	}
	// A good answer is used as given: required does not mean "always careful".
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f, func(c *Config) { c.Required = []Kind{KindCommandRisk, KindFileSensitivity} })
	if risk, out := s.CommandRisk(ctx, "run_command", nil); risk != RiskRoutine || out.Cautious || out.Status != StatusApplied {
		t.Fatalf("%q %+v", risk, out)
	}
	if sens, out := s.FileSensitivity(ctx, "a/b.txt", nil); sens != SensitivityOrdinary || out.Cautious || out.Status != StatusApplied {
		t.Fatalf("%q %+v", sens, out)
	}
	// A required kind with the provider paused is cautious without asking at all.
	g := &fakeAsker{script: func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error) {
		return harness.DecisionAnswer{}, errors.New("down")
	}}
	h, _ := service(t, g, func(c *Config) { c.Required = []Kind{KindCommandRisk} })
	for i := 0; i < failureThreshold; i++ {
		h.CommandRisk(ctx, "run_command", nil)
	}
	before := len(g.asked())
	if risk, out := h.CommandRisk(ctx, "run_command", nil); risk != RiskCareful || out.Status != StatusPaused || !out.Cautious || len(g.asked()) != before {
		t.Fatalf("paused: %q %+v", risk, out)
	}
}

func second2(s *Service, _ *clock) *Service { return s }

func TestRequiredNeverTurnsAMalformedRequestIntoPermission(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f, func(c *Config) { c.Required = []Kind{KindCommandRisk} })
	risk, out := s.CommandRisk(context.Background(), "BAD CLASS", nil)
	if risk != RiskCareful || out.Status != StatusInvalid || !out.Cautious {
		t.Fatalf("%q %+v", risk, out)
	}
}

func TestEveryReturnedValueIsBoundedByConstruction(t *testing.T) {
	// Whatever a provider selects among the candidates it was given, the value handed back is
	// one of them, so it can only be one the caller offered or one of the fixed labels.
	offeredChunks := map[string]bool{}
	chunks := items("bound-", 8)
	for _, c := range chunks {
		offeredChunks[c.ID] = true
	}
	for position := 0; position < 8; position++ {
		f := &fakeAsker{script: picks(0.9, position)}
		s, _ := service(t, f)
		got, out := s.Visibility(context.Background(), chunks)
		if out.Status != StatusApplied || len(got) != 1 || !offeredChunks[got[0]] {
			t.Fatalf("position %d: %v %+v", position, got, out)
		}
	}
	for position := 0; position < 3; position++ {
		f := &fakeAsker{script: picks(0.9, position)}
		s, _ := service(t, f)
		risk, _ := s.CommandRisk(context.Background(), "run_command", nil)
		if risk != RiskRoutine && risk != RiskCareful && risk != RiskHazardous {
			t.Fatalf("a risk outside the labels: %q", risk)
		}
		strategy, _ := s.Cache(context.Background(), Facts{"h": 10, "n": 9})
		if position < 2 && strategy != CacheStable && strategy != CacheRelevance {
			t.Fatalf("a strategy outside the labels: %q", strategy)
		}
	}
}

func manyProviders(n int) []Item {
	out := make([]Item, n)
	for i := range out {
		out[i] = Item{ID: fmt.Sprintf("model-%02d", i)}
	}
	return out
}

func TestACancelledCallersFaultIsNeverTheProvidersFault(t *testing.T) {
	// The provider cancels the caller's context and then reports an ordinary error, the way a
	// transport that notices the cancellation does. Whichever event the call sees first, the
	// result is a cancellation and does not count against the provider.
	for i := 0; i < 3*failureThreshold; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		f := &fakeAsker{script: func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error) {
			cancel()
			return harness.DecisionAnswer{}, errors.New("read tcp: connection reset")
		}}
		health := NewHealth(nil)
		s, _ := service(t, f, func(c *Config) { c.Health = health; c.Now = health.now })
		if got, out := s.Model(ctx, providers("a-model", "b-model")); got != "" || out.Status != StatusCancelled {
			t.Fatalf("round %d: %q %+v", i, got, out)
		}
		if !health.Snapshot().ProviderPaused.IsZero() || len(health.Snapshot().PausedKinds) != 0 {
			t.Fatal("a cancellation counted as a fault")
		}
	}
	// A caller that has gone gets nothing, even from a provider that answered well.
	gone, cancelGone := context.WithCancel(context.Background())
	g := &fakeAsker{script: func(_ context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
		cancelGone()
		return answer(q, 0.99, 0), nil
	}}
	gs, _ := service(t, g)
	for i := 0; i < 20; i++ {
		if got, out := gs.Model(gone, providers("a-model", "b-model")); got != "" || out.Status != StatusCancelled {
			t.Fatalf("a cancelled caller was handed advice: %q %+v", got, out)
		}
	}
	// And repeated cancellations through one health record never pause anything.
	health := NewHealth(nil)
	for i := 0; i < 3*failureThreshold; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		f := &fakeAsker{script: func(context.Context, harness.DecisionQuestion) (harness.DecisionAnswer, error) {
			cancel()
			return harness.DecisionAnswer{}, errors.New("boom")
		}}
		s, _ := service(t, f, func(c *Config) { c.Health = health; c.Now = health.now })
		if _, out := s.Model(ctx, providers("a-model", "b-model")); out.Status != StatusCancelled {
			t.Fatalf("round %d: %+v", i, out)
		}
	}
	if !health.Snapshot().ProviderPaused.IsZero() {
		t.Fatal("repeated cancellations paused the provider")
	}
}

func TestAProbeRefusedForBudgetIsGivenBackSoAnotherRunCanTryTheProvider(t *testing.T) {
	shared := NewHealth(nil)
	clk := newClock()
	shared.now = clk.now
	down := &fakeAsker{script: failing(errors.New("down"))}
	first, _ := service(t, down, func(c *Config) { c.Health = shared; c.Now = clk.now; c.RunBudget = failureThreshold })
	for i := 0; i < failureThreshold; i++ {
		first.Model(context.Background(), providers("a-model", "b-model"))
	}
	clk.advance(baseCooldown)
	// The first run's budget is spent, so its request is refused after the provider was let
	// through as a probe: the probe must be given back.
	if _, out := first.Model(context.Background(), providers("a-model", "b-model")); out.Status != StatusExhausted {
		t.Fatalf("%+v", out)
	}
	up := &fakeAsker{script: picks(0.9, 0)}
	second, _ := service(t, up, func(c *Config) { c.Health = shared; c.Now = clk.now })
	if got, out := second.Model(context.Background(), providers("a-model", "b-model")); got == "" || out.Status != StatusApplied {
		t.Fatalf("a refused probe was never returned: %q %+v", got, out)
	}
}

func TestMoreExistingSubgoalsThanAQuestionCanHoldAreCutFromTheEnd(t *testing.T) {
	f := &fakeAsker{script: picks(0.9, 0)}
	s, _ := service(t, f)
	existing := make([]Item, 40)
	for i := range existing {
		existing[i] = Item{ID: fmt.Sprintf("sg-%02d", i), Note: "a subgoal"}
	}
	got, out := s.Subgoals(context.Background(), existing, Item{ID: "new", Note: "another"})
	if got != "sg-00" || out.Status != StatusApplied || out.Asked != MaxQuestionItems { // thirty-one subgoals and "none"
		t.Fatalf("%q %+v", got, out)
	}
}

// lateCancel is a context that becomes cancelled after the call has started: its first Err
// is nil and every later one is Canceled. Nothing watches its Done channel, so the result of
// a call cannot depend on which of two events is seen first.
type lateCancel struct {
	context.Context
	calls atomic.Int32
}

func (c *lateCancel) Err() error {
	if c.calls.Add(1) > 1 {
		return context.Canceled
	}
	return nil
}

func TestAnAnswerThatArrivesForACallerWhoHasGoneIsNotApplied(t *testing.T) {
	f := &fakeAsker{script: picks(0.99, 0)}
	s, _ := service(t, f)
	ctx := &lateCancel{Context: context.Background()}
	got, out := s.Model(ctx, providers("a-model", "b-model"))
	if got != "" || out.Status != StatusCancelled || len(f.asked()) != 1 {
		t.Fatalf("%q %+v, asked %d", got, out, len(f.asked()))
	}
	// And the same provider failing for a caller who has gone is not a provider fault.
	g := &fakeAsker{script: failing(errors.New("boom"))}
	health := NewHealth(nil)
	gs, _ := service(t, g, func(c *Config) { c.Health = health; c.Now = health.now })
	for i := 0; i < 3*failureThreshold; i++ {
		if _, out := gs.Model(&lateCancel{Context: context.Background()}, providers("a-model", "b-model")); out.Status != StatusCancelled {
			t.Fatalf("round %d: %+v", i, out)
		}
	}
	if !health.Snapshot().ProviderPaused.IsZero() {
		t.Fatal("failures for a caller who had gone paused the provider")
	}
}
