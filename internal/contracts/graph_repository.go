package contracts

import (
	"context"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type GraphCommunityMember struct {
	Kind     string `json:"kind"`
	TargetID string `json:"target_id"`
}

type GraphEntityImportRecord struct {
	Entity   core.GraphEntity
	Version  core.GraphEntityVersion
	Evidence []core.GraphEvidence
}

type GraphEdgeImportRecord struct {
	Edge     core.GraphEdge
	Version  core.GraphEdgeVersion
	Evidence []core.GraphEvidence
}

type GraphCommunityImportRecord struct {
	Community core.GraphCommunity
	Members   []GraphCommunityMember
	Report    core.GraphReport
}

// GraphRevisionImportBatch is the complete normalized content of one inactive
// revision. Stores must apply all records and the Ready transition in one
// transaction or leave the revision unchanged.
type GraphRevisionImportBatch struct {
	Scope               core.GraphScope
	ConfigurationID     string
	RevisionID          string
	Entities            []GraphEntityImportRecord
	Edges               []GraphEdgeImportRecord
	Communities         []GraphCommunityImportRecord
	ExpectedEntities    int
	ExpectedEdges       int
	ExpectedCommunities int
}

type GraphRevisionBatchStore interface {
	ImportGraphRevisionBatch(context.Context, GraphRevisionImportBatch) error
}

// GraphRepository is the provider-neutral normalized derived-index store used
// by both standalone SQLite and hosted PostgreSQL implementations.
type GraphRepository interface {
	UpsertGraphConfiguration(context.Context, core.GraphConfiguration) error
	CreateGraphRevision(context.Context, core.GraphRevision) error
	EnqueueGraphJob(context.Context, core.GraphJob) (core.GraphJob, bool, error)
	ClaimGraphJobs(context.Context, core.GraphScope, string, int, time.Duration, time.Time) ([]core.GraphJob, error)
	CancelGraphJob(context.Context, core.GraphScope, string, time.Time) error
	ActivateGraphRevision(context.Context, core.GraphActivation) error
	ActiveGraphRevisions(context.Context, core.GraphScope, string) (string, string, error)

	ImportGraphEntity(context.Context, core.GraphEntity, core.GraphEntityVersion, []core.GraphEvidence) error
	ImportGraphEdge(context.Context, core.GraphEdge, core.GraphEdgeVersion, []core.GraphEvidence) error
	ImportGraphCommunity(context.Context, core.GraphCommunity, []GraphCommunityMember, core.GraphReport) error
	MarkGraphReportStale(context.Context, core.GraphScope, string) error
	GraphReport(context.Context, core.GraphScope, string) (core.GraphReport, error)
	ListQueryableGraphEdges(context.Context, core.GraphScope) ([]core.GraphEdge, error)
	ReviewGraphRecord(context.Context, core.GraphReview) error
	RecordGraphFeedback(context.Context, core.GraphFeedback) error
	DeleteGraphWorkspace(context.Context, core.GraphScope) error
}
