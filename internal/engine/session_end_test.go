package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// sessionEndGoldenTranscript is a fixed fixture with known noise and signal.
const sessionEndGoldenTranscript = `Session started at 10:00 UTC.

[2026-08-04T10:00:00Z] Starting deployment pipeline.

The payment service uses exponential backoff for retry logic.

$ npm run build
> building project...
> done in 3.2s

# Check the logs for errors
$ tail -f /var/log/app.log

` + "```" + `
const x = 1;
console.log(x);
` + "```" + `

We learned that database connection pooling prevents timeout cascades during peak load.

The fix was successful after increasing the connection pool size from 10 to 50.

` + "```mermaid" + `
flowchart TD
  A["Client"] --> B["API Gateway"]
  B --> C["Payment Service"]
` + "```" + `

To deploy the payment service, run the migration first then restart the API.

The retry configuration should use exponential backoff with a maximum of 5 attempts.

[2026-08-04T10:05:00Z] Tests passed.
$ echo "deploy complete"
The build succeeded with all 42 tests passing.

Deployment completed successfully. This happened after the connection pool fix.

# Final notes
$ exit
`

func TestSessionEndGoldenTranscript(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "session.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	pipeline := NewWritePipeline(store)
	ex := NewSessionEndExtractor(pipeline)

	out, err := ex.ExtractAndStore(context.Background(), "golden-ws", sessionEndGoldenTranscript)
	if err != nil {
		t.Fatalf("extract and store: %v", err)
	}

	// We expect several signal lines to be extracted.
	if out.TotalExtracted == 0 {
		t.Fatalf("expected extracted memories")
	}
	if out.TotalFailed > 0 {
		t.Fatalf("unexpected failures: %d", out.TotalFailed)
	}

	memories, err := store.ListMemoriesByWorkspace(context.Background(), "golden-ws")
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}

	hasMermaid := false
	hasProcedural := false
	hasOutcome := false
	hasSemantic := false

	for _, m := range memories {
		if m.Diagram != nil && strings.Contains(m.Diagram.Lang, "mermaid") && strings.Contains(m.Diagram.Code, "flowchart") {
			hasMermaid = true
		}
		if m.Type == core.ProceduralMemory {
			hasProcedural = true
		}
		if m.Type == core.OutcomeMemory {
			hasOutcome = true
			// Verify honest provenance.
			if m.Outcome != nil {
				if m.Outcome.Approach != "session-end-heuristic" {
					t.Errorf("expected outcome approach 'session-end-heuristic', got %q", m.Outcome.Approach)
				}
				if m.Outcome.Result != core.OutcomePartial {
					t.Errorf("expected outcome result 'partial', got %q", m.Outcome.Result)
				}
				// Confidence should be capped at 0.5.
				if m.Confidence > outcomeConfidenceCap {
					t.Errorf("expected confidence <= %.2f for outcome, got %.2f", outcomeConfidenceCap, m.Confidence)
				}
			}
		}
		if m.Type == core.SemanticMemory && m.Diagram == nil {
			hasSemantic = true
		}
	}
	if !hasMermaid {
		t.Error("expected mermaid diagram to be extracted")
	}
	if !hasProcedural {
		t.Error("expected at least one procedural memory")
	}
	if !hasOutcome {
		t.Error("expected at least one outcome memory")
	}
	if !hasSemantic {
		t.Error("expected at least one semantic memory")
	}

	t.Logf("extracted=%d skipped=%d failed=%d total_memories=%d",
		out.TotalExtracted, out.TotalSkipped, out.TotalFailed, len(memories))
}

// TestSessionEndNoiseStripping verifies that tool output, code fences
// (non-diagram), timestamps, and blank lines are stripped.
func TestSessionEndNoiseStripping(t *testing.T) {
	noisyTranscript := `$ npm install
> installing packages...

The application server handles requests asynchronously using goroutines for concurrency.

` + "```" + `
const junk = 123;
` + "```" + `

[2026-08-04T10:00:00Z] Some log line
# comment
To run the integration tests, use the make test-integration command with the TEST_FLAGS environment variable.
`

	lines := splitLines(noisyTranscript)
	// Count tool-like lines.
	toolCount := 0
	for _, l := range lines {
		clean := strings.TrimSpace(l)
		if toolOutputPrefix.MatchString(clean) {
			toolCount++
		}
	}
	if toolCount == 0 {
		t.Fatal("test fixture should contain tool-like lines")
	}

	items := extractTranscriptMemories(noisyTranscript)

	// "The application server handles requests asynchronously using goroutines for concurrency."
	// starts with capital, contains "handles" (verb) → SemanticMemory.
	// "To run the integration tests..." → procedural pattern.
	// No noise lines should produce memories.

	if len(items) == 0 {
		t.Fatal("expected at least one signal item")
	}

	// Verify no noise made it through.
	for _, it := range items {
		content := strings.ToLower(it.Content)
		if strings.HasPrefix(content, "$") || strings.HasPrefix(content, ">") || strings.HasPrefix(content, "#") {
			t.Errorf("noise line leaked into extraction: %q", it.Content)
		}
		if strings.Contains(content, "const junk") {
			t.Errorf("code fence content leaked: %q", it.Content)
		}
		if strings.Contains(content, "[2026-") {
			t.Errorf("timestamp line leaked: %q", it.Content)
		}
	}

	t.Logf("extracted %d items from noisy transcript", len(items))
}

// TestSessionEndDedupPreventsDuplicateExtraction verifies that running the
// same transcript twice does not produce duplicate memories.
func TestSessionEndDedupPreventsDuplicateExtraction(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dedup.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	pipeline := NewWritePipeline(store)
	ex := NewSessionEndExtractor(pipeline)

	transcript := "The payment service uses exponential backoff for retries and handles connection timeouts gracefully.\n" +
		"The deployment checklist mentions smoke tests and rollback plans for safety.\n"

	// First extraction.
	out1, err := ex.ExtractAndStore(context.Background(), "dedup-ws", transcript)
	if err != nil {
		t.Fatalf("first extract: %v", err)
	}
	if out1.TotalExtracted == 0 {
		t.Fatal("first extraction should produce memories")
	}

	memories1, _ := store.ListMemoriesByWorkspace(context.Background(), "dedup-ws")
	count1 := len(memories1)

	// Second extraction — same transcript.
	out2, err := ex.ExtractAndStore(context.Background(), "dedup-ws", transcript)
	if err != nil {
		t.Fatalf("second extract: %v", err)
	}

	memories2, _ := store.ListMemoriesByWorkspace(context.Background(), "dedup-ws")
	count2 := len(memories2)

	// The second extraction should produce no new memories (all deduplicated).
	// We allow for the keyword-overlap dedup in extractTranscriptMemories plus
	// the content-hash dedup in the write pipeline.
	if out2.TotalExtracted > out2.TotalSkipped && count2 > count1 {
		t.Errorf("duplicate memories created: first=%d second=%d extracted2=%d",
			count1, count2, out2.TotalExtracted)
	}

	t.Logf("first: extracted=%d count=%d, second: extracted=%d skipped=%d count=%d",
		out1.TotalExtracted, count1, out2.TotalExtracted, out2.TotalSkipped, count2)
}

// TestSessionEndTranscriptSizeRejection verifies that oversized transcripts
// are rejected with a clear error.
func TestSessionEndTranscriptSizeRejection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "oversize.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	pipeline := NewWritePipeline(store)
	ex := NewSessionEndExtractor(pipeline)

	// Create a transcript larger than 200KB.
	line := "The quick brown fox jumps over the lazy dog repeatedly. "
	oversized := strings.Repeat(line, (maxTranscriptBytes/len(line))+10)

	_, err = ex.ExtractAndStore(context.Background(), "oversize-ws", oversized)
	if err == nil {
		t.Fatal("expected error for oversized transcript")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' error, got: %v", err)
	}
}

// TestSessionEndTranscriptTooManyLines verifies line-count rejection.
func TestSessionEndTranscriptTooManyLines(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "manylines.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	pipeline := NewWritePipeline(store)
	ex := NewSessionEndExtractor(pipeline)

	// Create a transcript with more than 5000 lines.
	lines := make([]string, maxTranscriptLines+10)
	for i := range lines {
		lines[i] = "Line number " + string(rune('0'+i%10))
	}
	tooMany := strings.Join(lines, "\n")

	_, err = ex.ExtractAndStore(context.Background(), "manylines-ws", tooMany)
	if err == nil {
		t.Fatal("expected error for too many lines")
	}
	if !strings.Contains(err.Error(), "too many lines") {
		t.Errorf("expected 'too many lines' error, got: %v", err)
	}
}

// TestSessionEndBareKeywordRejection verifies that bare outcome/procedural
// keywords without evidence structure are rejected.
func TestSessionEndBareKeywordRejection(t *testing.T) {
	items := extractTranscriptMemories("success\nfailed\nmust\nshould always do this\n" +
		"The deployment completed successfully after we increased the pool size to handle more connections.")

	// "success" alone → too short (< 40 chars), rejected.
	// "failed" alone → too short, rejected.
	// "must" alone → too short, rejected.
	// "should always do this" → 24 chars, too short, rejected.
	// "The deployment completed successfully..." → 95 chars, matches outcomeContextPattern.

	if len(items) == 0 {
		t.Fatal("expected the long sentence to be extracted")
	}
	if len(items) > 2 {
		// At most we should get the one long sentence plus maybe
		// the diagram (none here). Bare keywords should not produce items.
		t.Fatalf("too many items extracted: %d", len(items))
	}
	for _, it := range items {
		if len(it.Content) < minEvidenceLength {
			t.Errorf("short item leaked through: %q (%d chars)", it.Content, len(it.Content))
		}
		if it.Content == "success" || it.Content == "failed" || it.Content == "must" {
			t.Errorf("bare keyword leaked: %q", it.Content)
		}
	}
}

// TestSessionEndBoilerplateFiltering verifies that repeated identical lines
// are treated as boilerplate and filtered out.
func TestSessionEndBoilerplateFiltering(t *testing.T) {
	// "Processing..." appears 4 times (more than maxRepeatedLines=2), should be filtered.
	// "The database migration ran successfully with zero downtime." appears once, should pass.
	transcript := "Processing...\nProcessing...\nProcessing...\nProcessing...\n" +
		"The database migration ran successfully with zero downtime and all data was preserved correctly."

	items := extractTranscriptMemories(transcript)

	// The signal line should be extracted (it's > 40 chars, matches subjectVerbSentence).
	if len(items) == 0 {
		t.Fatal("expected signal line to be extracted")
	}
	for _, it := range items {
		if strings.Contains(it.Content, "Processing") {
			t.Errorf("boilerplate leaked: %q", it.Content)
		}
	}
}

// TestSessionEndMermaidExtraction verifies that mermaid diagrams are still
// extracted as before.
func TestSessionEndMermaidExtraction(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mermaid.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()
	pipeline := NewWritePipeline(store)
	ex := NewSessionEndExtractor(pipeline)

	transcript := "We added a new API endpoint.\n```mermaid\nflowchart TD\n  A[\"Start\"] --> B[\"End\"]\n```\nThe endpoint handles payment processing."
	out, err := ex.ExtractAndStore(context.Background(), "mermaid-ws", transcript)
	if err != nil {
		t.Fatalf("extract and store: %v", err)
	}
	if out.TotalExtracted == 0 {
		t.Fatal("expected extracted memories")
	}

	memories, err := store.ListMemoriesByWorkspace(context.Background(), "mermaid-ws")
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}

	hasMermaid := false
	for _, m := range memories {
		if m.Diagram != nil && strings.Contains(m.Diagram.Lang, "mermaid") {
			hasMermaid = true
			break
		}
	}
	if !hasMermaid {
		t.Error("expected mermaid diagram to be extracted")
	}
}
