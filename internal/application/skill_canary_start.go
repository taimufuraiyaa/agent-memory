package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillCanaryStartInput struct {
	Workspace           string `json:"workspace"`
	Environment         string `json:"environment"`
	SkillID             string `json:"skill_id"`
	CandidateRevisionID string `json:"candidate_revision_id"`
	PolicyDecisionID    string `json:"policy_decision_id"`
	ExpectedGeneration  int64  `json:"expected_generation"`
	Actor               string `json:"actor"`
}

type skillCanaryStartRepository interface {
	GetSkillRevision(context.Context, string, string) (core.SkillRevision, error)
	GetSkillPolicyDecision(context.Context, string, string) (core.SkillPolicyDecision, error)
	StartSkillCanary(context.Context, string, string, string, string, string, string, int64, time.Time) (core.SkillActivation, error)
}

type SkillCanaryStartService struct {
	repository skillCanaryStartRepository
	now        func() time.Time
}

func NewSkillCanaryStartService(repository skillCanaryStartRepository, now func() time.Time) *SkillCanaryStartService {
	if now == nil {
		now = time.Now
	}
	return &SkillCanaryStartService{repository: repository, now: now}
}

func (s *SkillCanaryStartService) Start(ctx context.Context, input SkillCanaryStartInput) (core.SkillActivation, error) {
	if s == nil || s.repository == nil {
		return core.SkillActivation{}, errors.New("skill canary repository is required")
	}
	for _, value := range []string{input.Workspace, input.Environment, input.SkillID, input.CandidateRevisionID, input.PolicyDecisionID, input.Actor} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return core.SkillActivation{}, errors.New("skill canary scope is required and bounded")
		}
	}
	if input.ExpectedGeneration < 1 {
		return core.SkillActivation{}, errors.New("skill canary expected_generation must be positive")
	}
	revision, err := s.repository.GetSkillRevision(ctx, input.Workspace, input.CandidateRevisionID)
	if err != nil {
		return core.SkillActivation{}, err
	}
	decision, err := s.repository.GetSkillPolicyDecision(ctx, input.Workspace, input.PolicyDecisionID)
	if err != nil {
		return core.SkillActivation{}, err
	}
	if revision.SkillID != input.SkillID || revision.State != core.SkillRevisionTesting || decision.SkillID != input.SkillID || decision.RevisionID != revision.ID || decision.RiskTier != revision.RiskTier || decision.Decision != core.SkillDecisionCanary {
		return core.SkillActivation{}, errors.New("skill canary revision or policy decision is ineligible")
	}
	return s.repository.StartSkillCanary(ctx, input.Workspace, input.Environment, input.SkillID, input.CandidateRevisionID, input.PolicyDecisionID, input.Actor, input.ExpectedGeneration, s.now().UTC())
}
