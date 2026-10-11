package application

import (
	"errors"
	"testing"
	"time"

	graphretrieval "github.com/taimufuraiyaa/agent-memory/internal/retrieval"
)

func TestGraphRecallObservationRecordsAttemptedFallbackRouteWithoutContent(t *testing.T) {
	decision := graphretrieval.GraphRouteDecision{RequestedMode: graphretrieval.GraphQueryAuto, SelectedMode: graphretrieval.GraphQueryBasic, Intent: graphretrieval.GraphIntentGlobal, ReasonCode: graphretrieval.GraphReasonIndexStale, Fallback: true}
	observation := graphRecallObservation(time.Now().Add(-time.Millisecond), true, decision, &RecallResult{GraphRoute: decision})
	if observation.Mode != "basic" || observation.Route != "global" || observation.Outcome != "fallback" || observation.Reason != "index_stale" || !observation.Fallback {
		t.Fatalf("unexpected graph query observation: %+v", observation)
	}
}

func TestGraphRouteRecallContractDefaultsToBasicAndPreservesFallback(t *testing.T) {
	decision, err := ResolveRecallGraphRoute(RecallOptions{Task: "What patterns occur across all memories?"})
	if err != nil || decision.SelectedMode != graphretrieval.GraphQueryBasic || decision.Fallback {
		t.Fatalf("default recall route=%+v err=%v", decision, err)
	}

	decision, err = ResolveRecallGraphRoute(RecallOptions{
		Task: "How are A and B related?", GraphMode: graphretrieval.GraphQueryLocal,
		GraphPolicy: graphretrieval.GraphRoutePolicy{GraphEnabled: true, AllowLocal: true},
	})
	if err != nil || decision.SelectedMode != graphretrieval.GraphQueryBasic || !decision.Fallback || decision.ReasonCode != graphretrieval.GraphReasonIndexUnavailable {
		t.Fatalf("recall fallback route=%+v err=%v", decision, err)
	}
}

func TestGraphFallbackRecallRequiredFailsBeforeBasicRetrieval(t *testing.T) {
	_, err := ResolveRecallGraphRoute(RecallOptions{
		Task: "How are A and B related?", GraphMode: graphretrieval.GraphQueryLocal, GraphRequired: true,
		GraphPolicy: graphretrieval.GraphRoutePolicy{GraphEnabled: true, AllowLocal: true},
	})
	if !errors.Is(err, graphretrieval.ErrGraphRouteRequired) {
		t.Fatalf("expected required graph route error, got %v", err)
	}
}
