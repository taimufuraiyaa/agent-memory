package harnessjev

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harness/harnesstest"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/jev"
)

func TestTheProviderNeedsAnExplicitEgressConfirmationAndACredentialSource(t *testing.T) {
	token := func(context.Context) (string, error) { return "k", nil }
	if _, err := NewProvider(Config{Token: token}); err == nil {
		t.Error("a provider was built without confirming egress")
	}
	if _, err := NewProvider(Config{AllowEgress: true}); err == nil {
		t.Error("a provider was built without a credential source")
	}
	p, err := NewProvider(Config{Token: token, AllowEgress: true})
	if err != nil || p.client == nil {
		t.Fatalf("%v %+v", err, p)
	}
	m := p.Manifest()
	if m.ID != ProviderID || m.Kind != harness.KindDecision || !reflect.DeepEqual(m.Capabilities, []harness.CapabilityID{Capability}) || m.Version != harness.ContractVersion {
		t.Fatalf("manifest = %+v", m)
	}
	registry := harness.NewRegistry()
	if err := p.Register(registry); err != nil {
		t.Fatal(err)
	}
	if err := p.Register(registry); err == nil {
		t.Error("a second registration under the same identity was accepted")
	}
}

func TestLiveAccessComesFromTheModelListingAndCostsNoDecision(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(*fakeService)
		token func(context.Context) (string, error)
		want  harness.AccessState
		calls int
	}{
		"a working service":     {func(*fakeService) {}, nil, harness.AccessAvailable, 1},
		"a refused credential":  {func(f *fakeService) { f.status = http.StatusUnauthorized }, nil, harness.AccessDenied, 1},
		"a forbidden one":       {func(f *fakeService) { f.status = http.StatusForbidden }, nil, harness.AccessDenied, 1},
		"an overloaded service": {func(f *fakeService) { f.status = http.StatusServiceUnavailable }, nil, harness.AccessUnavailable, 1},
		"a rejected request":    {func(f *fakeService) { f.status = http.StatusBadRequest }, nil, harness.AccessUnavailable, 1},
		"no such model":         {func(f *fakeService) { f.models = `{"models":[{"name":"other"}]}` }, nil, harness.AccessUnavailable, 1},
		"a garbled listing":     {func(f *fakeService) { f.models = `not json` }, nil, harness.AccessUnavailable, 1},
		"no credential":         {func(*fakeService) {}, func(context.Context) (string, error) { return "", nil }, harness.AccessUnavailable, 0},
		"a failing credential":  {func(*fakeService) {}, func(context.Context) (string, error) { return "", errors.New("keychain locked") }, harness.AccessUnavailable, 0},
	} {
		f := newFakeService(t)
		tc.setup(f)
		s := openSession(t, newProvider(t, f, tc.token))
		if got := s.State(Capability); got != tc.want {
			t.Errorf("%s: state %s, want %s", name, got, tc.want)
		}
		if err := s.Access().Validate(s.Manifest(), s.Scope()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		f.mu.Lock()
		got, gets := len(f.requests), 0
		for _, r := range f.requests {
			if r.Method == http.MethodGet && r.Path == "/v1/models" {
				gets++
			}
		}
		f.mu.Unlock()
		if got != tc.calls || gets != tc.calls || len(f.posts()) != 0 {
			t.Errorf("%s: %d requests, %d listings, %d decisions", name, got, gets, len(f.posts()))
		}
	}
}

func TestEveryKindIsSentAsOneTypedChoiceQuestionWithinTheServicesLimits(t *testing.T) {
	want := map[harnessdecide.Kind]int{harnessdecide.KindVisibility: 6, harnessdecide.KindModel: 2, harnessdecide.KindTools: 4, harnessdecide.KindCache: 2,
		harnessdecide.KindCommandRisk: 3, harnessdecide.KindFileSensitivity: 2, harnessdecide.KindSubgoals: 3}
	for _, call := range kindCalls() {
		f := newFakeService(t)
		s, _ := newService(t, f)
		if _, out := call.run(s, "p"); out.Status != harnessdecide.StatusApplied {
			t.Fatalf("%s: %+v", call.kind, out)
		}
		req := f.last(t)
		q, ok := req.Request.Questions[questionID]
		if req.Path != "/v1/systemone" || req.Method != http.MethodPost || req.Auth != "Bearer "+testToken || req.Request.Model != "jev-latest" || len(req.Request.Questions) != 1 || !ok || q.Type != "choice" {
			t.Errorf("%s: request = %+v", call.kind, req)
			continue
		}
		if q.Instructions != instructions[call.kind] || len(q.Instructions) == 0 || len(q.Instructions) > maxInstructions {
			t.Errorf("%s: instructions = %q", call.kind, q.Instructions)
		}
		if len(q.Criteria) != want[call.kind] {
			t.Errorf("%s: %d choices, want %d", call.kind, len(q.Criteria), want[call.kind])
		}
		for i := 0; i < want[call.kind]; i++ {
			description, ok := q.Criteria[alias(i)]
			if !ok || description == "" || len(description) > maxDescription {
				t.Errorf("%s: choice %s = %q", call.kind, alias(i), description)
			}
		}
		if !strings.HasPrefix(req.Request.State, "Harness decision "+string(call.kind)+".") || len(req.Request.State) > maxState || len(req.Body) > 10000 {
			t.Errorf("%s: state %q, %d bytes", call.kind, req.Request.State, len(req.Body))
		}
	}
}

func TestDescriptionsAreBuiltFromFixedTextAndTheFactsOnly(t *testing.T) {
	f := newFakeService(t)
	s, _ := newService(t, f)
	for _, call := range kindCalls() {
		call.run(s, "p")
	}
	byKind := map[string]captured{}
	for _, req := range f.posts() {
		kind := strings.TrimSuffix(strings.TrimPrefix(req.Request.State, "Harness decision "), ".")
		kind = kind[:strings.Index(kind, ".v1")+3]
		byKind[kind] = req
	}
	check := func(kind harnessdecide.Kind, key, want string) {
		t.Helper()
		if got := byKind[string(kind)].Request.Questions[questionID].Criteria[key]; got != want {
			t.Errorf("%s %s = %q, want %q", kind, key, got, want)
		}
	}
	check(harnessdecide.KindVisibility, "a0", "context chunk from a memory source, relevance 50 of 100, about 100 tokens")
	check(harnessdecide.KindModel, "a0", "model alpha-model, cost tier 2, latency tier 1")
	check(harnessdecide.KindModel, "a1", "model beta-model, cost tier 5")
	check(harnessdecide.KindTools, "a0", "tool read_file, schema about 220 tokens, used 3 times so far")
	check(harnessdecide.KindTools, "a1", "tool list_dir")
	check(harnessdecide.KindCache, "a0", labels[string(harnessdecide.CacheStable)])
	check(harnessdecide.KindCommandRisk, "a1", labels[string(harnessdecide.RiskCareful)])
	check(harnessdecide.KindFileSensitivity, "a1", labels[string(harnessdecide.SensitivitySensitive)])
	check(harnessdecide.KindSubgoals, "a0", "subgoal: tidy the parser")
	check(harnessdecide.KindSubgoals, "a2", labels[harnessdecide.None])
	state := func(kind harnessdecide.Kind) string { return byKind[string(kind)].Request.State }
	for kind, want := range map[harnessdecide.Kind]string{
		harnessdecide.KindCache:           "Harness decision cache.v1. Observed cache hit rate 70 percent, over 9 turns, prefix about 4000 tokens.",
		harnessdecide.KindCommandRisk:     "Harness decision command_risk.v1. Action: run_command. Touches 2 paths, not a catalogued command (1).",
		harnessdecide.KindFileSensitivity: "Harness decision file_sensitivity.v1. Extension class: yaml. Directory depth 2, size bucket 3. Redacted path: config/settings.yaml.",
		harnessdecide.KindSubgoals:        "Harness decision subgoal_dedup.v1. New subgoal: clean up the parser.",
		harnessdecide.KindVisibility:      "Harness decision visibility.v1.",
		harnessdecide.KindModel:           "Harness decision model.v1.",
		harnessdecide.KindTools:           "Harness decision tools.v1.",
	} {
		if got := state(kind); got != want {
			t.Errorf("%s state = %q, want %q", kind, got, want)
		}
	}
}

func TestNothingThatNamesTheProjectOrTheRunLeavesThroughAnOpaqueKind(t *testing.T) {
	const secret = "PRIVATE-PATH-qxz789"
	f := newFakeService(t)
	s, _ := newService(t, f, func(c *harnessdecide.Config) { c.MaxClass = harnessdecide.ClassOpaque })
	for _, call := range kindCalls() {
		_, out := call.run(s, secret)
		spec, _ := harnessdecide.SpecOf(call.kind)
		if spec.Egress == harnessdecide.ClassInternal {
			if out.Status != harnessdecide.StatusIneligible {
				t.Errorf("%s: %+v", call.kind, out)
			}
			continue
		}
		if out.Status != harnessdecide.StatusApplied {
			t.Errorf("%s: %+v", call.kind, out)
		}
	}
	posts := f.posts()
	if len(posts) != 5 {
		t.Fatalf("%d decisions were sent, want the five opaque ones", len(posts))
	}
	for _, req := range posts {
		for _, forbidden := range []string{secret, testToken, "run-1", "tidy the parser", "settings.yaml", "Note"} {
			if strings.Contains(string(req.Body), forbidden) {
				t.Errorf("a request leaked %q: %s", forbidden, req.Body)
			}
		}
	}
}

func TestInternalKindsSendOnlyTheirOneLineAndNeverTheAliasedIdentifiers(t *testing.T) {
	const secret = "PRIVATE-ID-abc123"
	f := newFakeService(t)
	s, _ := newService(t, f)
	for _, call := range kindCalls() {
		call.run(s, secret)
	}
	for _, req := range f.posts() {
		if strings.Contains(string(req.Body), secret) || strings.Contains(string(req.Body), testToken) {
			t.Errorf("a request leaked an identifier or the credential: %s", req.Body)
		}
	}
	// The line of text is what an internal kind is for, and it is there.
	found := 0
	for _, req := range f.posts() {
		if strings.Contains(req.Request.State, "config/settings.yaml") || strings.Contains(req.Request.Questions[questionID].Criteria["a0"], "tidy the parser") {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("the two internal kinds carried their text %d times", found)
	}
}

func TestTheProviderDefendsItselfWhenAQuestionBypassesTheService(t *testing.T) {
	f := newFakeService(t)
	s := openSession(t, newProvider(t, f, nil))
	envelope, _ := s.Envelope(Capability, 1024)
	ask := func(q harness.DecisionQuestion) captured {
		t.Helper()
		q.Envelope = envelope
		if answer, err := s.Decide(context.Background(), q); err != nil || answer.Outcome != harness.OutcomeOK {
			t.Fatalf("%+v %v", answer, err)
		}
		return f.last(t)
	}
	// A note on an opaque kind is not forwarded.
	req := ask(harness.DecisionQuestion{Kind: string(harnessdecide.KindVisibility), Candidates: []string{"c0", "c1"},
		Evidence: []harness.EvidenceRef{{ID: "c0", Class: "memory", Note: "SECRET-NOTE-ONE"}, {ID: "c1", Class: "memory", Note: "SECRET-NOTE-TWO"}, {ID: "subject", Revision: "k=1", Note: "SECRET-SUBJECT"}}})
	if strings.Contains(string(req.Body), "SECRET") {
		t.Errorf("an opaque kind forwarded a note: %s", req.Body)
	}
	// An identifier that is not a plain name is not put in a description.
	req = ask(harness.DecisionQuestion{Kind: string(harnessdecide.KindModel), Candidates: []string{"alpha-model", "../secret/model", "Upper Case", "Beta-Model", strings.Repeat("l", 65)}})
	for _, forbidden := range []string{"secret/model", "Upper Case", "..", "Beta-Model", strings.Repeat("l", 65)} {
		if strings.Contains(string(req.Body), forbidden) {
			t.Errorf("a model description forwarded %q: %s", forbidden, req.Body)
		}
	}
	if got := req.Request.Questions[questionID].Criteria["a0"]; got != "model alpha-model" {
		t.Errorf("a plain name was not used: %q", got)
	}
	for _, key := range []string{"a1", "a2", "a3", "a4"} {
		if got := req.Request.Questions[questionID].Criteria[key]; got != "option "+key {
			t.Errorf("a fallback description for %s = %q", key, got)
		}
	}
	// A provider that happens to be named like a label is described as a provider.
	req = ask(harness.DecisionQuestion{Kind: string(harnessdecide.KindModel), Candidates: []string{"stable", "relevance", "none"}})
	for key, want := range map[string]string{"a0": "model stable", "a1": "model relevance", "a2": "model none"} {
		if got := req.Request.Questions[questionID].Criteria[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// Text from an internal kind is made printable and bounded. A session already refuses
	// control characters, so the provider's own cleaning is exercised directly.
	dirty, err := render(harness.DecisionQuestion{Kind: string(harnessdecide.KindFileSensitivity), Candidates: []string{"ordinary", "sensitive"},
		Evidence: []harness.EvidenceRef{{ID: "subject", Class: "txt", Revision: "d=1", Note: "a\nb\x1b[31mc\u2028d" + strings.Repeat("z", 400)}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range dirty.state {
		if r < 0x20 || r == 0x7f || r == 0x2028 {
			t.Fatalf("a control character was forwarded: %q", dirty.state)
		}
	}
	if len(dirty.state) > maxState || strings.Count(dirty.state, "z") > maxNote {
		t.Errorf("the note was not bounded: %d bytes", len(dirty.state))
	}
	req = ask(harness.DecisionQuestion{Kind: string(harnessdecide.KindFileSensitivity), Candidates: []string{"ordinary", "sensitive"},
		Evidence: []harness.EvidenceRef{{ID: "subject", Class: "txt", Revision: "d=1", Note: "line\u2028separator " + strings.Repeat("y", 100)}}})
	if strings.ContainsRune(req.Request.State, 0x2028) || !strings.Contains(req.Request.State, "line separator") {
		t.Errorf("a line separator was forwarded: %q", req.Request.State)
	}
	// Facts outside a kind's vocabulary or malformed facts say nothing.
	req = ask(harness.DecisionQuestion{Kind: string(harnessdecide.KindVisibility), Candidates: []string{"c0", "c1"},
		Evidence: []harness.EvidenceRef{{ID: "c0", Class: "memory", Revision: "t=5;zz=9"}, {ID: "c1", Class: "memory", Revision: "garbage"}, {ID: "subject", Revision: "k=1"}}})
	crit := req.Request.Questions[questionID].Criteria
	if crit["a0"] != "context chunk from a memory source, about 5 tokens" || crit["a1"] != "context chunk from a memory source" {
		t.Errorf("descriptions = %v", crit)
	}
}

func TestWorstCaseTextIsEitherWithinTheRequestLimitOrNeverSent(t *testing.T) {
	f := newFakeService(t)
	s, _ := newService(t, f)
	long := func(c string) []harnessdecide.Item {
		out := make([]harnessdecide.Item, 31)
		for i := range out {
			out[i] = harnessdecide.Item{ID: fmt.Sprintf("sg-%02d", i), Note: strings.Repeat(c, 200)}
		}
		return out
	}
	got, out := s.Subgoals(context.Background(), long("x"), harnessdecide.Item{ID: "new", Note: strings.Repeat("y", 200)})
	if out.Status != harnessdecide.StatusApplied || got != "sg-00" || len(f.last(t).Body) > 10000 {
		t.Fatalf("%q %+v, %d bytes", got, out, len(f.last(t).Body))
	}
	sent := len(f.posts())
	// Characters JSON must escape swell the request past the service's limit: it is refused.
	if got, out := s.Subgoals(context.Background(), long("<"), harnessdecide.Item{ID: "new", Note: "n"}); got != "" || out.Status != harnessdecide.StatusFailed || len(f.posts()) != sent {
		t.Fatalf("%q %+v, %d requests sent", got, out, len(f.posts())-sent)
	}
}

func TestASubsetAnswerIsTheChoiceAndTheLikelyOthersMostLikelyFirstWithinTheCap(t *testing.T) {
	visibility, _ := harnessdecide.SpecOf(harnessdecide.KindVisibility)
	model, _ := harnessdecide.SpecOf(harnessdecide.KindModel)
	r := func(limit int) rendered {
		return rendered{candidates: []string{"c0", "c1", "c2", "c3", "c4", "c5"}, limit: limit}
	}
	for name, tc := range map[string]struct {
		spec   harnessdecide.Spec
		limit  int
		answer jev.ChoiceAnswer
		want   []string
	}{
		"the choice and one other":         {visibility, 3, jev.ChoiceAnswer{Choice: "a0", Probabilities: map[string]float64{"a0": .5, "a1": .3, "a2": .1, "a3": .05, "a4": .03, "a5": .02}}, []string{"c0", "c1"}},
		"every likely one within the cap":  {visibility, 3, jev.ChoiceAnswer{Choice: "a0", Probabilities: map[string]float64{"a0": .4, "a1": .3, "a2": .3}}, []string{"c0", "c1", "c2"}},
		"a smaller cap":                    {visibility, 2, jev.ChoiceAnswer{Choice: "a0", Probabilities: map[string]float64{"a0": .4, "a1": .3, "a2": .3}}, []string{"c0", "c1"}},
		"a cap of one":                     {visibility, 1, jev.ChoiceAnswer{Choice: "a0", Probabilities: map[string]float64{"a0": .4, "a1": .3, "a2": .3}}, []string{"c0"}},
		"most likely first":                {visibility, 4, jev.ChoiceAnswer{Choice: "a2", Probabilities: map[string]float64{"a0": .25, "a1": .3, "a2": .4, "a3": .05}}, []string{"c2", "c1", "c0"}},
		"ties go to the earlier":           {visibility, 4, jev.ChoiceAnswer{Choice: "a5", Probabilities: map[string]float64{"a3": .25, "a1": .25, "a5": .4, "a0": .1}}, []string{"c5", "c1", "c3"}},
		"the threshold is inclusive":       {visibility, 4, jev.ChoiceAnswer{Choice: "a0", Probabilities: map[string]float64{"a0": .6, "a1": .2, "a2": .19}}, []string{"c0", "c1"}},
		"a choice below the threshold":     {visibility, 4, jev.ChoiceAnswer{Choice: "a3", Probabilities: map[string]float64{"a0": .15, "a1": .15, "a2": .15, "a3": .16, "a4": .15, "a5": .15}}, []string{"c3"}},
		"an unknown choice":                {visibility, 4, jev.ChoiceAnswer{Choice: "zz", Probabilities: map[string]float64{"a0": 1}}, nil},
		"a probability for an unknown key": {visibility, 4, jev.ChoiceAnswer{Choice: "a0", Probabilities: map[string]float64{"a0": .5, "a9": .5, "zz": .5}}, []string{"c0"}},
		"one answer is just the choice":    {model, 4, jev.ChoiceAnswer{Choice: "a1", Probabilities: map[string]float64{"a0": .45, "a1": .5}}, []string{"c1"}},
	} {
		if got := choose(tc.spec, r(tc.limit), tc.answer); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	for name, tc := range map[string]struct {
		in   []string
		max  int
		want []string
	}{
		"fits":          {[]string{"ab", "cd"}, 4, []string{"ab", "cd"}},
		"keeps leading": {[]string{"ab", "cd"}, 3, []string{"ab"}},
		"exactly":       {[]string{"ab"}, 2, []string{"ab"}},
		"nothing fits":  {[]string{"ab"}, 1, []string{}},
		"empty":         {nil, 4, nil},
	} {
		if got := fit(tc.in, tc.max); !reflect.DeepEqual(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
			t.Errorf("fit %s: %v, want %v", name, got, tc.want)
		}
	}
}

func TestTheServicesChoiceAndConfidenceReachTheCallerThroughTheWholeStack(t *testing.T) {
	f := newFakeService(t)
	f.set(func(f *fakeService) {
		f.answer = func(q sentQuestion) (string, map[string]float64, float64) {
			return odds(sortedKeys(q.Criteria), map[string]float64{"a2": 0.7, "a0": 0.25}, 0.83)
		}
	})
	s, _ := newService(t, f)
	got, out := s.Visibility(context.Background(), chunks("v-", 6))
	if out.Status != harnessdecide.StatusApplied || !reflect.DeepEqual(got, []string{"v-c", "v-a"}) || out.Confidence != 0.83 {
		t.Fatalf("%v %+v", got, out)
	}
	tool, out := s.Tools(context.Background(), named("read_file", "list_dir", "search", "git_status"), 2)
	if out.Status != harnessdecide.StatusApplied || !reflect.DeepEqual(tool, []string{"search", "read_file"}) {
		t.Fatalf("%v %+v", tool, out)
	}
	model, out := s.Model(context.Background(), named("alpha-model", "beta-model", "gamma-model"))
	if out.Status != harnessdecide.StatusApplied || model != "gamma-model" {
		t.Fatalf("%q %+v", model, out)
	}
	f.set(func(f *fakeService) {
		f.answer = func(q sentQuestion) (string, map[string]float64, float64) {
			return odds(sortedKeys(q.Criteria), map[string]float64{"a1": 0.8}, 0.4)
		}
	})
	if got, out := s.CommandRisk(context.Background(), "run_command", harnessdecide.Facts{"n": 1}); got != "" || out.Status != harnessdecide.StatusLowConfidence {
		t.Fatalf("%q %+v", got, out)
	}
}

func TestEveryWayTheServiceCanFailIsATypedOutcomeWithNoServiceTextInIt(t *testing.T) {
	asked := harness.DecisionQuestion{Kind: string(harnessdecide.KindModel), Candidates: []string{"a-model", "b-model"}}
	for name, tc := range map[string]struct {
		setup func(*fakeService)
		want  harness.Outcome
	}{
		"unauthorized":      {func(f *fakeService) { f.status = http.StatusUnauthorized }, harness.OutcomeDenied},
		"forbidden":         {func(f *fakeService) { f.status = http.StatusForbidden }, harness.OutcomeDenied},
		"too many requests": {func(f *fakeService) { f.status = http.StatusTooManyRequests }, harness.OutcomeUnavailable},
		"internal error":    {func(f *fakeService) { f.status = http.StatusInternalServerError }, harness.OutcomeUnavailable},
		"bad gateway":       {func(f *fakeService) { f.status = http.StatusBadGateway }, harness.OutcomeUnavailable},
		"bad request":       {func(f *fakeService) { f.status = http.StatusBadRequest }, harness.OutcomeFailed},
		"not found":         {func(f *fakeService) { f.status = http.StatusNotFound }, harness.OutcomeFailed},
		"not json":          {func(f *fakeService) { f.raw = "<html>oops</html>" }, harness.OutcomeFailed},
		"an unknown choice": {func(f *fakeService) {
			f.answer = func(sentQuestion) (string, map[string]float64, float64) {
				return "zz", map[string]float64{"a0": .5, "a1": .5}, 0.9
			}
		}, harness.OutcomeFailed},
		"inconsistent odds": {func(f *fakeService) {
			f.answer = func(sentQuestion) (string, map[string]float64, float64) {
				return "a0", map[string]float64{"a0": .9, "a1": .9}, 0.9
			}
		}, harness.OutcomeFailed},
		"confidence out of range": {func(f *fakeService) {
			f.answer = func(sentQuestion) (string, map[string]float64, float64) {
				return "a0", map[string]float64{"a0": .9, "a1": .1}, 1.5
			}
		}, harness.OutcomeFailed},
	} {
		f := newFakeService(t)
		session := openSession(t, newProvider(t, f, nil))
		tc.setup(f)
		q := asked
		q.Envelope, _ = session.Envelope(Capability, 256)
		answer, err := session.Decide(context.Background(), q)
		if err != nil || answer.Outcome != tc.want || len(answer.Selected) != 0 || answer.Confidence != 0 {
			t.Errorf("%s: %+v %v, want %s", name, answer, err, tc.want)
		}
		if text := fmt.Sprintf("%+v %v", answer, err); strings.Contains(text, testToken) || strings.Contains(text, "leaked") || strings.Contains(text, "oops") {
			t.Errorf("%s: an outcome carries service text: %s", name, text)
		}
	}
	// An unreachable service.
	f := newFakeService(t)
	session := openSession(t, newProvider(t, f, nil))
	f.srv.Close()
	q := asked
	q.Envelope, _ = session.Envelope(Capability, 256)
	if answer, err := session.Decide(context.Background(), q); err != nil || answer.Outcome != harness.OutcomeUnavailable {
		t.Errorf("an unreachable service: %+v %v", answer, err)
	}
}

func TestACredentialThatIsMissingWhenAskedIsDeniedAndNothingIsSent(t *testing.T) {
	// The credential is there when the session opens and gone when a decision is asked.
	for name, after := range map[string]func() (string, error){
		"empty":                      func() (string, error) { return "", nil },
		"failing":                    func() (string, error) { return "", errors.New("keychain locked with SECRET detail") },
		"failing with a stale value": func() (string, error) { return "stale-" + testToken, errors.New("keychain locked") },
	} {
		var mu sync.Mutex
		calls := 0
		token := func(context.Context) (string, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls == 1 {
				return testToken, nil
			}
			return after()
		}
		f := newFakeService(t)
		session := openSession(t, newProvider(t, f, token))
		q := harness.DecisionQuestion{Kind: string(harnessdecide.KindModel), Candidates: []string{"a-model", "b-model"}}
		q.Envelope, _ = session.Envelope(Capability, 256)
		answer, err := session.Decide(context.Background(), q)
		if err != nil || answer.Outcome != harness.OutcomeDenied || len(f.posts()) != 0 {
			t.Errorf("%s: %+v %v, %d decisions sent", name, answer, err, len(f.posts()))
		}
	}
	// A credential that is missing when the session opens makes the capability unavailable,
	// so no decision is ever attempted.
	f := newFakeService(t)
	session := openSession(t, newProvider(t, f, func(context.Context) (string, error) { return "", nil }))
	q := harness.DecisionQuestion{Kind: string(harnessdecide.KindModel), Candidates: []string{"a-model", "b-model"}}
	q.Envelope, _ = session.Envelope(Capability, 256)
	if answer, err := session.Decide(context.Background(), q); err != nil || answer.Outcome != harness.OutcomeUnavailable || len(f.posts()) != 0 {
		t.Errorf("no credential at open: %+v %v, %d sent", answer, err, len(f.posts()))
	}
}

func TestQuestionsTheProviderWasNotWrittenForAreRefusedNotForwarded(t *testing.T) {
	f := newFakeService(t)
	session := openSession(t, newProvider(t, f, nil))
	envelope, _ := session.Envelope(Capability, 256)
	many := make([]string, harnessdecide.MaxQuestionItems+2)
	for i := range many {
		many[i] = fmt.Sprintf("m%02d-model", i)
	}
	for name, tc := range map[string]struct {
		q    harness.DecisionQuestion
		want harness.Outcome
	}{
		"an unknown kind":         {harness.DecisionQuestion{Kind: "visibility.v9", Candidates: []string{"a", "b"}}, harness.OutcomeUnsupported},
		"another provider's kind": {harness.DecisionQuestion{Kind: "weather", Candidates: []string{"a", "b"}}, harness.OutcomeUnsupported},
		"one candidate":           {harness.DecisionQuestion{Kind: string(harnessdecide.KindModel), Candidates: []string{"a-model"}}, harness.OutcomeFailed},
		"too many candidates":     {harness.DecisionQuestion{Kind: string(harnessdecide.KindModel), Candidates: many}, harness.OutcomeFailed},
	} {
		q := tc.q
		q.Envelope = envelope
		answer, err := session.Decide(context.Background(), q)
		if err != nil || answer.Outcome != tc.want {
			t.Errorf("%s: %+v %v", name, answer, err)
		}
	}
	if len(f.posts()) != 0 {
		t.Fatalf("%d refused questions were sent", len(f.posts()))
	}
}

func TestAFailingServiceStopsBeingAskedThroughTheDecisionService(t *testing.T) {
	f := newFakeService(t)
	f.set(func(f *fakeService) { f.postStatus = http.StatusServiceUnavailable })
	s, _ := newService(t, f)
	statuses := map[harnessdecide.Status]int{}
	for i := 0; i < 8; i++ {
		_, out := s.Model(context.Background(), named("a-model", "b-model"))
		statuses[out.Status]++
	}
	if statuses[harnessdecide.StatusUnavailable] != 3 || statuses[harnessdecide.StatusPaused] != 5 || len(f.posts()) != 3 {
		t.Fatalf("statuses = %v, %d requests sent", statuses, len(f.posts()))
	}
	// A refused credential is denied and pauses the same way.
	g := newFakeService(t)
	g.set(func(g *fakeService) { g.postStatus = http.StatusUnauthorized })
	d, _ := newService(t, g)
	if _, out := d.Model(context.Background(), named("a-model", "b-model")); out.Status != harnessdecide.StatusDenied {
		t.Fatalf("%+v", out)
	}
	// A slow service costs one deadline and not one per turn.
	h := newFakeService(t)
	h.set(func(h *fakeService) { h.postDelay = 5 * time.Second })
	slow, _ := newService(t, h)
	begin := time.Now()
	if _, out := slow.CommandRisk(context.Background(), "run_command", harnessdecide.Facts{"n": 1}); out.Status != harnessdecide.StatusTimeout || time.Since(begin) > 3*time.Second {
		t.Fatalf("%+v after %v", out, time.Since(begin))
	}
}

func TestManyDecisionsAtOnceAreRaceFree(t *testing.T) {
	f := newFakeService(t)
	s, _ := newService(t, f, func(c *harnessdecide.Config) { c.MaxInFlight = 64 })
	var wg sync.WaitGroup
	errs := make(chan string, 64)
	for i := 0; i < 8; i++ {
		for _, call := range kindCalls() {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if got, out := call.run(s, "p"); out.Status != harnessdecide.StatusApplied || got == "" {
					errs <- fmt.Sprintf("%s: %q %+v", call.kind, got, out)
				}
			}()
		}
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestEveryDecisionFamilyPassesTheSameConformanceSuite(t *testing.T) {
	spec := harnesstest.DecisionSpec{Capability: Capability, Kind: string(harnessdecide.KindModel), Candidates: []string{"a", "b"}}
	t.Run("typesafe against a fake service", func(t *testing.T) {
		f := newFakeService(t)
		harnesstest.DecisionConformance(t, func(t *testing.T) *harness.Session { return openSession(t, newProvider(t, f, nil)) }, spec)
	})
	t.Run("a fake family", func(t *testing.T) {
		fake := spec
		fake.Capability = "decide"
		harnesstest.DecisionConformance(t, func(t *testing.T) *harness.Session {
			registry := harness.NewRegistry()
			if _, err := harnesstest.Register(registry, harnesstest.Manifest("fake-decision", harness.KindDecision, "decide"), harnesstest.Behavior{}); err != nil {
				t.Fatal(err)
			}
			s, err := registry.OpenSession(context.Background(), "fake-decision", harness.Scope{Workspace: "ws", Run: "run-1", Generation: 1})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			return s
		}, fake)
	})
}

// A live check against the real service, on synthetic data only. It is off unless asked for,
// and asks the service one tiny question about made-up options.
func TestLiveTypeSafeDecisionThroughTheProviderOnSyntheticData(t *testing.T) {
	live(t)
}

func TestAReplyThatCannotFitItsBoundIsATypedFailureNotAContractError(t *testing.T) {
	f := newFakeService(t)
	session := openSession(t, newProvider(t, f, nil))
	// The answer would be "alpha-model", eleven bytes, in a four-byte reply.
	envelope, _ := session.Envelope(Capability, 4)
	answer, err := session.Decide(context.Background(), harness.DecisionQuestion{Envelope: envelope, Kind: string(harnessdecide.KindModel), Candidates: []string{"alpha-model", "beta-model"}})
	if err != nil || answer.Outcome != harness.OutcomeFailed || len(answer.Selected) != 0 {
		t.Fatalf("%+v %v", answer, err)
	}
	// One that fits is returned whole.
	envelope, _ = session.Envelope(Capability, 11)
	answer, err = session.Decide(context.Background(), harness.DecisionQuestion{Envelope: envelope, Kind: string(harnessdecide.KindModel), Candidates: []string{"alpha-model", "beta-model"}})
	if err != nil || answer.Outcome != harness.OutcomeOK || !reflect.DeepEqual(answer.Selected, []string{"alpha-model"}) {
		t.Fatalf("%+v %v", answer, err)
	}
}

// The provider's own outcomes, without the session in front of it, which would otherwise
// answer a cancelled or expired caller itself.
func TestTheProvidersOwnOutcomesForACallerWhoCancelsOrRunsOutOfTime(t *testing.T) {
	f := newFakeService(t)
	f.set(func(f *fakeService) { f.postDelay = 5 * time.Second })
	leaf := &session{p: newProvider(t, f, nil)}
	envelope := harness.Envelope{Version: harness.ContractVersion, Provider: ProviderID, Capability: Capability, MaxBytes: 256, AccessRevision: 1, Scope: harness.Scope{Workspace: "ws", Run: "r", Generation: 1}}
	q := harness.DecisionQuestion{Envelope: envelope, Kind: string(harnessdecide.KindModel), Candidates: []string{"a-model", "b-model"}}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if answer, err := leaf.Decide(ctx, q); err != nil || answer.Outcome != harness.OutcomeCancelled {
		t.Errorf("a caller who cancelled: %+v %v", answer, err)
	}
	expiring, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if answer, err := leaf.Decide(expiring, q); err != nil || answer.Outcome != harness.OutcomeTimeout {
		t.Errorf("a caller who ran out of time: %+v %v", answer, err)
	}
}
