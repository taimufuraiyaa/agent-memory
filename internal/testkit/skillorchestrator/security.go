package skillorchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SecurityIsolationResult struct {
	OwnScopeVisible       bool
	ForeignScopeConcealed bool
	ForgedReferenceDenied bool
	ForeignListingEmpty   bool
	ErrorsContentFree     bool
}

func RunSecurityIsolationReview(ctx context.Context, repository contracts.SkillOrchestratorRepository, scopeA, scopeB core.SkillOrchestratorScope) (SecurityIsolationResult, error) {
	if repository == nil || scopeA.Validate() != nil || scopeB.Validate() != nil || scopeA == scopeB {
		return SecurityIsolationResult{}, errors.New("two distinct valid security scopes are required")
	}
	now := time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC)
	workflow, job := securityWorkflowAndJob(scopeA, now)
	if _, created, err := repository.CreateSkillWorkflow(ctx, workflow); err != nil || !created {
		return SecurityIsolationResult{}, fmt.Errorf("create security fixture: %w", err)
	}
	if _, created, err := repository.EnqueueSkillJob(ctx, job, nil); err != nil || !created {
		return SecurityIsolationResult{}, fmt.Errorf("enqueue security fixture: %w", err)
	}
	_, ownErr := repository.GetSkillJob(ctx, scopeA, job.ID)
	_, foreignErr := repository.GetSkillJob(ctx, scopeB, job.ID)
	forged := job
	forged.ID = uuid.NewString()
	forged.Scope = scopeB
	_, _, forgedErr := repository.EnqueueSkillJob(ctx, forged, nil)
	foreign, _, listErr := repository.ListSkillJobs(ctx, scopeB, workflow.ID, "", 10)
	if listErr != nil {
		return SecurityIsolationResult{}, fmt.Errorf("foreign security listing: %w", listErr)
	}
	foreignMessage, forgedMessage := "", ""
	if foreignErr != nil {
		foreignMessage = foreignErr.Error()
	}
	if forgedErr != nil {
		forgedMessage = forgedErr.Error()
	}
	contentFree := true
	for _, identifier := range []string{workflow.ID, job.ID, scopeA.TenantID, scopeA.WorkspaceID} {
		if identifier != "" && (strings.Contains(foreignMessage, identifier) || strings.Contains(forgedMessage, identifier)) {
			contentFree = false
		}
	}
	return SecurityIsolationResult{
		OwnScopeVisible: ownErr == nil, ForeignScopeConcealed: foreignErr != nil,
		ForgedReferenceDenied: forgedErr != nil, ForeignListingEmpty: len(foreign) == 0,
		ErrorsContentFree: contentFree,
	}, nil
}

func (r SecurityIsolationResult) Passed() bool {
	return r.OwnScopeVisible && r.ForeignScopeConcealed && r.ForgedReferenceDenied && r.ForeignListingEmpty && r.ErrorsContentFree
}

func securityWorkflowAndJob(scope core.SkillOrchestratorScope, now time.Time) (core.SkillWorkflow, core.SkillJob) {
	digest := "sha256:" + strings.Repeat("7", 64)
	workflow := core.SkillWorkflow{
		ID: uuid.NewString(), Scope: scope, SkillID: uuid.NewString(), OriginKind: core.SkillWorkflowOriginOperator,
		OriginID: uuid.NewString(), Kind: core.SkillWorkflowAutomaticRevision, ContractVersion: core.SkillOrchestratorContractVersion,
		InputDigest: digest, State: core.SkillWorkflowOpen, CurrentStage: core.SkillStageDetect,
		Generation: 1, ConfigurationVersion: 1, PolicyDigest: digest, CreatedAt: now, UpdatedAt: now,
	}
	job := core.SkillJob{
		ID: uuid.NewString(), WorkflowID: workflow.ID, Scope: scope, Stage: core.SkillStageDetect,
		ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: digest, PolicyVersion: 1,
		State: core.SkillJobQueued, Priority: 100, ReadyAt: now, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now,
	}
	return workflow, job
}
