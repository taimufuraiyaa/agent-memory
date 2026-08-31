package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillOrchestratorEnablementEvidence struct {
	ApprovalReference        string
	ReleaseEvidenceReference string
	SignatureReference       string
	ConfigurationDigest      string
	PolicyDigest             string
	ApproverID               string
	ReleaseReviewerID        string
	SignerID                 string
	BuildVersion             string
	MigrationVersion         string
	VerifiedAt               time.Time
}

type SkillOrchestratorEvidenceVerifier interface {
	VerifySkillOrchestratorEnablement(context.Context, core.SkillOrchestratorScope, string, string, string) (SkillOrchestratorEnablementEvidence, error)
}

type SkillOrchestratorConfigurationAuthorizer interface {
	AuthorizeSkillOrchestratorConfiguration(context.Context, string, core.SkillOrchestratorScope, core.SkillOrchestratorMode) error
}

type SkillOrchestratorConfigurationRepository interface {
	GetLatestSkillOrchestratorConfiguration(context.Context, core.SkillOrchestratorScope) (core.SkillOrchestratorConfiguration, error)
	GetSkillOrchestratorConfiguration(context.Context, core.SkillOrchestratorScope, int64) (core.SkillOrchestratorConfiguration, error)
	StoreSkillOrchestratorConfiguration(context.Context, core.SkillOrchestratorConfiguration, core.SkillOrchestratorConfigurationAudit) (bool, error)
}

type SkillOrchestratorConfigurationAudit = core.SkillOrchestratorConfigurationAudit

type SkillOrchestratorConfigurationChange struct {
	Configuration         core.SkillOrchestratorConfiguration
	ActorID               string
	RequestID             string
	ExpectedLatestVersion int64
	ReasonCode            string
}

type SkillOrchestratorConfigurationService struct {
	repository SkillOrchestratorConfigurationRepository
	authorizer SkillOrchestratorConfigurationAuthorizer
	evidence   SkillOrchestratorEvidenceVerifier
	now        func() time.Time
}

func NewSkillOrchestratorConfigurationService(repository SkillOrchestratorConfigurationRepository, authorizer SkillOrchestratorConfigurationAuthorizer, evidence SkillOrchestratorEvidenceVerifier, now func() time.Time) *SkillOrchestratorConfigurationService {
	if now == nil {
		now = time.Now
	}
	return &SkillOrchestratorConfigurationService{repository: repository, authorizer: authorizer, evidence: evidence, now: now}
}

func (s *SkillOrchestratorConfigurationService) Create(ctx context.Context, change SkillOrchestratorConfigurationChange) (core.SkillOrchestratorConfiguration, error) {
	if s == nil || s.repository == nil || s.authorizer == nil {
		return core.SkillOrchestratorConfiguration{}, errors.New("skill orchestrator configuration dependencies are required")
	}
	configuration := change.Configuration
	if err := validateConfigurationChange(change); err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	if err := s.authorizer.AuthorizeSkillOrchestratorConfiguration(ctx, change.ActorID, configuration.Scope, configuration.Mode); err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	latest, latestErr := s.repository.GetLatestSkillOrchestratorConfiguration(ctx, configuration.Scope)
	latestVersion := int64(0)
	if latestErr == nil {
		latestVersion = latest.Version
	} else if !errors.Is(latestErr, core.ErrSkillOrchestratorConfigurationNotFound) {
		return core.SkillOrchestratorConfiguration{}, latestErr
	}
	if change.ExpectedLatestVersion != latestVersion || configuration.Version != latestVersion+1 {
		return core.SkillOrchestratorConfiguration{}, errors.New("skill orchestrator configuration version is stale")
	}
	if configuration.CreatedBy != change.ActorID {
		return core.SkillOrchestratorConfiguration{}, errors.New("configuration creator must match the authorized actor")
	}
	if configuration.CreatedAt.IsZero() {
		configuration.CreatedAt = s.now().UTC()
	}
	expectedDigest, err := ComputeSkillOrchestratorConfigurationDigest(configuration)
	if err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	if configuration.Digest != expectedDigest {
		return core.SkillOrchestratorConfiguration{}, errors.New("skill orchestrator configuration digest mismatch")
	}
	if err := configuration.Validate(); err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	if configuration.Mode == core.SkillOrchestratorAutomaticLowRisk {
		if err := s.verifyEnablement(ctx, configuration, change.ActorID); err != nil {
			return core.SkillOrchestratorConfiguration{}, err
		}
	}
	audit := SkillOrchestratorConfigurationAudit{ActorID: change.ActorID, RequestID: change.RequestID, Operation: "skill_orchestrator.configuration.create", FromVersion: latestVersion, ToVersion: configuration.Version, ReasonCode: change.ReasonCode, OccurredAt: s.now().UTC()}
	created, err := s.repository.StoreSkillOrchestratorConfiguration(ctx, configuration, audit)
	if err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	if !created {
		stored, getErr := s.repository.GetSkillOrchestratorConfiguration(ctx, configuration.Scope, configuration.Version)
		if getErr != nil || stored.Digest != configuration.Digest {
			return core.SkillOrchestratorConfiguration{}, errors.New("configuration version is already bound to different inputs")
		}
		return stored, nil
	}
	return configuration, nil
}

func (s *SkillOrchestratorConfigurationService) Rollback(ctx context.Context, scope core.SkillOrchestratorScope, targetVersion int64, actorID, requestID, reasonCode string) (core.SkillOrchestratorConfiguration, error) {
	if s == nil || s.repository == nil {
		return core.SkillOrchestratorConfiguration{}, errors.New("skill orchestrator configuration dependencies are required")
	}
	target, err := s.repository.GetSkillOrchestratorConfiguration(ctx, scope, targetVersion)
	if err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	latest, err := s.repository.GetLatestSkillOrchestratorConfiguration(ctx, scope)
	if err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	target.Version = latest.Version + 1
	target.CreatedBy = actorID
	target.CreatedAt = s.now().UTC()
	target.Digest = ""
	target.Digest, err = ComputeSkillOrchestratorConfigurationDigest(target)
	if err != nil {
		return core.SkillOrchestratorConfiguration{}, err
	}
	return s.Create(ctx, SkillOrchestratorConfigurationChange{Configuration: target, ActorID: actorID, RequestID: requestID, ExpectedLatestVersion: latest.Version, ReasonCode: reasonCode})
}

func (s *SkillOrchestratorConfigurationService) verifyEnablement(ctx context.Context, configuration core.SkillOrchestratorConfiguration, actorID string) error {
	if s.evidence == nil {
		return errors.New("automatic low-risk mode requires a signed evidence verifier")
	}
	evidence, err := s.evidence.VerifySkillOrchestratorEnablement(ctx, configuration.Scope, configuration.ApprovalReference, configuration.ReleaseEvidenceReference, configuration.SignatureReference)
	if err != nil {
		return fmt.Errorf("verify automatic low-risk evidence: %w", err)
	}
	if evidence.ApprovalReference != configuration.ApprovalReference || evidence.ReleaseEvidenceReference != configuration.ReleaseEvidenceReference || evidence.SignatureReference != configuration.SignatureReference || evidence.ConfigurationDigest != configuration.Digest || evidence.PolicyDigest != configuration.PolicyDigest {
		return errors.New("automatic low-risk evidence digest or reference mismatch")
	}
	for field, value := range map[string]string{"approver": evidence.ApproverID, "release reviewer": evidence.ReleaseReviewerID, "signer": evidence.SignerID, "build version": evidence.BuildVersion, "migration version": evidence.MigrationVersion} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("automatic low-risk evidence %s is required", field)
		}
	}
	if evidence.VerifiedAt.IsZero() {
		return errors.New("automatic low-risk evidence verification timestamp is required")
	}
	if actorID == evidence.ApproverID || actorID == evidence.ReleaseReviewerID || actorID == evidence.SignerID || evidence.ApproverID == evidence.ReleaseReviewerID || evidence.ApproverID == evidence.SignerID || evidence.ReleaseReviewerID == evidence.SignerID {
		return errors.New("automatic low-risk enablement requires separation of duty")
	}
	return nil
}

func ComputeSkillOrchestratorConfigurationDigest(configuration core.SkillOrchestratorConfiguration) (string, error) {
	configuration.Digest = ""
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateConfigurationChange(change SkillOrchestratorConfigurationChange) error {
	for field, value := range map[string]string{"actor_id": change.ActorID, "request_id": change.RequestID, "reason_code": change.ReasonCode} {
		if strings.TrimSpace(value) == "" || len(value) > core.MaxSkillOrchestratorReferenceBytes || strings.ContainsAny(value, "\r\n\t") {
			return fmt.Errorf("configuration change %s is required and bounded", field)
		}
	}
	if change.ExpectedLatestVersion < 0 {
		return errors.New("configuration expected latest version cannot be negative")
	}
	return nil
}

var ErrSkillOrchestratorConfigurationNotFound = core.ErrSkillOrchestratorConfigurationNotFound
