package application

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
)

type GraphDeletionRequest = contracts.GraphDeletionRequest
type GraphDeletionImpact = contracts.GraphDeletionImpact
type GraphDeletionTombstone = contracts.GraphDeletionTombstone
type GraphDeletionResult = contracts.GraphDeletionResult
type GraphLifecycleRepository = contracts.GraphLifecycleRepository

type GraphLifecycleService struct{ repository GraphLifecycleRepository }

func NewGraphLifecycleService(repository GraphLifecycleRepository) *GraphLifecycleService {
	return &GraphLifecycleService{repository: repository}
}

func (s *GraphLifecycleService) DeleteCanonicalEvidence(ctx context.Context, request GraphDeletionRequest) (GraphDeletionResult, error) {
	if s == nil || s.repository == nil {
		return GraphDeletionResult{}, fmt.Errorf("graph lifecycle repository is required")
	}
	if err := request.Scope.Validate(); err != nil {
		return GraphDeletionResult{}, err
	}
	if strings.TrimSpace(request.CanonicalKind) == "" || strings.TrimSpace(request.CanonicalID) == "" || request.DeletedAt.IsZero() {
		return GraphDeletionResult{}, fmt.Errorf("graph deletion canonical identity and time are required")
	}
	if request.RepairDeadline.IsZero() {
		request.RepairDeadline = request.DeletedAt.Add(30 * time.Minute)
	}
	if request.RepairDeadline.Before(request.DeletedAt) || request.RepairDeadline.After(request.DeletedAt.Add(2*time.Hour)) {
		return GraphDeletionResult{}, fmt.Errorf("graph deletion repair deadline is outside policy")
	}
	if atomic, ok := s.repository.(contracts.GraphLifecycleAtomicRepository); ok {
		impact, err := atomic.DeleteGraphEvidenceAtomic(ctx, request)
		if err != nil {
			return GraphDeletionResult{}, err
		}
		return graphDeletionResult(request, impact), nil
	}
	impact, err := s.repository.RevokeGraphEvidence(ctx, request)
	if err != nil {
		return GraphDeletionResult{}, err
	}
	if err := s.repository.RecordGraphDeletionAndScheduleRepair(ctx, request, impact); err != nil {
		return GraphDeletionResult{}, err
	}
	return graphDeletionResult(request, impact), nil
}

func graphDeletionResult(request GraphDeletionRequest, impact GraphDeletionImpact) GraphDeletionResult {
	return GraphDeletionResult{
		GraphDeletionImpact: impact,
		Tombstone:           GraphDeletionTombstone{Scope: request.Scope, CanonicalKind: request.CanonicalKind, CanonicalID: request.CanonicalID, DeletedAt: request.DeletedAt.UTC()},
		RepairDeadline:      request.RepairDeadline.UTC(),
	}
}

type GraphArtifactClass string

const (
	GraphArtifactProjection GraphArtifactClass = "projection"
	GraphArtifactCache      GraphArtifactClass = "cache"
	GraphArtifactNative     GraphArtifactClass = "native"
)

type GraphRetainedArtifact struct {
	ID                 string
	Class              GraphArtifactClass
	RetentionStartedAt time.Time
	Hold               bool
}

type GraphRetentionPlan struct {
	DeleteIDs []string
	HeldIDs   []string
}

func PlanGraphRetention(now time.Time, artifacts []GraphRetainedArtifact) GraphRetentionPlan {
	plan := GraphRetentionPlan{}
	for _, artifact := range artifacts {
		if artifact.Hold {
			plan.HeldIDs = append(plan.HeldIDs, artifact.ID)
			continue
		}
		var ttl time.Duration
		switch artifact.Class {
		case GraphArtifactProjection:
			ttl = 24 * time.Hour
		case GraphArtifactCache:
			ttl = 7 * 24 * time.Hour
		case GraphArtifactNative:
			ttl = 30 * 24 * time.Hour
		default:
			continue
		}
		if !artifact.RetentionStartedAt.IsZero() && !now.Before(artifact.RetentionStartedAt.Add(ttl)) {
			plan.DeleteIDs = append(plan.DeleteIDs, artifact.ID)
		}
	}
	sort.Strings(plan.DeleteIDs)
	sort.Strings(plan.HeldIDs)
	return plan
}

func RebuildGraphFromCanonical(request GraphProjectionRequest) (GraphProjection, error) {
	return NewGraphProjectionBuilder().Build(request)
}
