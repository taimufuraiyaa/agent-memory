package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type CreateSkillEvaluationSuiteInput struct {
	ID        string                     `json:"id"`
	Workspace string                     `json:"workspace"`
	SkillID   string                     `json:"skill_id"`
	Cases     []core.SkillEvaluationCase `json:"cases"`
	CreatedBy string                     `json:"created_by"`
}

type skillEvaluationSuiteRepository interface {
	GetLogicalSkill(context.Context, string, string) (core.LogicalSkill, error)
	GetLatestSkillEvaluationSuite(context.Context, string, string) (core.SkillEvaluationSuite, error)
	CreateSkillEvaluationSuite(context.Context, core.SkillEvaluationSuite) error
}

type SkillEvaluationSuiteAuthorizer interface {
	AuthorizeEvaluationSuite(context.Context, string, string, string) error
}

type SkillEvaluationReferenceValidator interface {
	ValidateEvaluationReference(context.Context, string, string) error
}

type SkillEvaluationSuiteService struct {
	repository skillEvaluationSuiteRepository
	authorizer SkillEvaluationSuiteAuthorizer
	references SkillEvaluationReferenceValidator
	now        func() time.Time
}

func NewSkillEvaluationSuiteService(repository skillEvaluationSuiteRepository, authorizer SkillEvaluationSuiteAuthorizer, references SkillEvaluationReferenceValidator, now func() time.Time) *SkillEvaluationSuiteService {
	if now == nil {
		now = time.Now
	}
	return &SkillEvaluationSuiteService{repository: repository, authorizer: authorizer, references: references, now: now}
}

func (s *SkillEvaluationSuiteService) Create(ctx context.Context, input CreateSkillEvaluationSuiteInput) (core.SkillEvaluationSuite, error) {
	if s == nil || s.repository == nil || s.authorizer == nil || s.references == nil {
		return core.SkillEvaluationSuite{}, errors.New("evaluation suite service dependencies are required")
	}
	for field, value := range map[string]string{"id": input.ID, "workspace": input.Workspace, "skill_id": input.SkillID, "created_by": input.CreatedBy} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return core.SkillEvaluationSuite{}, fmt.Errorf("evaluation suite %s is required and bounded", field)
		}
	}
	if err := s.authorizer.AuthorizeEvaluationSuite(ctx, input.CreatedBy, input.Workspace, input.SkillID); err != nil {
		return core.SkillEvaluationSuite{}, err
	}
	skill, err := s.repository.GetLogicalSkill(ctx, input.Workspace, input.SkillID)
	if err != nil || skill.Workspace != input.Workspace {
		return core.SkillEvaluationSuite{}, errors.New("evaluation suite skill is unavailable in workspace")
	}
	for _, item := range input.Cases {
		if err := item.Validate(); err != nil {
			return core.SkillEvaluationSuite{}, err
		}
		if err := s.references.ValidateEvaluationReference(ctx, input.Workspace, item.Reference); err != nil {
			return core.SkillEvaluationSuite{}, fmt.Errorf("validate evaluation reference %q: %w", item.Reference, err)
		}
	}
	digest, err := SkillEvaluationSuiteDigest(input.Cases)
	if err != nil {
		return core.SkillEvaluationSuite{}, err
	}
	version := int64(1)
	latest, err := s.repository.GetLatestSkillEvaluationSuite(ctx, input.Workspace, input.SkillID)
	if err == nil {
		if latest.Digest == digest {
			return latest, nil
		}
		version = latest.Version + 1
	} else if !errors.Is(err, sql.ErrNoRows) {
		return core.SkillEvaluationSuite{}, err
	}
	cases := append([]core.SkillEvaluationCase(nil), input.Cases...)
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	suite := core.SkillEvaluationSuite{ID: input.ID, SkillID: input.SkillID, Workspace: input.Workspace, Version: version, Digest: digest, Cases: cases, CreatedBy: input.CreatedBy, CreatedAt: s.now().UTC()}
	if err := suite.Validate(); err != nil {
		return core.SkillEvaluationSuite{}, err
	}
	if err := s.repository.CreateSkillEvaluationSuite(ctx, suite); err != nil {
		return core.SkillEvaluationSuite{}, err
	}
	return suite, nil
}

func SkillEvaluationSuiteDigest(cases []core.SkillEvaluationCase) (string, error) {
	if len(cases) == 0 || len(cases) > core.MaxSkillListItems {
		return "", errors.New("evaluation suite cases are required and bounded")
	}
	ordered := append([]core.SkillEvaluationCase(nil), cases...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	seen := make(map[string]struct{}, len(ordered))
	hash := sha256.New()
	for _, item := range ordered {
		if err := item.Validate(); err != nil {
			return "", err
		}
		if _, exists := seen[item.ID]; exists {
			return "", errors.New("evaluation suite case ids must be unique")
		}
		seen[item.ID] = struct{}{}
		for _, value := range []string{item.ID, string(item.Kind), item.Summary, item.Reference, strconv.FormatBool(item.Required)} {
			hash.Write([]byte(value))
			hash.Write([]byte{0})
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
