package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var ErrNoCompatibleSkillRevision = errors.New("no compatible skill revision is available")

type SkillResolutionRequest struct {
	Workspace                string   `json:"workspace"`
	Environment              string   `json:"environment"`
	PrincipalID              string   `json:"principal_id"`
	TaskID                   string   `json:"task_id"`
	SkillID                  string   `json:"skill_id"`
	ExplicitRevisionID       string   `json:"explicit_revision_id,omitempty"`
	EnvironmentRevisionID    string   `json:"environment_revision_id,omitempty"`
	Platform                 string   `json:"platform"`
	Architecture             string   `json:"architecture"`
	RuntimeVersion           string   `json:"runtime_version"`
	Capabilities             []string `json:"capabilities,omitempty"`
	PolicyVersion            int64    `json:"policy_version"`
	CanaryBasisPoints        int      `json:"canary_basis_points"`
	CanaryApproved           bool     `json:"canary_approved"`
	AcknowledgementSupported bool     `json:"acknowledgement_supported"`
}

type SkillCompatibilityDecision struct {
	Compatible          bool     `json:"compatible"`
	PlatformMatched     bool     `json:"platform_matched"`
	ArchitectureMatched bool     `json:"architecture_matched"`
	RuntimeMatched      bool     `json:"runtime_matched"`
	MissingCapabilities []string `json:"missing_capabilities,omitempty"`
}

type SkillResolutionResult struct {
	Resolution           core.SkillResolution       `json:"resolution"`
	Compatibility        SkillCompatibilityDecision `json:"compatibility"`
	AcknowledgementToken string                     `json:"acknowledgement_token"`
}

type skillResolutionRepository interface {
	GetLogicalSkill(context.Context, string, string) (core.LogicalSkill, error)
	GetSkillActivation(context.Context, string, string, string) (core.SkillActivation, error)
	GetSkillRevision(context.Context, string, string) (core.SkillRevision, error)
	CreateSkillResolution(context.Context, core.SkillResolution) error
}

type SkillResolutionAuthorizer interface {
	AuthorizeSkillResolution(context.Context, string, string, string, string) error
	AuthorizeSkillPin(context.Context, string, string, string, string) error
}

type SkillResolutionArtifactVerifier interface {
	VerifyActive(context.Context, core.LogicalSkill, core.SkillRevision) error
	VerifyImmutable(context.Context, core.SkillRevision) error
}

type SkillResolver struct {
	repository skillResolutionRepository
	authorizer SkillResolutionAuthorizer
	artifacts  SkillResolutionArtifactVerifier
	now        func() time.Time
}

func NewSkillResolver(repository skillResolutionRepository, authorizer SkillResolutionAuthorizer, artifacts SkillResolutionArtifactVerifier, now func() time.Time) *SkillResolver {
	if now == nil {
		now = time.Now
	}
	return &SkillResolver{repository: repository, authorizer: authorizer, artifacts: artifacts, now: now}
}

func (r *SkillResolver) Resolve(ctx context.Context, request SkillResolutionRequest) (SkillResolutionResult, error) {
	if r == nil || r.repository == nil || r.authorizer == nil || r.artifacts == nil {
		return SkillResolutionResult{}, errors.New("skill resolver dependencies are required")
	}
	if err := validateResolutionRequest(request); err != nil {
		return SkillResolutionResult{}, err
	}
	if err := r.authorizer.AuthorizeSkillResolution(ctx, request.PrincipalID, request.Workspace, request.Environment, request.SkillID); err != nil {
		return SkillResolutionResult{}, err
	}
	skill, err := r.repository.GetLogicalSkill(ctx, request.Workspace, request.SkillID)
	if err != nil {
		return SkillResolutionResult{}, err
	}
	if skill.Status != core.SkillStatusActive {
		return SkillResolutionResult{}, errors.New("logical skill is not active")
	}
	activation, err := r.repository.GetSkillActivation(ctx, request.Workspace, request.Environment, request.SkillID)
	if err != nil {
		return SkillResolutionResult{}, err
	}
	if activation.Materialization != core.SkillMaterializationReady {
		return SkillResolutionResult{}, errors.New("active skill materialization is not ready")
	}

	selected, reason, compatibility, err := r.selectRevision(ctx, request, skill, activation)
	if err != nil {
		return SkillResolutionResult{}, err
	}
	if selected.ID == activation.ActiveRevisionID {
		if selected.BundleDigest != activation.ActiveDigest {
			return SkillResolutionResult{}, errors.New("active revision digest does not match activation")
		}
		if err := r.artifacts.VerifyActive(ctx, skill, selected); err != nil {
			return SkillResolutionResult{}, fmt.Errorf("verify active skill artifact: %w", err)
		}
	} else if err := r.artifacts.VerifyImmutable(ctx, selected); err != nil {
		return SkillResolutionResult{}, fmt.Errorf("verify immutable skill artifact: %w", err)
	}

	resolutionID, token, tokenHash, err := newSkillAcknowledgement()
	if err != nil {
		return SkillResolutionResult{}, err
	}
	now := r.now().UTC()
	resolution := core.SkillResolution{
		ID: resolutionID, Workspace: request.Workspace, Environment: request.Environment, PrincipalID: request.PrincipalID,
		TaskID: request.TaskID, SkillID: skill.ID, RevisionID: selected.ID, RevisionNumber: selected.Number,
		Digest: selected.BundleDigest, Reason: reason, PolicyVersion: request.PolicyVersion,
		FallbackRevisionID: activation.LastKnownGoodRevisionID, FallbackDigest: activation.LastKnownGoodDigest,
		AcknowledgementTokenHash: tokenHash, ExpiresAt: now.Add(5 * time.Minute), ResolvedAt: now,
	}
	if resolution.FallbackRevisionID == selected.ID {
		resolution.FallbackRevisionID, resolution.FallbackDigest = "", ""
	}
	if err := resolution.Validate(); err != nil {
		return SkillResolutionResult{}, err
	}
	if err := r.repository.CreateSkillResolution(ctx, resolution); err != nil {
		return SkillResolutionResult{}, err
	}
	return SkillResolutionResult{Resolution: resolution, Compatibility: compatibility, AcknowledgementToken: token}, nil
}

func (r *SkillResolver) selectRevision(ctx context.Context, request SkillResolutionRequest, skill core.LogicalSkill, activation core.SkillActivation) (core.SkillRevision, core.SkillResolutionReason, SkillCompatibilityDecision, error) {
	for _, pin := range []struct {
		id     string
		reason core.SkillResolutionReason
	}{{request.ExplicitRevisionID, core.SkillResolutionExplicitPin}, {request.EnvironmentRevisionID, core.SkillResolutionEnvironment}} {
		if pin.id == "" {
			continue
		}
		if err := r.authorizer.AuthorizeSkillPin(ctx, request.PrincipalID, request.Workspace, skill.ID, pin.id); err != nil {
			return core.SkillRevision{}, "", SkillCompatibilityDecision{}, err
		}
		revision, err := r.repository.GetSkillRevision(ctx, request.Workspace, pin.id)
		if err != nil {
			return core.SkillRevision{}, "", SkillCompatibilityDecision{}, err
		}
		decision := evaluateSkillCompatibility(revision.Compatibility, request)
		if revision.SkillID != skill.ID || !selectablePinnedRevision(revision.State) || !decision.Compatible {
			return core.SkillRevision{}, "", decision, errors.New("pinned skill revision is unavailable or incompatible")
		}
		return revision, pin.reason, decision, nil
	}

	if activation.CanaryRevisionID != "" {
		canary, err := r.repository.GetSkillRevision(ctx, request.Workspace, activation.CanaryRevisionID)
		if err == nil {
			decision := evaluateSkillCompatibility(canary.Compatibility, request)
			allocation := (SkillCanaryAllocator{}).Allocate(SkillCanaryAllocationInput{Workspace: request.Workspace, Environment: request.Environment, TaskID: request.TaskID, SkillID: request.SkillID, PolicyVersion: request.PolicyVersion, BasisPoints: request.CanaryBasisPoints, RiskTier: canary.RiskTier, Approved: request.CanaryApproved, Compatible: decision.Compatible, AcknowledgementSupported: request.AcknowledgementSupported})
			if canary.SkillID == skill.ID && canary.State == core.SkillRevisionCanary && canary.BundleDigest == activation.CanaryDigest && allocation.Allocated {
				return canary, core.SkillResolutionCanary, decision, nil
			}
		}
	}

	active, activeErr := r.repository.GetSkillRevision(ctx, request.Workspace, activation.ActiveRevisionID)
	if activeErr == nil {
		decision := evaluateSkillCompatibility(active.Compatibility, request)
		if active.SkillID == skill.ID && active.State == core.SkillRevisionActive && active.BundleDigest == activation.ActiveDigest && decision.Compatible {
			return active, core.SkillResolutionActive, decision, nil
		}
	}
	if activation.LastKnownGoodRevisionID != "" {
		fallback, fallbackErr := r.repository.GetSkillRevision(ctx, request.Workspace, activation.LastKnownGoodRevisionID)
		if fallbackErr == nil {
			decision := evaluateSkillCompatibility(fallback.Compatibility, request)
			if fallback.SkillID == skill.ID && (fallback.State == core.SkillRevisionPrevious || fallback.State == core.SkillRevisionActive) && fallback.BundleDigest == activation.LastKnownGoodDigest && decision.Compatible {
				return fallback, core.SkillResolutionFallback, decision, nil
			}
		}
	}
	return core.SkillRevision{}, "", SkillCompatibilityDecision{}, ErrNoCompatibleSkillRevision
}

func validateResolutionRequest(request SkillResolutionRequest) error {
	for field, value := range map[string]string{"workspace": request.Workspace, "environment": request.Environment, "principal_id": request.PrincipalID, "task_id": request.TaskID, "skill_id": request.SkillID, "platform": request.Platform, "architecture": request.Architecture, "runtime_version": request.RuntimeVersion} {
		if strings.TrimSpace(value) == "" || len(value) > 256 {
			return fmt.Errorf("skill resolution %s is required and bounded", field)
		}
	}
	if request.PolicyVersion < 1 || request.CanaryBasisPoints < 0 || request.CanaryBasisPoints > 10_000 {
		return errors.New("skill resolution policy_version or canary allocation is invalid")
	}
	if !validRuntimeVersion(request.RuntimeVersion) {
		return errors.New("skill resolution runtime_version must be numeric dot-separated components")
	}
	return nil
}

func selectablePinnedRevision(state core.SkillRevisionState) bool {
	return state == core.SkillRevisionActive || state == core.SkillRevisionPrevious || state == core.SkillRevisionCanary
}

func evaluateSkillCompatibility(compatibility core.SkillCompatibility, request SkillResolutionRequest) SkillCompatibilityDecision {
	decision := SkillCompatibilityDecision{
		PlatformMatched:     len(compatibility.Platforms) == 0 || containsSkillValue(compatibility.Platforms, request.Platform),
		ArchitectureMatched: len(compatibility.Architectures) == 0 || containsSkillValue(compatibility.Architectures, request.Architecture),
		RuntimeMatched:      compatibility.MinimumRuntime == "" || (validRuntimeVersion(compatibility.MinimumRuntime) && compareRuntimeVersions(request.RuntimeVersion, compatibility.MinimumRuntime) >= 0),
		MissingCapabilities: []string{},
	}
	available := make(map[string]struct{}, len(request.Capabilities))
	for _, capability := range request.Capabilities {
		available[capability] = struct{}{}
	}
	for _, required := range compatibility.RequiredCapabilities {
		if _, exists := available[required]; !exists {
			decision.MissingCapabilities = append(decision.MissingCapabilities, required)
		}
	}
	decision.Compatible = decision.PlatformMatched && decision.ArchitectureMatched && decision.RuntimeMatched && len(decision.MissingCapabilities) == 0
	return decision
}

func containsSkillValue(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func compareRuntimeVersions(left, right string) int {
	leftParts, rightParts := strings.Split(left, "."), strings.Split(right, ".")
	length := len(leftParts)
	if len(rightParts) > length {
		length = len(rightParts)
	}
	for index := 0; index < length; index++ {
		var leftValue, rightValue int64
		if index < len(leftParts) {
			leftValue, _ = strconv.ParseInt(leftParts[index], 10, 64)
		}
		if index < len(rightParts) {
			rightValue, _ = strconv.ParseInt(rightParts[index], 10, 64)
		}
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}

func validRuntimeVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) == 0 || len(parts) > 8 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if _, err := strconv.ParseUint(part, 10, 31); err != nil {
			return false
		}
	}
	return true
}

func newSkillAcknowledgement() (string, string, string, error) {
	var idRandom, tokenRandom [16]byte
	if _, err := rand.Read(idRandom[:]); err != nil {
		return "", "", "", errors.New("generate skill resolution id")
	}
	if _, err := rand.Read(tokenRandom[:]); err != nil {
		return "", "", "", errors.New("generate skill acknowledgement token")
	}
	token := hex.EncodeToString(tokenRandom[:])
	digest := sha256.Sum256([]byte(token))
	return "resolution-" + hex.EncodeToString(idRandom[:]), token, "sha256:" + hex.EncodeToString(digest[:]), nil
}
