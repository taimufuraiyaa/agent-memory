package harnesstest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// ModelSpec describes how to exercise one model provider family.
type ModelSpec struct {
	Capability harness.CapabilityID
	// Prompt is a valid prompt the provider will answer.
	Prompt string
}

// ModelConformance checks the contract every model provider must meet, whatever it talks
// to: a live capability, validated bounded answers that respect the reply envelope,
// prompt cancellation, and disposal that makes later use stale. The same checks run
// against fake families and the real provider, so a provider cannot pass by being
// unusually friendly to the harness.
func ModelConformance(t *testing.T, open func(t *testing.T) *harness.Session, spec ModelSpec) {
	t.Helper()
	request := func(t *testing.T, s *harness.Session, maxBytes int) harness.ModelRequest {
		t.Helper()
		envelope, err := s.Envelope(spec.Capability, maxBytes)
		if err != nil {
			t.Fatal(err)
		}
		return harness.ModelRequest{Envelope: envelope, MaxOutputTokens: 64, Prompt: spec.Prompt, PrefixID: strings.Repeat("a", 32)}
	}

	t.Run("capability is live", func(t *testing.T) {
		s := open(t)
		if s.State(spec.Capability) != harness.AccessAvailable {
			t.Fatalf("state = %s", s.State(spec.Capability))
		}
		if err := s.Access().Validate(s.Manifest(), s.Scope()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("answers within the contract", func(t *testing.T) {
		s := open(t)
		answer, err := s.Generate(context.Background(), request(t, s, 4096))
		if err != nil {
			t.Fatal(err)
		}
		if answer.Outcome != harness.OutcomeOK && answer.Outcome != harness.OutcomePartial {
			t.Fatalf("outcome = %s", answer.Outcome)
		}
		if strings.TrimSpace(answer.Text) == "" || len(answer.Text) > 4096 {
			t.Fatalf("text = %q", answer.Text)
		}
		if answer.Usage.InputTokens <= 0 || answer.Usage.OutputTokens < 0 || answer.Usage.CachedInputTokens > answer.Usage.InputTokens {
			t.Fatalf("usage = %+v", answer.Usage)
		}
		if answer.CostMicros < 0 || answer.CostMicros > harness.MaxCostMicros {
			t.Fatalf("cost = %d", answer.CostMicros)
		}
		if answer.ToolID != "" || len(answer.ToolArguments) != 0 {
			t.Fatalf("a text-only exchange produced a tool call: %+v", answer)
		}
	})

	t.Run("respects a small reply bound", func(t *testing.T) {
		s := open(t)
		answer, err := s.Generate(context.Background(), request(t, s, 8))
		if err != nil {
			t.Fatalf("a provider must fit its reply to the envelope, not violate the contract: %v", err)
		}
		if len(answer.Text) > 8 {
			t.Fatalf("text of %d bytes exceeds the 8-byte bound", len(answer.Text))
		}
	})

	t.Run("an empty prompt stays inside the contract", func(t *testing.T) {
		s := open(t)
		r := request(t, s, 4096)
		r.Prompt = ""
		answer, err := s.Generate(context.Background(), r)
		if err != nil {
			t.Fatalf("an empty prompt must yield a typed outcome, not a contract error: %v", err)
		}
		switch answer.Outcome {
		case harness.OutcomeOK, harness.OutcomePartial:
			// A provider that answers regardless must still return bounded, non-empty text.
			if strings.TrimSpace(answer.Text) == "" || len(answer.Text) > 4096 {
				t.Fatalf("an answer with no usable text: %+v", answer)
			}
		default:
			if answer.Text != "" || answer.ToolID != "" {
				t.Fatalf("a non-result outcome carried content: %+v", answer)
			}
		}
	})

	t.Run("cancellation is prompt and typed", func(t *testing.T) {
		s := open(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		started := time.Now()
		answer, err := s.Generate(ctx, request(t, s, 4096))
		if err != nil || answer.Outcome != harness.OutcomeCancelled || time.Since(started) > time.Second {
			t.Fatalf("answer=%+v err=%v after %v", answer, err, time.Since(started))
		}
	})

	t.Run("disposal makes later use stale and is idempotent", func(t *testing.T) {
		s := open(t)
		r := request(t, s, 4096)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("second close = %v", err)
		}
		if _, err := s.Generate(context.Background(), r); !errors.Is(err, harness.ErrStale) {
			t.Fatalf("use after close = %v", err)
		}
	})
}

// DecisionSpec describes how to exercise one decision provider family.
type DecisionSpec struct {
	Capability harness.CapabilityID
	// Kind is a decision kind the provider understands.
	Kind string
	// Candidates are valid candidates for it, each a single byte long so that a one-byte
	// reply bound can still hold an answer.
	Candidates []string
	Evidence   []harness.EvidenceRef
}

// DecisionConformance checks the contract every decision provider must meet, whatever it
// talks to: a live capability, advice that is a subset of the candidates it was given with a
// confidence in range, a typed outcome rather than a contract error when its reply bound is
// too small, prompt typed cancellation, and disposal that makes later use stale.
func DecisionConformance(t *testing.T, open func(t *testing.T) *harness.Session, spec DecisionSpec) {
	t.Helper()
	question := func(t *testing.T, s *harness.Session, maxBytes int) harness.DecisionQuestion {
		t.Helper()
		envelope, err := s.Envelope(spec.Capability, maxBytes)
		if err != nil {
			t.Fatal(err)
		}
		return harness.DecisionQuestion{Envelope: envelope, Kind: spec.Kind, Candidates: spec.Candidates, Evidence: spec.Evidence}
	}

	t.Run("capability is live", func(t *testing.T) {
		s := open(t)
		if s.State(spec.Capability) != harness.AccessAvailable {
			t.Fatalf("state = %s", s.State(spec.Capability))
		}
		if err := s.Access().Validate(s.Manifest(), s.Scope()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("advises within the contract", func(t *testing.T) {
		s := open(t)
		answer, err := s.Decide(context.Background(), question(t, s, 4096))
		if err != nil {
			t.Fatal(err)
		}
		if answer.Outcome != harness.OutcomeOK || len(answer.Selected) == 0 {
			t.Fatalf("answer = %+v", answer)
		}
		if answer.Confidence < 0 || answer.Confidence > 1 {
			t.Fatalf("confidence = %v", answer.Confidence)
		}
		offered := map[string]bool{}
		for _, c := range spec.Candidates {
			offered[c] = true
		}
		for _, selected := range answer.Selected {
			if !offered[selected] {
				t.Fatalf("selected %q, which was not offered", selected)
			}
		}
	})

	t.Run("respects a small reply bound", func(t *testing.T) {
		s := open(t)
		answer, err := s.Decide(context.Background(), question(t, s, 1))
		if err != nil {
			t.Fatalf("a provider must fit its reply to the envelope or fail with a typed outcome, not violate the contract: %v", err)
		}
		total := 0
		for _, selected := range answer.Selected {
			total += len(selected)
		}
		if total > 1 {
			t.Fatalf("a reply of %d bytes exceeds the 1-byte bound", total)
		}
	})

	t.Run("cancellation is prompt and typed", func(t *testing.T) {
		s := open(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		started := time.Now()
		answer, err := s.Decide(ctx, question(t, s, 4096))
		if err != nil || answer.Outcome != harness.OutcomeCancelled || time.Since(started) > time.Second {
			t.Fatalf("answer=%+v err=%v after %v", answer, err, time.Since(started))
		}
	})

	t.Run("disposal makes later use stale and is idempotent", func(t *testing.T) {
		s := open(t)
		q := question(t, s, 4096)
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("second close = %v", err)
		}
		if _, err := s.Decide(context.Background(), q); !errors.Is(err, harness.ErrStale) {
			t.Fatalf("use after close = %v", err)
		}
	})
}
