package core

import (
	"strings"
	"testing"
	"time"
)

func TestSolutionEpisodeValidateAcceptsActiveEpisode(t *testing.T) {
	now := time.Date(2026, 8, 24, 6, 0, 0, 0, time.UTC)
	episode := SolutionEpisode{
		ID:             "episode-1",
		Workspace:      "agent-memory",
		SessionID:      "session-1",
		PrincipalID:    "principal-1",
		ClientID:       "codex",
		GoalSummary:    "Add structured solution-path persistence.",
		Status:         SolutionEpisodeActive,
		CapturePolicy:  SolutionCaptureStructured,
		RetentionClass: SolutionRetentionStandard,
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := episode.Validate(); err != nil {
		t.Fatalf("expected valid episode, got %v", err)
	}
}

func TestSolutionEpisodeValidateRejectsUnknownStatusAndBlankGoal(t *testing.T) {
	now := time.Now().UTC()
	episode := SolutionEpisode{
		ID: "episode-1", Workspace: "agent-memory", SessionID: "session-1",
		PrincipalID: "principal-1", ClientID: "codex", Status: SolutionEpisodeStatus("mystery"),
		CapturePolicy: SolutionCaptureStructured, RetentionClass: SolutionRetentionStandard,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}

	err := episode.Validate()
	if err == nil || !strings.Contains(err.Error(), "goal_summary") {
		t.Fatalf("expected blank goal validation error, got %v", err)
	}

	episode.GoalSummary = "Preserve how the task was solved."
	err = episode.Validate()
	if err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("expected status validation error, got %v", err)
	}
}

func TestSolutionEpisodeTerminalStates(t *testing.T) {
	terminal := []SolutionEpisodeStatus{
		SolutionEpisodeCompleted,
		SolutionEpisodePartial,
		SolutionEpisodeAbandoned,
		SolutionEpisodeCancelled,
	}
	for _, status := range terminal {
		if !status.Terminal() {
			t.Errorf("expected %q to be terminal", status)
		}
	}
	for _, status := range []SolutionEpisodeStatus{SolutionEpisodeActive, SolutionEpisodePaused} {
		if status.Terminal() {
			t.Errorf("expected %q to be non-terminal", status)
		}
	}
}

func TestSolutionStepValidateAcceptsEvidenceLinkedDecision(t *testing.T) {
	now := time.Date(2026, 8, 24, 6, 5, 0, 0, time.UTC)
	step := SolutionStep{
		ID:               "step-1",
		EpisodeID:        "episode-1",
		Ordinal:          1,
		Kind:             SolutionStepDecision,
		Status:           SolutionStepCompleted,
		Summary:          "Use additive tables instead of a fifth memory type.",
		RationaleSummary: "Episodes need ordered children and a separate expiry lifecycle.",
		Source:           "agent",
		Confidence:       0.9,
		Sensitivity:      SolutionSensitivityInternal,
		References: []SolutionReference{{
			Kind: SolutionReferenceMemory, TargetID: "memory-1",
		}},
		CreatedAt: now,
	}

	if err := step.Validate(); err != nil {
		t.Fatalf("expected valid step, got %v", err)
	}
}

func TestSolutionStepValidateRejectsInvalidOrderingAndReferences(t *testing.T) {
	now := time.Now().UTC()
	step := SolutionStep{
		ID: "step-1", EpisodeID: "episode-1", Kind: SolutionStepAction,
		Status: SolutionStepCompleted, Summary: "Run the focused test suite.",
		Source: "agent", Confidence: 0.8, Sensitivity: SolutionSensitivityInternal,
		CreatedAt: now,
	}

	err := step.Validate()
	if err == nil || !strings.Contains(err.Error(), "ordinal") {
		t.Fatalf("expected ordinal validation error, got %v", err)
	}

	step.Ordinal = 1
	step.References = []SolutionReference{{Kind: SolutionReferenceObservation}}
	err = step.Validate()
	if err == nil || !strings.Contains(err.Error(), "target_id") {
		t.Fatalf("expected reference target validation error, got %v", err)
	}
}

func TestWorkingStateValidateEnforcesGenerationAndExpiry(t *testing.T) {
	now := time.Date(2026, 8, 24, 6, 10, 0, 0, time.UTC)
	state := SolutionWorkingState{
		EpisodeID:     "episode-1",
		Workspace:     "agent-memory",
		SessionID:     "session-1",
		PrincipalID:   "principal-1",
		GoalSummary:   "Implement solution-path persistence.",
		Constraints:   []string{"Keep existing memory types unchanged."},
		PlanItems:     []SolutionPlanItem{{ID: "plan-1", Summary: "Define contracts", Status: SolutionPlanCompleted}},
		OpenQuestions: []string{"Which retention default should ship?"},
		NextAction:    "Add storage tests.",
		Generation:    1,
		Sensitivity:   SolutionSensitivityInternal,
		UpdatedAt:     now,
		ExpiresAt:     now.Add(24 * time.Hour),
	}

	if err := state.Validate(); err != nil {
		t.Fatalf("expected valid working state, got %v", err)
	}

	state.ExpiresAt = now
	err := state.Validate()
	if err == nil || !strings.Contains(err.Error(), "expires_at") {
		t.Fatalf("expected expiry validation error, got %v", err)
	}

	state.ExpiresAt = now.Add(time.Hour)
	state.Generation = 0
	err = state.Validate()
	if err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("expected generation validation error, got %v", err)
	}
}

func TestSolutionContractsRejectOversizedSummaries(t *testing.T) {
	now := time.Now().UTC()
	episode := SolutionEpisode{
		ID: "episode-1", Workspace: "agent-memory", SessionID: "session-1",
		PrincipalID: "principal-1", ClientID: "codex", GoalSummary: strings.Repeat("x", MaxSolutionGoalSummaryBytes+1),
		Status: SolutionEpisodeActive, CapturePolicy: SolutionCaptureStructured,
		RetentionClass: SolutionRetentionStandard, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := episode.Validate(); err == nil || !strings.Contains(err.Error(), "goal_summary") {
		t.Fatalf("expected oversized goal rejection, got %v", err)
	}

	step := SolutionStep{
		ID: "step-1", EpisodeID: "episode-1", Ordinal: 1, Kind: SolutionStepAction,
		Status: SolutionStepCompleted, Summary: strings.Repeat("x", MaxSolutionStepSummaryBytes+1),
		Source: "agent", Confidence: 0.5, Sensitivity: SolutionSensitivityInternal, CreatedAt: now,
	}
	if err := step.Validate(); err == nil || !strings.Contains(err.Error(), "summary") {
		t.Fatalf("expected oversized step rejection, got %v", err)
	}
}
