package application

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillAcknowledgementInput struct {
	Workspace    string `json:"workspace"`
	ResolutionID string `json:"resolution_id"`
	PrincipalID  string `json:"principal_id"`
	TaskID       string `json:"task_id"`
	RevisionID   string `json:"revision_id"`
	Digest       string `json:"digest"`
	Token        string `json:"token"`
}

type skillAcknowledgementRepositoryContract interface {
	GetSkillResolution(context.Context, string, string) (core.SkillResolution, error)
	GetSkillResolutionAcknowledgement(context.Context, string, string) (core.SkillResolutionAcknowledgement, error)
	AcknowledgeSkillResolution(context.Context, core.SkillResolutionAcknowledgement) (core.SkillResolutionAcknowledgement, error)
}

type SkillAcknowledgementService struct {
	repository skillAcknowledgementRepositoryContract
	now        func() time.Time
}

func NewSkillAcknowledgementService(repository skillAcknowledgementRepositoryContract, now func() time.Time) *SkillAcknowledgementService {
	if now == nil {
		now = time.Now
	}
	return &SkillAcknowledgementService{repository: repository, now: now}
}

func (s *SkillAcknowledgementService) Acknowledge(ctx context.Context, input SkillAcknowledgementInput) (core.SkillResolutionAcknowledgement, error) {
	if s == nil || s.repository == nil {
		return core.SkillResolutionAcknowledgement{}, errors.New("skill acknowledgement repository is required")
	}
	for field, value := range map[string]string{"workspace": input.Workspace, "resolution_id": input.ResolutionID, "principal_id": input.PrincipalID, "task_id": input.TaskID, "revision_id": input.RevisionID, "digest": input.Digest, "token": input.Token} {
		if strings.TrimSpace(value) == "" || len(value) > 512 {
			return core.SkillResolutionAcknowledgement{}, fmt.Errorf("skill acknowledgement %s is required and bounded", field)
		}
	}
	resolution, err := s.repository.GetSkillResolution(ctx, input.Workspace, input.ResolutionID)
	if err != nil {
		return core.SkillResolutionAcknowledgement{}, err
	}
	if resolution.PrincipalID != input.PrincipalID || resolution.TaskID != input.TaskID || resolution.RevisionID != input.RevisionID || resolution.Digest != input.Digest {
		return core.SkillResolutionAcknowledgement{}, errors.New("skill acknowledgement scope does not match resolution")
	}
	now := s.now().UTC()
	if now.After(resolution.ExpiresAt) {
		return core.SkillResolutionAcknowledgement{}, errors.New("skill acknowledgement token expired")
	}
	tokenDigest := sha256.Sum256([]byte(input.Token))
	want, err := hex.DecodeString(strings.TrimPrefix(resolution.AcknowledgementTokenHash, "sha256:"))
	if err != nil || len(want) != len(tokenDigest) || subtle.ConstantTimeCompare(tokenDigest[:], want) != 1 {
		return core.SkillResolutionAcknowledgement{}, errors.New("skill acknowledgement token is invalid")
	}
	acknowledgement := core.SkillResolutionAcknowledgement{Workspace: input.Workspace, ResolutionID: input.ResolutionID, PrincipalID: input.PrincipalID, TaskID: input.TaskID, RevisionID: input.RevisionID, RevisionDigest: input.Digest, AcknowledgedAt: now}
	if err := acknowledgement.Validate(); err != nil {
		return core.SkillResolutionAcknowledgement{}, err
	}
	existing, existingErr := s.repository.GetSkillResolutionAcknowledgement(ctx, input.Workspace, input.ResolutionID)
	if existingErr == nil {
		if existing.PrincipalID != acknowledgement.PrincipalID || existing.TaskID != acknowledgement.TaskID || existing.RevisionID != acknowledgement.RevisionID || existing.RevisionDigest != acknowledgement.RevisionDigest {
			return core.SkillResolutionAcknowledgement{}, errors.New("skill acknowledgement replay does not match original")
		}
		return existing, nil
	}
	if !errors.Is(existingErr, sql.ErrNoRows) {
		return core.SkillResolutionAcknowledgement{}, existingErr
	}
	return s.repository.AcknowledgeSkillResolution(ctx, acknowledgement)
}
