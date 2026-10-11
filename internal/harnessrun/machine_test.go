package harnessrun

import (
	"strings"
	"testing"
	"time"
)

var allStates = []State{StateQueued, StateRunning, StateNeedsAttention, StateCancelling, StateCompleted, StatePartial, StateFailed, StateCancelled}

func TestTransitionTableIsExactlyTheDocumentedMachine(t *testing.T) {
	allowed := map[State][]State{
		StateQueued:         {StateRunning, StateCancelled, StateFailed},
		StateRunning:        {StateNeedsAttention, StateCancelling, StateCompleted, StatePartial, StateFailed, StateQueued},
		StateNeedsAttention: {StateQueued, StateCancelled, StateFailed},
		StateCancelling:     {StateCancelled, StateFailed},
	}
	for _, from := range allStates {
		for _, to := range allStates {
			want := false
			for _, candidate := range allowed[from] {
				want = want || candidate == to
			}
			if got := canTransition(from, to); got != want {
				t.Errorf("%s -> %s = %v, want %v", from, to, got, want)
			}
		}
		if from.Terminal() && len(allowed[from]) != 0 {
			t.Errorf("terminal %s has exits", from)
		}
	}
	for _, state := range []State{StateCompleted, StatePartial, StateFailed, StateCancelled} {
		if !state.Terminal() {
			t.Errorf("%s is not terminal", state)
		}
	}
	for _, state := range []State{StateQueued, StateRunning, StateNeedsAttention, StateCancelling} {
		if state.Terminal() {
			t.Errorf("%s is terminal", state)
		}
	}
	if State("bogus").valid() {
		t.Error("bogus state is valid")
	}
}

func TestMoveBumpsGenerationRecordsEventAndClearsAttention(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	run := Run{State: StateRunning, Generation: 3, Attention: &Attention{Kind: AttentionClarification}}
	if err := run.move(StateNeedsAttention, "clarification_needed", now); err != nil {
		t.Fatal(err)
	}
	if run.Generation != 4 || run.State != StateNeedsAttention || run.Attention == nil || run.Code != "clarification_needed" {
		t.Fatalf("run = %+v", run)
	}
	if err := run.move(StateQueued, "resumed", now); err != nil {
		t.Fatal(err)
	}
	if run.Attention != nil || run.Generation != 5 {
		t.Fatalf("attention survived leaving needs-attention: %+v", run)
	}
	if len(run.Events) != 2 || run.Events[0].Seq != 1 || run.Events[1].Seq != 2 || run.Events[1].State != StateQueued {
		t.Fatalf("events = %+v", run.Events)
	}
	before := run.Generation
	if err := run.move(StateCompleted, "completed", now); err == nil || run.State != StateQueued || run.Generation != before {
		t.Fatalf("illegal move changed the run: %v %+v", err, run)
	}
	if err := run.move(StateRunning, "Bad Code!", now); err == nil || run.State != StateQueued {
		t.Fatalf("bad reason code accepted: %v", err)
	}
}

func TestEventRingKeepsNewestAndSequencesNeverRepeat(t *testing.T) {
	run := Run{State: StateRunning}
	now := time.Now()
	for i := 0; i < MaxEvents+40; i++ {
		run.addEvent("turn", "model_reply", now)
	}
	if len(run.Events) != MaxEvents || run.Events[0].Seq != 41 || run.Events[MaxEvents-1].Seq != uint64(MaxEvents+40) {
		t.Fatalf("ring = first %d last %d len %d", run.Events[0].Seq, run.Events[len(run.Events)-1].Seq, len(run.Events))
	}
}

func TestChunksKeepTheGoalAndTheNewestOthers(t *testing.T) {
	run := Run{NextSeq: 1, Chunks: []Chunk{{ID: "goal", Kind: "goal", Text: "g"}}}
	for i := 0; i < MaxChunks+10; i++ {
		run.addChunk("tool_result", strings.Repeat("x", 5), i)
		run.NextSeq++
	}
	goals, others := 0, 0
	for _, c := range run.Chunks {
		if c.Kind == "goal" {
			goals++
		} else {
			others++
		}
	}
	if goals != 1 || others != MaxChunks || run.Chunks[len(run.Chunks)-1].Turn != MaxChunks+9 {
		t.Fatalf("goals=%d others=%d last=%+v", goals, others, run.Chunks[len(run.Chunks)-1])
	}
	big := run.addChunk("tool_result", strings.Repeat("y", MaxChunkBytes*3), 0)
	if len(big.Text) != MaxChunkBytes {
		t.Fatalf("chunk was not bounded: %d", len(big.Text))
	}
}

func TestArtifactsAreBoundedAndHashed(t *testing.T) {
	run := Run{NextSeq: 1}
	for i := 0; i < MaxArtifacts+3; i++ {
		run.addArtifact("result", strings.Repeat("a", MaxArtifactBytes*2), i, digestOf)
		run.NextSeq++
	}
	if len(run.Artifacts) != MaxArtifacts || run.Artifacts[0].Bytes != MaxArtifactBytes || len(run.Artifacts[0].SHA256) != 64 {
		t.Fatalf("artifacts = %d first=%+v", len(run.Artifacts), run.Artifacts[0])
	}
	if status := run.status(); len(status.Artifacts) != MaxArtifacts {
		t.Fatalf("status artifacts = %d", len(status.Artifacts))
	}
}

func TestIdempotencyRecordsAreBounded(t *testing.T) {
	run := Run{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < MaxIdempotency+5; i++ {
		run.remember(strings.Repeat("k", 7)+string(rune('a'+i%26))+strings.Repeat("z", i), idemRecord{Operation: "cancel", At: base.Add(time.Duration(i) * time.Second)})
	}
	if len(run.Idem) != MaxIdempotency {
		t.Fatalf("records = %d", len(run.Idem))
	}
	oldest := strings.Repeat("k", 7) + "a"
	if _, kept := run.Idem[oldest]; kept {
		t.Fatal("the oldest record survived eviction")
	}
}

func TestBudgetNormalizeAppliesDefaultsAndEnforcesTheCeiling(t *testing.T) {
	got, err := Budget{}.Normalize()
	if err != nil || got != DefaultBudget {
		t.Fatalf("defaults = %+v, %v", got, err)
	}
	tooBig := []Budget{
		{MaxTurns: CeilingBudget.MaxTurns + 1}, {MaxActive: CeilingBudget.MaxActive + time.Second},
		{MaxToolCalls: CeilingBudget.MaxToolCalls + 1}, {MaxOutputBytes: CeilingBudget.MaxOutputBytes + 1},
		{MaxSpendMicros: CeilingBudget.MaxSpendMicros + 1}, {MaxDepth: CeilingBudget.MaxDepth + 1},
		{MaxTurns: -1}, {MaxActive: -time.Second}, {MaxSpendMicros: -1}, {MaxDepth: -1}, {MaxToolCalls: -1}, {MaxOutputBytes: -1},
	}
	for _, b := range tooBig {
		if _, err := b.Normalize(); err == nil {
			t.Errorf("accepted %+v", b)
		}
	}
	if _, err := CeilingBudget.Normalize(); err != nil {
		t.Fatalf("the ceiling itself must be valid: %v", err)
	}
}

func TestSanitizeStripsControlsAndCutsOnARuneBoundary(t *testing.T) {
	if got := sanitize("a\x00b\x1b[31mc\nd\te", 100); got != "ab[31mc\nd\te" {
		t.Fatalf("controls = %q", got)
	}
	if got := sanitize("héllo", 2); got != "h" {
		t.Fatalf("rune boundary = %q", got)
	}
	if got := sanitize("ok\xff\xfeok", 100); got != "okok" {
		t.Fatalf("invalid UTF-8 = %q", got)
	}
	if got := sanitize(strings.Repeat("é", 10), 7); len(got) != 6 {
		t.Fatalf("bound = %d bytes", len(got))
	}
}

func TestRunValidationRejectsDamagedRecords(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	good := func() Run {
		budget, _ := Budget{}.Normalize()
		return Run{SchemaVersion: 1, ID: "run_" + strings.Repeat("a", 32), State: StateQueued, Generation: 1, Budget: budget, NextSeq: 2,
			Owner:  Owner{ClientID: "claude-desktop", Workspace: "agent-memory", GrantID: strings.Repeat("b", 32), GrantRevision: 1},
			Events: []Event{{Seq: 1, At: now, Kind: "state", State: StateQueued, Code: "queued"}}, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
	}
	if err := good().validate(); err != nil {
		t.Fatalf("good record rejected: %v", err)
	}
	for name, mutate := range map[string]func(*Run){
		"schema":               func(r *Run) { r.SchemaVersion = 2 },
		"id":                   func(r *Run) { r.ID = "../../etc/passwd" },
		"state":                func(r *Run) { r.State = "bogus" },
		"generation":           func(r *Run) { r.Generation = 0 },
		"owner":                func(r *Run) { r.Owner.ClientID = "Bad" },
		"budget over ceiling":  func(r *Run) { r.Budget.MaxTurns = 10_000 },
		"negative usage":       func(r *Run) { r.Usage.Turns = -1 },
		"event order":          func(r *Run) { r.Events = append(r.Events, Event{Seq: 1, State: StateQueued}) },
		"event code":           func(r *Run) { r.Events[0].Code = "Not A Code" },
		"sequence behind":      func(r *Run) { r.NextSeq = 1 },
		"oversized chunk":      func(r *Run) { r.Chunks = []Chunk{{Text: strings.Repeat("x", MaxChunkBytes+1)}} },
		"attention mismatch":   func(r *Run) { r.Attention = &Attention{Kind: AttentionClarification} },
		"attention kind":       func(r *Run) { r.State = StateNeedsAttention; r.Attention = &Attention{Kind: "root"} },
		"too many events":      func(r *Run) { r.Events = make([]Event, MaxEvents+1) },
		"zero expiry":          func(r *Run) { r.ExpiresAt = time.Time{} },
		"needs attention bare": func(r *Run) { r.State = StateNeedsAttention },
	} {
		r := good()
		mutate(&r)
		if r.validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
