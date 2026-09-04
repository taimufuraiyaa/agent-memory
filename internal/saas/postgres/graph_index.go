package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var (
	ErrGraphRevisionConflict = errors.New("graph revision activation conflict")
	ErrGraphRevisionNotReady = errors.New("graph revision is not ready")
)

var _ contracts.GraphRepository = (*GraphIndexRepository)(nil)

type GraphIndexRepository struct {
	pool *pgxpool.Pool
}

func NewGraphIndexRepository(pool *pgxpool.Pool) *GraphIndexRepository {
	return &GraphIndexRepository{pool: pool}
}

func (r *GraphIndexRepository) UpsertGraphConfiguration(ctx context.Context, configuration core.GraphConfiguration) error {
	if err := configuration.Validate(); err != nil {
		return err
	}
	tx, err := r.begin(ctx, configuration.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO saas_graph_configurations (
		tenant_id,workspace_id,id,version,enabled,adapter_name,adapter_version,index_method,
		projection_version,artifact_schema_version,prompt_fingerprint,model_route,created_at,updated_at
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	ON CONFLICT(tenant_id,id) DO UPDATE SET version=excluded.version,enabled=excluded.enabled,
		adapter_name=excluded.adapter_name,adapter_version=excluded.adapter_version,index_method=excluded.index_method,
		projection_version=excluded.projection_version,artifact_schema_version=excluded.artifact_schema_version,
		prompt_fingerprint=excluded.prompt_fingerprint,model_route=excluded.model_route,updated_at=excluded.updated_at
	WHERE saas_graph_configurations.workspace_id=excluded.workspace_id`, configuration.Scope.TenantID,
		configuration.Scope.WorkspaceID, configuration.ID, configuration.Version, configuration.Enabled,
		configuration.AdapterName, configuration.AdapterVersion, configuration.IndexMethod,
		configuration.ProjectionVersion, configuration.ArtifactSchemaVersion, configuration.PromptFingerprint,
		configuration.ModelRoute, configuration.CreatedAt, configuration.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) CreateGraphRevision(ctx context.Context, revision core.GraphRevision) error {
	if err := revision.Validate(); err != nil {
		return err
	}
	tx, err := r.begin(ctx, revision.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `INSERT INTO saas_graph_revisions (
		tenant_id,workspace_id,id,configuration_id,base_revision_id,state,cutoff_sequence,
		cutoff_event_time,cutoff_digest,projection_hash,artifact_hash,previous_revision_id,created_at,updated_at
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,NULLIF($5,'')::uuid,$6,$7,$8,$9,$10,$11,NULLIF($12,'')::uuid,$13,$14)`,
		revision.Scope.TenantID, revision.Scope.WorkspaceID, revision.ID, revision.ConfigurationID,
		revision.BaseRevisionID, revision.State, revision.Cutoff.Sequence, nullableGraphTime(revision.Cutoff.EventTime),
		revision.Cutoff.Digest, revision.ProjectionHash, revision.ArtifactHash, revision.PreviousRevisionID,
		revision.CreatedAt, revision.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) EnqueueGraphJob(ctx context.Context, job core.GraphJob) (core.GraphJob, bool, error) {
	if err := validateHostedGraphJob(job); err != nil {
		return core.GraphJob{}, false, err
	}
	tx, err := r.begin(ctx, job.Scope)
	if err != nil {
		return core.GraphJob{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row := tx.QueryRow(ctx, `WITH inserted AS (
		INSERT INTO saas_graph_jobs(tenant_id,workspace_id,id,configuration_id,revision_id,idempotency_key,
			state,attempt,lease_owner,lease_expires_at,created_at,updated_at)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10,$11,$12)
		ON CONFLICT(tenant_id,workspace_id,configuration_id,idempotency_key) DO NOTHING
		RETURNING id::text,tenant_id::text,workspace_id::text,configuration_id::text,revision_id::text,
			idempotency_key,state,attempt,lease_owner,lease_expires_at,created_at,updated_at,true
	) SELECT * FROM inserted UNION ALL
	SELECT id::text,tenant_id::text,workspace_id::text,configuration_id::text,revision_id::text,
		idempotency_key,state,attempt,lease_owner,lease_expires_at,created_at,updated_at,false
	FROM saas_graph_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$4::uuid AND idempotency_key=$6
	LIMIT 1`, job.Scope.TenantID, job.Scope.WorkspaceID, job.ID, job.ConfigurationID, job.RevisionID,
		job.IdempotencyKey, job.State, job.Attempt, job.LeaseOwner, job.LeaseExpiresAt, job.CreatedAt, job.UpdatedAt)
	stored, created, err := scanHostedGraphJobWithCreated(row)
	if err != nil {
		return core.GraphJob{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.GraphJob{}, false, err
	}
	return stored, created, nil
}

func (r *GraphIndexRepository) ClaimGraphJobs(ctx context.Context, scope core.GraphScope, owner string, limit int, lease time.Duration, now time.Time) ([]core.GraphJob, error) {
	if strings.TrimSpace(owner) == "" || limit < 1 || limit > 100 || lease <= 0 {
		return nil, fmt.Errorf("invalid graph job claim")
	}
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `WITH candidates AS (
		SELECT id FROM saas_graph_jobs WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid
		AND (state='queued' OR (state='running' AND lease_expires_at<=$3))
		ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT $4
	) UPDATE saas_graph_jobs j SET state='running',attempt=j.attempt+1,lease_owner=$5,
		lease_expires_at=$6,updated_at=$3 FROM candidates c
	WHERE j.tenant_id=$1::uuid AND j.id=c.id
	RETURNING j.id::text,j.tenant_id::text,j.workspace_id::text,j.configuration_id::text,j.revision_id::text,
		j.idempotency_key,j.state,j.attempt,j.lease_owner,j.lease_expires_at,j.created_at,j.updated_at`,
		scope.TenantID, scope.WorkspaceID, now, limit, owner, now.Add(lease))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []core.GraphJob
	for rows.Next() {
		job, err := scanHostedGraphJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *GraphIndexRepository) CancelGraphJob(ctx context.Context, scope core.GraphScope, jobID string, now time.Time) error {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE saas_graph_jobs SET state='cancelled',lease_owner='',lease_expires_at=NULL,updated_at=$4
		WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state IN ('queued','running')`,
		scope.TenantID, scope.WorkspaceID, jobID, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("graph job cannot be cancelled")
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) ActivateGraphRevision(ctx context.Context, activation core.GraphActivation) error {
	if err := activation.Validate(); err != nil {
		return err
	}
	tx, err := r.begin(ctx, activation.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(active_revision_id::text,'') FROM saas_graph_configurations
		WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid FOR UPDATE`, activation.Scope.TenantID,
		activation.Scope.WorkspaceID, activation.ConfigurationID).Scan(&active); err != nil {
		return err
	}
	if active != activation.ExpectedRevision {
		if active == activation.CandidateRevision {
			return tx.Commit(ctx)
		}
		return ErrGraphRevisionConflict
	}
	var state core.GraphRevisionState
	if err := tx.QueryRow(ctx, `SELECT state FROM saas_graph_revisions WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid
		AND configuration_id=$3::uuid AND id=$4::uuid FOR UPDATE`, activation.Scope.TenantID,
		activation.Scope.WorkspaceID, activation.ConfigurationID, activation.CandidateRevision).Scan(&state); err != nil {
		return err
	}
	if state != core.GraphRevisionReady && state != core.GraphRevisionPrevious {
		return ErrGraphRevisionNotReady
	}
	if active != "" {
		if _, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET state='previous',updated_at=clock_timestamp()
			WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state='active'`,
			activation.Scope.TenantID, activation.Scope.WorkspaceID, active); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET state='active',previous_revision_id=NULLIF($5,'')::uuid,
		updated_at=clock_timestamp() WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND id=$4::uuid`,
		activation.Scope.TenantID, activation.Scope.WorkspaceID, activation.ConfigurationID,
		activation.CandidateRevision, active); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_graph_configurations SET previous_revision_id=active_revision_id,
		active_revision_id=$4::uuid,updated_at=clock_timestamp() WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`,
		activation.Scope.TenantID, activation.Scope.WorkspaceID, activation.ConfigurationID,
		activation.CandidateRevision); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) ActiveGraphRevisions(ctx context.Context, scope core.GraphScope, configurationID string) (string, string, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active, previous string
	err = tx.QueryRow(ctx, `SELECT COALESCE(active_revision_id::text,''),COALESCE(previous_revision_id::text,'')
		FROM saas_graph_configurations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`,
		scope.TenantID, scope.WorkspaceID, configurationID).Scan(&active, &previous)
	return active, previous, err
}

func (r *GraphIndexRepository) ImportGraphEntity(ctx context.Context, entity core.GraphEntity, version core.GraphEntityVersion, evidence []core.GraphEvidence) error {
	if len(evidence) == 0 {
		return errors.New("graph evidence is required")
	}
	if err := validateHostedGraphEntity(entity, version, evidence); err != nil {
		return err
	}
	aliases, err := json.Marshal(version.Aliases)
	if err != nil {
		return err
	}
	tx, err := r.begin(ctx, entity.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `INSERT INTO saas_graph_entities (
		tenant_id,workspace_id,id,trust,first_revision_id,last_revision_id,superseded_by,created_at,updated_at
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,NULLIF($7,'')::uuid,$8,$9)
	ON CONFLICT(tenant_id,id) DO UPDATE SET last_revision_id=excluded.last_revision_id,updated_at=excluded.updated_at
	WHERE saas_graph_entities.workspace_id=excluded.workspace_id`, entity.Scope.TenantID, entity.Scope.WorkspaceID,
		entity.ID, entity.Trust, entity.FirstRevisionID, entity.LastRevisionID, entity.SupersededBy,
		entity.CreatedAt, entity.UpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("graph entity scope conflict")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_entity_versions (
		tenant_id,workspace_id,entity_id,revision_id,external_id,name,entity_type,description,aliases,occurrence_count,degree
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,$11)
	ON CONFLICT(tenant_id,entity_id,revision_id) DO NOTHING`, entity.Scope.TenantID, entity.Scope.WorkspaceID,
		entity.ID, version.RevisionID, version.ExternalID, version.Name, version.EntityType, version.Description,
		aliases, version.OccurrenceCount, version.Degree); err != nil {
		return err
	}
	for _, item := range evidence {
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_entity_evidence (
			tenant_id,workspace_id,entity_id,revision_id,evidence_id,canonical_kind,canonical_id,
			canonical_fingerprint,locator,occurrence_count
		) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10)
		ON CONFLICT(tenant_id,entity_id,revision_id,evidence_id) DO NOTHING`, item.Scope.TenantID,
			item.Scope.WorkspaceID, entity.ID, version.RevisionID, item.ID, item.CanonicalKind,
			item.CanonicalID, item.CanonicalFingerprint, item.Locator, item.OccurrenceCount); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) ImportGraphEdge(ctx context.Context, edge core.GraphEdge, version core.GraphEdgeVersion, evidence []core.GraphEvidence) error {
	if len(evidence) == 0 {
		return errors.New("graph evidence is required")
	}
	if err := validateHostedGraphEdge(edge, version, evidence); err != nil {
		return err
	}
	tx, err := r.begin(ctx, edge.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `INSERT INTO saas_graph_edges (
		tenant_id,workspace_id,id,source_entity_id,target_entity_id,normalized_kind,external_kind,trust,
		first_revision_id,last_revision_id,created_at,updated_at
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9::uuid,$10::uuid,$11,$12)
	ON CONFLICT(tenant_id,id) DO UPDATE SET last_revision_id=excluded.last_revision_id,updated_at=excluded.updated_at
	WHERE saas_graph_edges.workspace_id=excluded.workspace_id`, edge.Scope.TenantID, edge.Scope.WorkspaceID,
		edge.ID, edge.SourceEntityID, edge.TargetEntityID, edge.NormalizedKind, edge.ExternalKind, edge.Trust,
		edge.FirstRevisionID, edge.LastRevisionID, edge.CreatedAt, edge.UpdatedAt)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("graph edge scope conflict")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_edge_versions (
		tenant_id,workspace_id,edge_id,revision_id,external_id,description,weight
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7)
	ON CONFLICT(tenant_id,edge_id,revision_id) DO NOTHING`, edge.Scope.TenantID, edge.Scope.WorkspaceID,
		edge.ID, version.RevisionID, version.ExternalID, version.Description, version.Weight); err != nil {
		return err
	}
	for _, item := range evidence {
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_edge_evidence (
			tenant_id,workspace_id,edge_id,revision_id,evidence_id,canonical_kind,canonical_id,
			canonical_fingerprint,locator,occurrence_count
		) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10)
		ON CONFLICT(tenant_id,edge_id,revision_id,evidence_id) DO NOTHING`, item.Scope.TenantID,
			item.Scope.WorkspaceID, edge.ID, version.RevisionID, item.ID, item.CanonicalKind,
			item.CanonicalID, item.CanonicalFingerprint, item.Locator, item.OccurrenceCount); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) ImportGraphCommunity(ctx context.Context, community core.GraphCommunity, members []contracts.GraphCommunityMember, report core.GraphReport) error {
	if report.ReviewVersion < 1 {
		report.ReviewVersion = 1
	}
	if err := validateHostedGraphCommunity(community, members, report); err != nil {
		return err
	}
	findings, err := json.Marshal(report.Findings)
	if err != nil {
		return err
	}
	tx, err := r.begin(ctx, community.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `INSERT INTO saas_graph_communities (
		tenant_id,workspace_id,id,revision_id,external_id,parent_id,level,entity_count,edge_count,source_count,unresolved_count
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11)
	ON CONFLICT(tenant_id,id) DO UPDATE SET revision_id=excluded.revision_id,external_id=excluded.external_id,
		parent_id=excluded.parent_id,level=excluded.level,entity_count=excluded.entity_count,
		edge_count=excluded.edge_count,source_count=excluded.source_count,unresolved_count=excluded.unresolved_count
	WHERE saas_graph_communities.workspace_id=excluded.workspace_id`, community.Scope.TenantID,
		community.Scope.WorkspaceID, community.ID, community.RevisionID, community.ExternalID, community.ParentID,
		community.Level, community.EntityCount, community.EdgeCount, community.SourceCount, community.UnresolvedCount)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("graph community scope conflict")
	}
	for _, member := range members {
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_community_members (
			tenant_id,workspace_id,community_id,revision_id,kind,target_id
		) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6)
		ON CONFLICT(tenant_id,community_id,revision_id,kind,target_id) DO NOTHING`, community.Scope.TenantID,
			community.Scope.WorkspaceID, community.ID, community.RevisionID, member.Kind, member.TargetID); err != nil {
			return err
		}
	}
	tag, err = tx.Exec(ctx, `INSERT INTO saas_graph_reports (
		tenant_id,workspace_id,id,community_id,revision_id,title,summary,findings,rank,trust,stale,evidence_count,unresolved_count
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13)
	ON CONFLICT(tenant_id,id) DO UPDATE SET revision_id=excluded.revision_id,title=excluded.title,
		summary=excluded.summary,findings=excluded.findings,rank=excluded.rank,stale=excluded.stale,
		evidence_count=excluded.evidence_count,unresolved_count=excluded.unresolved_count
	WHERE saas_graph_reports.workspace_id=excluded.workspace_id`, report.Scope.TenantID, report.Scope.WorkspaceID,
		report.ID, report.CommunityID, report.RevisionID, report.Title, report.Summary, findings,
		report.Rank, report.Trust, report.Stale, report.EvidenceCount, report.UnresolvedCount)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("graph report scope conflict")
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) MarkGraphReportStale(ctx context.Context, scope core.GraphScope, reportID string) error {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE saas_graph_reports SET stale=true WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`,
		scope.TenantID, scope.WorkspaceID, reportID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("graph report not found")
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) GraphReport(ctx context.Context, scope core.GraphScope, reportID string) (core.GraphReport, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return core.GraphReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var report core.GraphReport
	var findings []byte
	err = tx.QueryRow(ctx, `SELECT id::text,tenant_id::text,workspace_id::text,community_id::text,revision_id::text,
		title,summary,findings,rank,trust,stale,evidence_count,unresolved_count FROM saas_graph_reports
		WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, scope.TenantID, scope.WorkspaceID,
		reportID).Scan(&report.ID, &report.Scope.TenantID, &report.Scope.WorkspaceID, &report.CommunityID,
		&report.RevisionID, &report.Title, &report.Summary, &findings, &report.Rank, &report.Trust,
		&report.Stale, &report.EvidenceCount, &report.UnresolvedCount)
	if err != nil {
		return core.GraphReport{}, err
	}
	if err := json.Unmarshal(findings, &report.Findings); err != nil {
		return core.GraphReport{}, err
	}
	return report, nil
}

func (r *GraphIndexRepository) ListQueryableGraphEdges(ctx context.Context, scope core.GraphScope) ([]core.GraphEdge, error) {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id::text,tenant_id::text,workspace_id::text,source_entity_id::text,
		target_entity_id::text,normalized_kind,external_kind,trust,first_revision_id::text,last_revision_id::text,
		created_at,updated_at FROM saas_graph_edges e WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid
		AND trust IN ('proposed','reviewed','approved') AND EXISTS (
			SELECT 1 FROM saas_graph_edge_versions ev
			JOIN saas_graph_revisions r ON r.tenant_id=ev.tenant_id AND r.id=ev.revision_id
			JOIN saas_graph_configurations c ON c.tenant_id=r.tenant_id AND c.workspace_id=r.workspace_id AND c.id=r.configuration_id
			WHERE ev.tenant_id=e.tenant_id AND ev.edge_id=e.id AND ev.revision_id=c.active_revision_id
		) ORDER BY id`, scope.TenantID, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []core.GraphEdge
	for rows.Next() {
		var edge core.GraphEdge
		if err := rows.Scan(&edge.ID, &edge.Scope.TenantID, &edge.Scope.WorkspaceID, &edge.SourceEntityID,
			&edge.TargetEntityID, &edge.NormalizedKind, &edge.ExternalKind, &edge.Trust,
			&edge.FirstRevisionID, &edge.LastRevisionID, &edge.CreatedAt, &edge.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, edge)
	}
	return result, rows.Err()
}

func (r *GraphIndexRepository) ReviewGraphRecord(ctx context.Context, review core.GraphReview) error {
	if err := review.Scope.Validate(); err != nil {
		return err
	}
	if review.TargetKind != "entity" && review.TargetKind != "edge" && review.TargetKind != "report" {
		return fmt.Errorf("unsupported graph review target %q", review.TargetKind)
	}
	if err := core.ValidateGraphReviewAction(review); err != nil {
		return err
	}
	table := "saas_graph_entities"
	if review.TargetKind == "edge" {
		table = "saas_graph_edges"
	}
	tx, err := r.begin(ctx, review.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	versionColumn, timestampUpdate := "record_version", ",updated_at=clock_timestamp()"
	if review.TargetKind == "report" {
		table, versionColumn, timestampUpdate = "saas_graph_reports", "review_version", ",stale=true"
	}
	query := `UPDATE ` + table + ` SET trust=$4,` + versionColumn + `=` + versionColumn + `+1` + timestampUpdate + `
		WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND trust=$5 AND ` + versionColumn + `=$6`
	tag, err := tx.Exec(ctx, query, review.Scope.TenantID, review.Scope.WorkspaceID, review.TargetID,
		review.To, review.From, review.ExpectedVersion)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("graph review version conflict")
	}
	createdAt := review.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_reviews (
		tenant_id,workspace_id,id,action,target_kind,target_id,from_state,to_state,expected_version,reason,reviewer_id,created_at
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::uuid,$7,$8,$9,$10,$11::uuid,$12)`, review.Scope.TenantID,
		review.Scope.WorkspaceID, review.ID, review.Action, review.TargetKind, review.TargetID, review.From, review.To,
		review.ExpectedVersion, review.Reason, review.ReviewerID, createdAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) RecordGraphFeedback(ctx context.Context, feedback core.GraphFeedback) error {
	if err := feedback.Validate(); err != nil {
		return err
	}
	tx, err := r.begin(ctx, feedback.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	createdAt := feedback.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_feedback (
		tenant_id,workspace_id,id,request_id,target_kind,target_id,outcome,reason,created_at
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9)`, feedback.Scope.TenantID,
		feedback.Scope.WorkspaceID, feedback.ID, feedback.RequestID, feedback.TargetKind,
		feedback.TargetID, feedback.Outcome, feedback.Reason, createdAt); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) DeleteGraphWorkspace(ctx context.Context, scope core.GraphScope) error {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	statements := []string{
		`DELETE FROM saas_graph_feedback WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid`,
		`DELETE FROM saas_graph_reviews WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid`,
		`DELETE FROM saas_graph_reports WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid`,
		`DELETE FROM saas_graph_communities WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid`,
		`DELETE FROM saas_graph_edges WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid`,
		`DELETE FROM saas_graph_entities WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid`,
		`UPDATE saas_graph_configurations SET active_revision_id=NULL,previous_revision_id=NULL WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid`,
		`DELETE FROM saas_graph_configurations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(ctx, statement, scope.TenantID, scope.WorkspaceID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) begin(ctx context.Context, scope core.GraphScope) (pgx.Tx, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("graph PostgreSQL repository is not configured")
	}
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(scope.TenantID) == "" {
		return nil, fmt.Errorf("hosted graph scope requires tenant_id")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", scope.TenantID); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

func validateHostedGraphJob(job core.GraphJob) error {
	if err := job.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(job.Scope.TenantID) == "" || strings.TrimSpace(job.ID) == "" ||
		strings.TrimSpace(job.ConfigurationID) == "" || strings.TrimSpace(job.RevisionID) == "" ||
		strings.TrimSpace(job.IdempotencyKey) == "" || job.State == "" {
		return fmt.Errorf("invalid hosted graph job")
	}
	return nil
}

func validateHostedGraphEntity(entity core.GraphEntity, version core.GraphEntityVersion, evidence []core.GraphEvidence) error {
	if err := entity.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(entity.Scope.TenantID) == "" || strings.TrimSpace(entity.ID) == "" ||
		version.EntityID != entity.ID || version.RevisionID != entity.LastRevisionID ||
		strings.TrimSpace(version.Name) == "" || strings.TrimSpace(version.EntityType) == "" {
		return errors.New("invalid hosted graph entity")
	}
	return validateHostedGraphEvidence(entity.Scope, evidence)
}

func validateHostedGraphEdge(edge core.GraphEdge, version core.GraphEdgeVersion, evidence []core.GraphEvidence) error {
	if err := edge.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(edge.Scope.TenantID) == "" || strings.TrimSpace(edge.ID) == "" ||
		version.EdgeID != edge.ID || version.RevisionID != edge.LastRevisionID ||
		strings.TrimSpace(edge.SourceEntityID) == "" || strings.TrimSpace(edge.TargetEntityID) == "" ||
		strings.TrimSpace(edge.NormalizedKind) == "" || version.Weight < 0 || version.Weight > 1 {
		return errors.New("invalid hosted graph edge")
	}
	return validateHostedGraphEvidence(edge.Scope, evidence)
}

func validateHostedGraphEvidence(scope core.GraphScope, evidence []core.GraphEvidence) error {
	for _, item := range evidence {
		if item.Scope != scope || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.CanonicalKind) == "" ||
			strings.TrimSpace(item.CanonicalID) == "" || strings.TrimSpace(item.CanonicalFingerprint) == "" ||
			item.OccurrenceCount < 0 {
			return errors.New("invalid or cross-scope hosted graph evidence")
		}
	}
	return nil
}

func validateHostedGraphCommunity(community core.GraphCommunity, members []contracts.GraphCommunityMember, report core.GraphReport) error {
	if err := community.Scope.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(community.Scope.TenantID) == "" || strings.TrimSpace(community.ID) == "" ||
		strings.TrimSpace(community.RevisionID) == "" || len(members) == 0 || report.Scope != community.Scope ||
		report.CommunityID != community.ID || report.RevisionID != community.RevisionID ||
		strings.TrimSpace(report.ID) == "" || strings.TrimSpace(report.Title) == "" || strings.TrimSpace(report.Summary) == "" {
		return errors.New("invalid hosted graph community")
	}
	for _, member := range members {
		if (member.Kind != "entity" && member.Kind != "edge" && member.Kind != "text_unit") || strings.TrimSpace(member.TargetID) == "" {
			return errors.New("invalid hosted graph community member")
		}
	}
	return nil
}

type hostedGraphScanner interface{ Scan(...any) error }

func scanHostedGraphJob(scanner hostedGraphScanner) (core.GraphJob, error) {
	var job core.GraphJob
	var lease *time.Time
	err := scanner.Scan(&job.ID, &job.Scope.TenantID, &job.Scope.WorkspaceID, &job.ConfigurationID,
		&job.RevisionID, &job.IdempotencyKey, &job.State, &job.Attempt, &job.LeaseOwner,
		&lease, &job.CreatedAt, &job.UpdatedAt)
	job.LeaseExpiresAt = lease
	return job, err
}

func scanHostedGraphJobWithCreated(scanner hostedGraphScanner) (core.GraphJob, bool, error) {
	var job core.GraphJob
	var lease *time.Time
	var created bool
	err := scanner.Scan(&job.ID, &job.Scope.TenantID, &job.Scope.WorkspaceID, &job.ConfigurationID,
		&job.RevisionID, &job.IdempotencyKey, &job.State, &job.Attempt, &job.LeaseOwner,
		&lease, &job.CreatedAt, &job.UpdatedAt, &created)
	job.LeaseExpiresAt = lease
	return job, created, err
}

func nullableGraphTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
