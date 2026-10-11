package application

import (
	"context"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillDomainLoopAutomaticallyRoutesEvaluationDecisionCanaryAndPromotion(t *testing.T) {
	router := &domainLoopRouter{seen: map[string]struct{}{}}

	evaluation := newEvaluationAdapterFixture(t)
	evaluation.adapter.WithDownstreamRouter(router)
	if _, err := evaluation.adapter.Execute(context.Background(), evaluation.job); err != nil || !router.has(SkillSignalEvaluation) {
		t.Fatalf("evaluation downstream kinds=%v err=%v", router.kinds, err)
	}

	policy := newPolicyAdapterFixture(t, core.SkillRiskLow)
	policy.adapter.WithDownstreamRouter(router)
	if _, err := policy.adapter.Execute(context.Background(), policy.job); err != nil || !router.has(SkillSignalDecision) {
		t.Fatalf("policy downstream kinds=%v err=%v", router.kinds, err)
	}

	canary := newCanaryStartAdapterFixture(t, core.SkillRiskLow, core.SkillDecisionCanary, false)
	if err := canary.adapter.WithDownstreamRouter(router); err != nil {
		t.Fatal(err)
	}
	if _, err := canary.adapter.Execute(context.Background(), canary.job); err != nil || !router.has(SkillSignalCanary) {
		t.Fatalf("canary downstream kinds=%v err=%v", router.kinds, err)
	}

	analysis := newCanaryAnalysisAdapterFixture(t)
	analysis.repository.aggregates[0].VerifiedSuccesses = analysis.repository.aggregates[0].VerifiedSamples
	if err := analysis.adapter.WithDownstreamRouter(router); err != nil {
		t.Fatal(err)
	}
	if _, err := analysis.adapter.Execute(context.Background(), analysis.job); err != nil || !router.has(SkillSignalPromotion) {
		t.Fatalf("analysis downstream kinds=%v policy=%+v err=%v", router.kinds, analysis.policy.last, err)
	}
}

type domainLoopRouter struct {
	seen  map[string]struct{}
	kinds []SkillLifecycleSignalKind
}

func (r *domainLoopRouter) Route(_ context.Context, signal SkillLifecycleSignal) (SkillSignalRouteResult, error) {
	digest := digestSkillLifecycleSignal(signal, signal.ParentJobIDs)
	if _, exists := r.seen[digest]; exists {
		return SkillSignalRouteResult{}, nil
	}
	r.seen[digest] = struct{}{}
	r.kinds = append(r.kinds, signal.Kind)
	return SkillSignalRouteResult{Created: true}, nil
}

func (r *domainLoopRouter) has(kind SkillLifecycleSignalKind) bool {
	for _, routed := range r.kinds {
		if routed == kind {
			return true
		}
	}
	return false
}
