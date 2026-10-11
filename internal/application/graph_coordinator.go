package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphCoordinationAction string

const (
	GraphCoordinationWait         GraphCoordinationAction = "wait"
	GraphCoordinationSchedule     GraphCoordinationAction = "schedule"
	GraphCoordinationCoalesce     GraphCoordinationAction = "coalesce"
	GraphCoordinationBackpressure GraphCoordinationAction = "backpressure"
)

type GraphCoordinationSnapshot struct {
	Scope                core.GraphScope
	ConfigurationID      string
	ConfigurationVersion int64
	ProjectionVersion    string
	PendingChanges       int
	ProjectionBytes      int64
	EstimatedCostUSD     float64
	OldestChangeAt       time.Time
	NewestChangeAt       time.Time
	CutoffSequence       int64
	CutoffFingerprints   []string
	RunningRevisions     int
	SuccessorQueued      bool
	TenantRunning        int
	GlobalRunning        int
	OperatorImmediate    bool
	Now                  time.Time
}

type GraphScheduleRequest struct {
	RevisionID      string
	Scope           core.GraphScope
	ConfigurationID string
	Successor       bool
	Cutoff          core.GraphWatermark
	IdempotencyKey  string
	CreatedAt       time.Time
}

type GraphCoordinatorStore interface {
	// ScheduleGraphRevision must atomically enforce one running revision and at
	// most one queued successor for the scope/configuration.
	ScheduleGraphRevision(context.Context, GraphScheduleRequest) error
}

type GraphCoordinationDecision struct {
	Action  GraphCoordinationAction
	Reason  string
	Request *GraphScheduleRequest
}

type GraphCoordinator struct {
	store  GraphCoordinatorStore
	limits GraphLimits
}

func NewGraphCoordinator(store GraphCoordinatorStore, limits GraphLimits) *GraphCoordinator {
	return &GraphCoordinator{store: store, limits: limits}
}

func (c *GraphCoordinator) Coordinate(ctx context.Context, snapshot GraphCoordinationSnapshot) (GraphCoordinationDecision, error) {
	if c == nil || c.store == nil {
		return GraphCoordinationDecision{}, fmt.Errorf("graph coordinator store is required")
	}
	if err := c.limits.Validate(); err != nil {
		return GraphCoordinationDecision{}, err
	}
	if err := snapshot.Scope.Validate(); err != nil {
		return GraphCoordinationDecision{}, err
	}
	if strings.TrimSpace(snapshot.ConfigurationID) == "" || snapshot.ConfigurationVersion < 1 || strings.TrimSpace(snapshot.ProjectionVersion) == "" || snapshot.Now.IsZero() {
		return GraphCoordinationDecision{}, fmt.Errorf("graph coordination snapshot identity is incomplete")
	}
	if snapshot.PendingChanges < 1 {
		return GraphCoordinationDecision{Action: GraphCoordinationWait, Reason: "no_changes"}, nil
	}
	due := snapshot.OperatorImmediate || snapshot.PendingChanges >= c.limits.BatchChanges
	if !snapshot.OldestChangeAt.IsZero() && snapshot.Now.Sub(snapshot.OldestChangeAt) >= c.limits.MaxWait {
		due = true
	}
	if !due {
		return GraphCoordinationDecision{Action: GraphCoordinationWait, Reason: "coalescing_window"}, nil
	}
	if snapshot.RunningRevisions > 1 {
		return GraphCoordinationDecision{}, fmt.Errorf("graph coordination invariant violated: multiple running revisions")
	}
	if snapshot.RunningRevisions == 1 && snapshot.SuccessorQueued {
		return GraphCoordinationDecision{Action: GraphCoordinationCoalesce, Reason: "successor_already_queued"}, nil
	}
	admission := c.limits.Admit(GraphWorkEstimate{
		Changes: snapshot.PendingChanges, ProjectionBytes: snapshot.ProjectionBytes,
		EstimatedCostUSD: snapshot.EstimatedCostUSD, TenantRunning: snapshot.TenantRunning,
		GlobalRunning: snapshot.GlobalRunning,
	})
	if !admission.Allowed {
		return GraphCoordinationDecision{Action: GraphCoordinationBackpressure, Reason: admission.Code}, nil
	}
	cutoff := graphCoordinationWatermark(snapshot)
	identity := strings.Join([]string{snapshot.Scope.TenantID, snapshot.Scope.WorkspaceID, snapshot.ConfigurationID,
		fmt.Sprint(snapshot.ConfigurationVersion), snapshot.ProjectionVersion, fmt.Sprint(cutoff.Sequence), cutoff.Digest}, "\x00")
	idempotencyDigest := sha256.Sum256([]byte(identity))
	request := GraphScheduleRequest{
		RevisionID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("revision\x00"+identity)).String(), Scope: snapshot.Scope,
		ConfigurationID: snapshot.ConfigurationID, Successor: snapshot.RunningRevisions == 1,
		Cutoff: cutoff, IdempotencyKey: "sha256:" + hex.EncodeToString(idempotencyDigest[:]), CreatedAt: snapshot.Now.UTC(),
	}
	if err := c.store.ScheduleGraphRevision(ctx, request); err != nil {
		return GraphCoordinationDecision{}, err
	}
	return GraphCoordinationDecision{Action: GraphCoordinationSchedule, Reason: "threshold_reached", Request: &request}, nil
}

func graphCoordinationWatermark(snapshot GraphCoordinationSnapshot) core.GraphWatermark {
	fingerprints := append([]string(nil), snapshot.CutoffFingerprints...)
	sort.Strings(fingerprints)
	digest := sha256.Sum256([]byte(strings.Join(fingerprints, "\x00")))
	eventTime := snapshot.NewestChangeAt
	if eventTime.IsZero() {
		eventTime = snapshot.Now
	}
	return core.GraphWatermark{Sequence: snapshot.CutoffSequence, EventTime: eventTime.UTC(), Digest: "sha256:" + hex.EncodeToString(digest[:])}
}
