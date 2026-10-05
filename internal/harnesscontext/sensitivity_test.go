package harnesscontext

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

type sensitivityStub struct {
	verdict map[string]Sensitivity // by chunk ID
	err     error
	panics  bool
	delay   time.Duration
	asked   atomic.Int32
	seen    []string
}

func (s *sensitivityStub) Sensitivity(ctx context.Context, c Chunk) (Sensitivity, error) {
	s.asked.Add(1)
	s.seen = append(s.seen, c.ID)
	if s.panics {
		panic("advisor bug")
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return s.verdict[c.ID], s.err
}

func repoChunk(id string, level Sensitivity) Chunk {
	c := chunk(id, 0.5, "package x\n")
	c.Source, c.Ref, c.Sensitivity = SourceRepository, "internal/"+id+".go", level
	return c
}

func includedIDs(t *testing.T, source *RunSource) []string {
	t.Helper()
	owner := harnessrun.Owner{ClientID: "claude-desktop", Workspace: ws, GrantID: strings.Repeat("b", 32), GrantRevision: 1}
	pc, err := source.Context(context.Background(), harnessrun.Snapshot{RunID: "run-1", Owner: owner, Turn: 1})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(pc.Refs))
	for i, r := range pc.Refs {
		out[i] = r.ID
	}
	return out
}

func sensitivitySource(chunks []Chunk, advisor SensitivityAdvisor) *RunSource {
	s := &RunSource{Assembler: fixedAssembler(Config{}), Eligibility: Eligibility{MaxSensitivity: SensitivityInternal, AdvisorMaxSensitivity: SensitivityPublic},
		Budget: Budget{Total: 4000, ReserveOutput: 500}, Gather: func(context.Context, harnessrun.Owner) ([]Chunk, error) { return chunks, nil }}
	if advisor != nil {
		s.SensitivityFor = func(string) SensitivityAdvisor { return advisor }
	}
	return s
}

func has(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func TestAdviceThatAFileIsSensitiveWithholdsItFromAProviderThatCannotHaveIt(t *testing.T) {
	chunks := []Chunk{repoChunk("a-secretish", SensitivityInternal), repoChunk("b-plain", SensitivityInternal)}
	if got := includedIDs(t, sensitivitySource(chunks, nil)); !has(got, "a-secretish") || !has(got, "b-plain") {
		t.Fatalf("without advice: %v", got)
	}
	stub := &sensitivityStub{verdict: map[string]Sensitivity{"a-secretish": SensitivitySensitive}}
	got := includedIDs(t, sensitivitySource(chunks, stub))
	if has(got, "a-secretish") || !has(got, "b-plain") {
		t.Fatalf("with advice: %v", got)
	}
	// A provider whose class covers it still gets it: the gate is the same as for any chunk.
	wide := sensitivitySource(chunks, stub)
	wide.Eligibility.MaxSensitivity = SensitivityRestricted
	if got := includedIDs(t, wide); !has(got, "a-secretish") {
		t.Fatalf("a provider that may see sensitive data lost the chunk: %v", got)
	}
}

func TestAdviceCanOnlyRaiseNeverLowerAndNeverJudgeWhatItShouldNot(t *testing.T) {
	pinned := repoChunk("c-pinned", SensitivityInternal)
	pinned.Pinned, pinned.Trust = true, TrustProject
	run := chunk("d-memory", 0.5, "a memory")
	already := repoChunk("e-already", SensitivitySensitive)
	above := repoChunk("f-above", SensitivityRestricted)
	plain := repoChunk("g-plain", SensitivityInternal)
	stub := &sensitivityStub{verdict: map[string]Sensitivity{"c-pinned": SensitivityRestricted, "d-memory": SensitivityRestricted, "g-plain": SensitivityPublic, "e-already": SensitivityPublic}}
	source := sensitivitySource([]Chunk{pinned, run, already, above, plain}, stub)
	source.Eligibility.MaxSensitivity = SensitivityRestricted
	got := includedIDs(t, source)
	// Pinned instructions, other sources and chunks already at the highest class are not asked about.
	if has(stub.seen, "c-pinned") || has(stub.seen, "d-memory") || has(stub.seen, "e-already") || has(stub.seen, "f-above") {
		t.Fatalf("the advisor was asked about %v", stub.seen)
	}
	if !has(got, "c-pinned") || !has(got, "d-memory") || !has(got, "g-plain") {
		t.Fatalf("included %v", got)
	}
	// A verdict of public does not lower an internal chunk: with a provider limited to public data it is still withheld.
	narrow := sensitivitySource([]Chunk{plain}, &sensitivityStub{verdict: map[string]Sensitivity{"g-plain": SensitivityPublic}})
	narrow.Eligibility.MaxSensitivity = SensitivityPublic
	if got := includedIDs(t, narrow); has(got, "g-plain") {
		t.Fatalf("advice made an internal chunk shareable: %v", got)
	}
}

func TestAnAdvisorThatMisbehavesLeavesEveryChunkAsItWas(t *testing.T) {
	chunks := []Chunk{repoChunk("a-file", SensitivityInternal)}
	for name, stub := range map[string]*sensitivityStub{
		"an error":     {verdict: map[string]Sensitivity{"a-file": SensitivityRestricted}, err: errors.New("down")},
		"a panic":      {panics: true},
		"out of range": {verdict: map[string]Sensitivity{"a-file": Sensitivity(99)}},
		"a negative":   {verdict: map[string]Sensitivity{"a-file": Sensitivity(-4)}},
	} {
		if got := includedIDs(t, sensitivitySource(chunks, stub)); !has(got, "a-file") {
			t.Errorf("%s: the chunk was lost: %v", name, got)
		}
	}
	slow := &sensitivityStub{verdict: map[string]Sensitivity{"a-file": SensitivityRestricted}, delay: time.Minute}
	begin := time.Now()
	if got := includedIDs(t, sensitivitySource(chunks, slow)); !has(got, "a-file") || time.Since(begin) > 8*time.Second {
		t.Errorf("a slow advisor changed the result or held the turn for %v: %v", time.Since(begin), got)
	}
}

func TestOnlyASmallNumberOfChunksAreAskedAboutInOneAssembly(t *testing.T) {
	var chunks []Chunk
	for i := 0; i < maxSensitivityAsks+10; i++ {
		chunks = append(chunks, repoChunk(fmt.Sprintf("r%02d", i), SensitivityInternal))
	}
	stub := &sensitivityStub{}
	includedIDs(t, sensitivitySource(chunks, stub))
	if int(stub.asked.Load()) != maxSensitivityAsks {
		t.Fatalf("asked about %d chunks, want %d", stub.asked.Load(), maxSensitivityAsks)
	}
}

func TestTheDecisionServiceSeesOnlyARedactedPathAndRemembersItsVerdicts(t *testing.T) {
	p := &positional{positions: []int{1}, conf: 0.9} // labels are ordinary, sensitive
	service := decisionService(t, p, func(c *harnessdecide.Config) { c.MaxClass = harnessdecide.ClassInternal })
	advisor := NewServiceSensitivityAdvisor(service, func(s string) string { return strings.ReplaceAll(s, "hunter2hunter2", "[REDACTED]") })
	c := repoChunk("a-file", SensitivityInternal)
	c.Ref, c.Text = "config/hunter2hunter2/prod.env", strings.Repeat("x", 3000)
	got, err := advisor.Sensitivity(context.Background(), c)
	if err != nil || got != SensitivitySensitive {
		t.Fatalf("%v %v", got, err)
	}
	q := p.questions[0]
	ev := q.Evidence[0]
	if q.Kind != "file_sensitivity.v1" || ev.Note != "config/[REDACTED]/prod.env" || ev.Class != "env" || ev.Revision != "d=2;z=2" {
		t.Fatalf("question = %+v", q)
	}
	if strings.Contains(fmt.Sprintf("%+v", q), "hunter2") || strings.Contains(fmt.Sprintf("%+v", q), "xxxx") {
		t.Fatalf("the question carried a secret or file content: %+v", q)
	}
	// The same file at the same revision is not asked about again; a new revision is.
	if got, err := advisor.Sensitivity(context.Background(), c); err != nil || got != SensitivitySensitive || len(p.questions) != 1 {
		t.Fatalf("%v %v after %d questions", got, err, len(p.questions))
	}
	c.Revision = "r2"
	if _, err := advisor.Sensitivity(context.Background(), c); err != nil || len(p.questions) != 2 {
		t.Fatalf("%v after %d questions", err, len(p.questions))
	}
	// A long path is shortened from the front.
	long := repoChunk("b-file", SensitivityInternal)
	long.Ref = strings.Repeat("d/", 150) + "secrets.yaml"
	advisor.Sensitivity(context.Background(), long)
	if note := p.questions[2].Evidence[0].Note; len(note) != maxPathNote || !strings.HasSuffix(note, "secrets.yaml") {
		t.Fatalf("note = %q", note)
	}
}

func TestTheVerdictCacheIsBoundedAndFailuresAreNotRemembered(t *testing.T) {
	p := &positional{positions: []int{0}, conf: 0.9}
	advisor := NewServiceSensitivityAdvisor(decisionService(t, p, func(c *harnessdecide.Config) {
		c.MaxClass = harnessdecide.ClassInternal
		c.RunBudget = 10000
		c.PerMinute = 10000
	}), nil)
	for i := 0; i < sensitivityCache+20; i++ {
		c := repoChunk("x", SensitivityInternal)
		c.Revision = fmt.Sprintf("r%d", i)
		if got, err := advisor.Sensitivity(context.Background(), c); err != nil || got != SensitivityPublic {
			t.Fatalf("%d: %v %v", i, got, err)
		}
	}
	if len(advisor.known) != sensitivityCache || len(advisor.order) != sensitivityCache {
		t.Fatalf("remembered %d and %d", len(advisor.known), len(advisor.order))
	}
	failing := NewServiceSensitivityAdvisor(decisionService(t, &positional{positions: []int{0}, conf: 0.9, outcome: harness.OutcomeDenied}, func(c *harnessdecide.Config) { c.MaxClass = harnessdecide.ClassInternal }), nil)
	if got, err := failing.Sensitivity(context.Background(), repoChunk("y", SensitivityInternal)); err == nil || got != SensitivityPublic || len(failing.known) != 0 {
		t.Fatalf("%v %v, %d remembered", got, err, len(failing.known))
	}
}

func TestAnOpaqueProviderIsNeverToldAFilesNameAndARequiredOneIsCautious(t *testing.T) {
	p := &positional{positions: []int{0}, conf: 0.9}
	opaque := NewServiceSensitivityAdvisor(decisionService(t, p), nil) // the default class is opaque
	if got, err := opaque.Sensitivity(context.Background(), repoChunk("a", SensitivityInternal)); err == nil || got != SensitivityPublic || len(p.questions) != 0 {
		t.Fatalf("%v %v after %d questions", got, err, len(p.questions))
	}
	required := NewServiceSensitivityAdvisor(decisionService(t, &positional{positions: []int{0}, conf: 0.9, outcome: harness.OutcomeUnavailable}, func(c *harnessdecide.Config) {
		c.MaxClass = harnessdecide.ClassInternal
		c.Required = []harnessdecide.Kind{harnessdecide.KindFileSensitivity}
	}), nil)
	c := repoChunk("b", SensitivityInternal)
	if got, err := required.Sensitivity(context.Background(), c); err != nil || got != SensitivitySensitive || len(required.known) != 0 {
		t.Fatalf("%v %v, %d remembered", got, err, len(required.known))
	}
	// Through a run source the required advisor withholds the chunk from an ordinary provider.
	if got := includedIDs(t, sensitivitySource([]Chunk{c}, required)); has(got, "b") {
		t.Fatalf("a chunk the required advisor could not judge was shared: %v", got)
	}
	if _, err := (*ServiceSensitivityAdvisor)(nil).Sensitivity(context.Background(), c); err == nil {
		t.Fatal("a missing advisor judged a file")
	}
}
