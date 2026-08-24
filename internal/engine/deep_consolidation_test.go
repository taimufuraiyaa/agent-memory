package engine

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// TestDeepConsolidationPass2DedupsIdenticalRerun runs pass 2 twice over the
// same failure cluster and asserts the second run dedups against the existing
// rule instead of inserting a duplicate.
func TestDeepConsolidationPass2DedupsIdenticalRerun(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStore(t)
	t.Cleanup(func() { _ = store.Close() })
	pipe := NewWritePipeline(store)

	seedFailureOutcomes(t, ctx, pipe, "ws",
		failureSeed{content: "attempt 0: direct SQL string concatenation", reason: "lock timeout occurred", session: sessionID(0)},
		failureSeed{content: "attempt 1: direct SQL string concatenation", reason: "lock timeout occurred", session: sessionID(1)},
		failureSeed{content: "attempt 2: direct SQL string concatenation", reason: "connection reset", session: sessionID(2)},
	)

	dc := NewDeepConsolidationEngine(store, pipe)
	res, err := dc.Run(ctx, DeepConsolidationOptions{Workspace: "ws", DaysBack: 30})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if res.ProceduralPromoted != 1 {
		t.Fatalf("expected 1 procedural promotion, got %d", res.ProceduralPromoted)
	}
	rules := proceduralRules(t, store, ctx, "ws")
	if len(rules) != 1 {
		t.Fatalf("expected exactly 1 rule after first run, got %d", len(rules))
	}

	// Re-run on the identical cluster: content-hash dedup must keep a single rule.
	res, err = dc.Run(ctx, DeepConsolidationOptions{Workspace: "ws", DaysBack: 30})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res.ProceduralPromoted != 1 {
		t.Fatalf("expected 1 procedural promotion on rerun, got %d", res.ProceduralPromoted)
	}
	rules = proceduralRules(t, store, ctx, "ws")
	if len(rules) != 1 {
		t.Fatalf("expected exactly 1 rule after identical rerun, got %d", len(rules))
	}
	if rules[0].SupersededBy != nil {
		t.Fatalf("expected rule to stay active after identical rerun, got superseded_by=%q", *rules[0].SupersededBy)
	}
}

// TestDeepConsolidationPass2SupersedesInsteadOfDuplicating verifies that when a
// later run re-promotes the same failure pattern with changed evidence (a new
// session id in the reason string and a higher failure count), the prior rule
// is superseded by the refreshed one rather than duplicated.
func TestDeepConsolidationPass2SupersedesInsteadOfDuplicating(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStore(t)
	t.Cleanup(func() { _ = store.Close() })
	pipe := NewWritePipeline(store)

	// Run 1: three failures, one approach; the reason embeds a session id.
	for i := 0; i < 3; i++ {
		seedFailureOutcomes(t, ctx, pipe, "ws", failureSeed{
			content: fmt.Sprintf("attempt %d: direct SQL string concatenation", i),
			reason:  "SQL injection vulnerability in session 0000000a",
			session: sessionID(i),
		})
	}
	dc := NewDeepConsolidationEngine(store, pipe)
	res, err := dc.Run(ctx, DeepConsolidationOptions{Workspace: "ws", DaysBack: 30})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if res.ProceduralPromoted != 1 {
		t.Fatalf("expected 1 procedural promotion, got %d", res.ProceduralPromoted)
	}

	// Run 2: more evidence for the same pattern; the reason string now mentions
	// a different session id, so the raw text differs but the normalized
	// essence matches the existing rule.
	for i := 3; i < 6; i++ {
		seedFailureOutcomes(t, ctx, pipe, "ws", failureSeed{
			content: fmt.Sprintf("attempt %d: direct SQL string concatenation", i),
			reason:  "SQL injection vulnerability in session 0000000b",
			session: sessionID(i),
		})
	}
	res, err = dc.Run(ctx, DeepConsolidationOptions{Workspace: "ws", DaysBack: 30})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if res.ProceduralPromoted != 1 {
		t.Fatalf("expected 1 procedural promotion on refresh, got %d", res.ProceduralPromoted)
	}

	rules := proceduralRules(t, store, ctx, "ws")
	if len(rules) != 2 {
		t.Fatalf("expected exactly 2 procedural rows (1 superseded + 1 active), got %d", len(rules))
	}
	var active, superseded *core.MemoryEntry
	for i := range rules {
		if rules[i].SupersededBy != nil {
			superseded = &rules[i]
		} else {
			active = &rules[i]
		}
	}
	if active == nil || superseded == nil {
		t.Fatalf("expected one active and one superseded rule, got %d rows", len(rules))
	}
	if got := *superseded.SupersededBy; got != active.ID {
		t.Fatalf("expected superseded rule to point at the refreshed rule, got %q want %q", got, active.ID)
	}
	if !strings.Contains(active.Content, "failed 6 times") {
		t.Fatalf("expected refreshed rule to record the new evidence (6 failures), got %q", active.Content)
	}
}

// TestDeepConsolidationPass1RequiresCrossSessionCluster verifies pass 1 only
// merges episodic clusters that span more than one session.
func TestDeepConsolidationPass1RequiresCrossSessionCluster(t *testing.T) {
	ctx := context.Background()
	store := mustOpenStore(t)
	t.Cleanup(func() { _ = store.Close() })
	pipe := NewWritePipeline(store)

	// A global attempt counter keeps every write's content unique so content-hash
	// dedup never collapses distinct observations within the test.
	attempt := 0
	writeEpisodics := func(ws, session string, n int) {
		for k := 0; k < n; k++ {
			_, err := pipe.Write(ctx, WriteInput{
				Workspace: ws,
				Type:      core.EpisodicMemory,
				Content:   fmt.Sprintf("orders pipeline failed during peak load testing on staging cluster attempt %d", attempt),
				Source:    core.MemorySource{Type: core.SourceAgentObservation, SessionID: session},
			})
			if err != nil {
				t.Fatalf("write episodic %d in %s: %v", k, ws, err)
			}
			attempt++
		}
	}

	dc := NewDeepConsolidationEngine(store, pipe)

	// Five overlapping episodic memories from a single session must not merge.
	writeEpisodics("ws-single", "session-a", 5)
	res, err := dc.Run(ctx, DeepConsolidationOptions{Workspace: "ws-single", DaysBack: 30})
	if err != nil {
		t.Fatalf("single-session run: %v", err)
	}
	if res.MemoriesMerged != 0 {
		t.Fatalf("single-session cluster must not merge, got %d merged", res.MemoriesMerged)
	}
	if res.SessionsScanned != 1 {
		t.Fatalf("expected 1 scanned session, got %d", res.SessionsScanned)
	}
	if n := countType(t, store, ctx, "ws-single", core.EpisodicMemory); n != 5 {
		t.Fatalf("expected 5 active episodic memories, got %d", n)
	}
	if n := countType(t, store, ctx, "ws-single", core.SemanticMemory); n != 0 {
		t.Fatalf("expected no semantic merge for single-session cluster, got %d", n)
	}

	// Five overlapping episodic memories spread across two sessions must merge.
	writeEpisodics("ws-cross", "session-b", 3)
	writeEpisodics("ws-cross", "session-c", 2)
	res, err = dc.Run(ctx, DeepConsolidationOptions{Workspace: "ws-cross", DaysBack: 30})
	if err != nil {
		t.Fatalf("cross-session run: %v", err)
	}
	if res.MemoriesMerged != 5 {
		t.Fatalf("expected 5 memories merged across sessions, got %d", res.MemoriesMerged)
	}
	if res.SessionsScanned != 2 {
		t.Fatalf("expected 2 scanned sessions, got %d", res.SessionsScanned)
	}
	if n := countType(t, store, ctx, "ws-cross", core.EpisodicMemory); n != 0 {
		t.Fatalf("expected all merged episodics superseded, got %d active", n)
	}
	if n := countType(t, store, ctx, "ws-cross", core.SemanticMemory); n != 1 {
		t.Fatalf("expected exactly 1 semantic merge, got %d", n)
	}
}

// TestNormalizeRuleEssence locks the essence normalization contract: variance
// in timestamps, session ids, failure count, and whitespace must not change the
// essence, while distinct failure patterns must stay distinct.
func TestNormalizeRuleEssence(t *testing.T) {
	equal := []struct {
		name string
		a, b string
	}{
		{
			"failure count variance",
			"Avoid approach: X. Reason: R (failed 3 times across sessions)",
			"Avoid approach: X. Reason: R (failed 47 times across sessions)",
		},
		{
			"timestamp variance",
			"Avoid approach: X. Reason: R at 2026-08-04T10:00:00Z",
			"Avoid approach: X. Reason: R at 2026-08-05T11:30:00+02:00",
		},
		{
			"bare date variance",
			"Avoid approach: X. Reason: R on 2026-08-04",
			"Avoid approach: X. Reason: R on 2026-08-05",
		},
		{
			"session id variance",
			"Avoid approach: X. Reason: R found in session abc123",
			"Avoid approach: X. Reason: R found in session 0000000a",
		},
		{
			"whitespace variance",
			"Avoid approach:  X. Reason:   R",
			"Avoid approach: X. Reason: R",
		},
	}
	for _, tc := range equal {
		t.Run(tc.name, func(t *testing.T) {
			if ruleEssenceHash(tc.a) != ruleEssenceHash(tc.b) {
				t.Fatalf("expected equal essence hashes:\n a=%q\n b=%q", tc.a, tc.b)
			}
		})
	}

	a := "Avoid approach: X. Reason: lock timeout (failed 3 times across sessions)"
	b := "Avoid approach: X. Reason: connection reset (failed 3 times across sessions)"
	if ruleEssenceHash(a) == ruleEssenceHash(b) {
		t.Fatalf("expected distinct essences for distinct failure patterns")
	}
}

// --- helpers ---

type failureSeed struct {
	content string
	reason  string
	session string
}

func seedFailureOutcomes(t *testing.T, ctx context.Context, pipe *WritePipeline, ws string, seeds ...failureSeed) {
	t.Helper()
	for i, s := range seeds {
		_, err := pipe.Write(ctx, WriteInput{
			Workspace: ws,
			Type:      core.OutcomeMemory,
			Content:   s.content,
			Source:    core.MemorySource{Type: core.SourceAgentObservation, SessionID: s.session},
			Outcome: &core.Outcome{
				Result:   core.OutcomeFailure,
				Approach: "direct SQL string concatenation",
				Reason:   s.reason,
			},
		})
		if err != nil {
			t.Fatalf("seed failure %d: %v", i, err)
		}
	}
}

func proceduralRules(t *testing.T, store *sqlite.Store, ctx context.Context, ws string) []core.MemoryEntry {
	t.Helper()
	memories, err := store.ListMemoriesByWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	out := make([]core.MemoryEntry, 0)
	for _, m := range memories {
		if m.Type == core.ProceduralMemory {
			out = append(out, m)
		}
	}
	return out
}

func countType(t *testing.T, store *sqlite.Store, ctx context.Context, ws string, typ core.MemoryType) int {
	t.Helper()
	memories, err := store.ListMemoriesByWorkspace(ctx, ws)
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	n := 0
	for _, m := range memories {
		if m.Type == typ && m.SupersededBy == nil {
			n++
		}
	}
	return n
}
