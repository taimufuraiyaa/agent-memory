package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/graphworker"
)

var _ contracts.GraphRevisionBatchStore = (*GraphIndexRepository)(nil)

func (r *GraphIndexRepository) ImportGraphRevisionBatch(ctx context.Context, batch contracts.GraphRevisionImportBatch) error {
	if err := batch.Scope.Validate(); err != nil {
		return err
	}
	if batch.Scope.TenantID == "" || batch.ConfigurationID == "" || batch.RevisionID == "" || batch.ExpectedEntities != len(batch.Entities) || batch.ExpectedEdges != len(batch.Edges) || batch.ExpectedCommunities != len(batch.Communities) {
		return fmt.Errorf("invalid hosted graph revision batch")
	}
	tx, err := r.begin(ctx, batch.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var state core.GraphRevisionState
	if err := tx.QueryRow(ctx, `SELECT state FROM saas_graph_revisions WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND id=$4::uuid FOR UPDATE`, batch.Scope.TenantID, batch.Scope.WorkspaceID, batch.ConfigurationID, batch.RevisionID).Scan(&state); err != nil {
		return err
	}
	if state != core.GraphRevisionImporting {
		return fmt.Errorf("hosted graph revision must be importing")
	}
	for _, record := range batch.Entities {
		entity, version := record.Entity, record.Version
		aliases, err := json.Marshal(version.Aliases)
		if err != nil {
			return err
		}
		if entity.Scope != batch.Scope || entity.LastRevisionID != batch.RevisionID || version.RevisionID != batch.RevisionID || len(record.Evidence) == 0 {
			return fmt.Errorf("invalid hosted graph entity record")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_entities(tenant_id,workspace_id,id,trust,first_revision_id,last_revision_id,superseded_by,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,NULLIF($7,'')::uuid,$8,$9) ON CONFLICT(tenant_id,id) DO UPDATE SET last_revision_id=excluded.last_revision_id,updated_at=excluded.updated_at WHERE saas_graph_entities.workspace_id=excluded.workspace_id`, batch.Scope.TenantID, batch.Scope.WorkspaceID, entity.ID, entity.Trust, entity.FirstRevisionID, entity.LastRevisionID, entity.SupersededBy, entity.CreatedAt, entity.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_entity_versions(tenant_id,workspace_id,entity_id,revision_id,external_id,name,entity_type,description,aliases,occurrence_count,degree) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9::jsonb,$10,$11) ON CONFLICT DO NOTHING`, batch.Scope.TenantID, batch.Scope.WorkspaceID, version.EntityID, version.RevisionID, version.ExternalID, version.Name, version.EntityType, version.Description, string(aliases), version.OccurrenceCount, version.Degree); err != nil {
			return err
		}
		for _, evidence := range record.Evidence {
			if evidence.Scope != batch.Scope {
				return fmt.Errorf("cross-scope graph entity evidence")
			}
			if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_entity_evidence(tenant_id,workspace_id,entity_id,revision_id,evidence_id,canonical_kind,canonical_id,canonical_fingerprint,locator,occurrence_count) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, batch.Scope.TenantID, batch.Scope.WorkspaceID, entity.ID, version.RevisionID, hostedGraphDerivedUUID("evidence", evidence.ID), evidence.CanonicalKind, evidence.CanonicalID, evidence.CanonicalFingerprint, evidence.Locator, evidence.OccurrenceCount); err != nil {
				return err
			}
		}
	}
	for _, record := range batch.Edges {
		edge, version := record.Edge, record.Version
		if edge.Scope != batch.Scope || edge.LastRevisionID != batch.RevisionID || version.RevisionID != batch.RevisionID || len(record.Evidence) == 0 {
			return fmt.Errorf("invalid hosted graph edge record")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_edges(tenant_id,workspace_id,id,source_entity_id,target_entity_id,normalized_kind,external_kind,trust,first_revision_id,last_revision_id,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9::uuid,$10::uuid,$11,$12) ON CONFLICT(tenant_id,id) DO UPDATE SET last_revision_id=excluded.last_revision_id,updated_at=excluded.updated_at WHERE saas_graph_edges.workspace_id=excluded.workspace_id`, batch.Scope.TenantID, batch.Scope.WorkspaceID, edge.ID, edge.SourceEntityID, edge.TargetEntityID, edge.NormalizedKind, edge.ExternalKind, edge.Trust, edge.FirstRevisionID, edge.LastRevisionID, edge.CreatedAt, edge.UpdatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_edge_versions(tenant_id,workspace_id,edge_id,revision_id,external_id,description,weight,origin,provenance_approved) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`, batch.Scope.TenantID, batch.Scope.WorkspaceID, edge.ID, version.RevisionID, version.ExternalID, version.Description, version.Weight, version.Origin, version.ProvenanceApproved); err != nil {
			return err
		}
		for _, evidence := range record.Evidence {
			if evidence.Scope != batch.Scope {
				return fmt.Errorf("cross-scope graph edge evidence")
			}
			if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_edge_evidence(tenant_id,workspace_id,edge_id,revision_id,evidence_id,canonical_kind,canonical_id,canonical_fingerprint,locator,occurrence_count) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, batch.Scope.TenantID, batch.Scope.WorkspaceID, edge.ID, version.RevisionID, hostedGraphDerivedUUID("evidence", evidence.ID), evidence.CanonicalKind, evidence.CanonicalID, evidence.CanonicalFingerprint, evidence.Locator, evidence.OccurrenceCount); err != nil {
				return err
			}
		}
	}
	for _, record := range batch.Communities {
		community, report := record.Community, record.Report
		findings, err := json.Marshal(report.Findings)
		if err != nil {
			return err
		}
		if community.Scope != batch.Scope || community.RevisionID != batch.RevisionID || report.Scope != batch.Scope || report.RevisionID != batch.RevisionID {
			return fmt.Errorf("invalid hosted graph community record")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_communities(tenant_id,workspace_id,id,configuration_id,revision_id,external_id,parent_id,level,entity_count,edge_count,source_count,unresolved_count,membership_fingerprint,evidence_fingerprint) VALUES($1::uuid,$2::uuid,$3::uuid,NULLIF($4,'')::uuid,$5::uuid,$6,NULLIF($7,'')::uuid,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(tenant_id,id) DO UPDATE SET revision_id=excluded.revision_id,external_id=excluded.external_id,parent_id=excluded.parent_id,level=excluded.level,entity_count=excluded.entity_count,edge_count=excluded.edge_count,source_count=excluded.source_count,unresolved_count=excluded.unresolved_count,membership_fingerprint=excluded.membership_fingerprint,evidence_fingerprint=excluded.evidence_fingerprint WHERE saas_graph_communities.workspace_id=excluded.workspace_id`, batch.Scope.TenantID, batch.Scope.WorkspaceID, community.ID, community.ConfigurationID, community.RevisionID, community.ExternalID, community.ParentID, community.Level, community.EntityCount, community.EdgeCount, community.SourceCount, community.UnresolvedCount, community.MembershipFingerprint, community.EvidenceFingerprint); err != nil {
			return err
		}
		for _, member := range record.Members {
			if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_community_members(tenant_id,workspace_id,community_id,revision_id,kind,target_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6) ON CONFLICT DO NOTHING`, batch.Scope.TenantID, batch.Scope.WorkspaceID, community.ID, community.RevisionID, member.Kind, member.TargetID); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_reports(tenant_id,workspace_id,id,community_id,revision_id,title,summary,findings,rank,trust,admission_state,stale,evidence_count,unresolved_count,model_route,model_fingerprint,prompt_fingerprint,membership_fingerprint,evidence_fingerprint,review_version) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8::jsonb,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20) ON CONFLICT(tenant_id,id) DO UPDATE SET revision_id=excluded.revision_id,title=excluded.title,summary=excluded.summary,findings=excluded.findings,rank=excluded.rank,trust=excluded.trust,admission_state=excluded.admission_state,stale=excluded.stale,evidence_count=excluded.evidence_count,unresolved_count=excluded.unresolved_count,model_route=excluded.model_route,model_fingerprint=excluded.model_fingerprint,prompt_fingerprint=excluded.prompt_fingerprint,membership_fingerprint=excluded.membership_fingerprint,evidence_fingerprint=excluded.evidence_fingerprint,review_version=excluded.review_version WHERE saas_graph_reports.workspace_id=excluded.workspace_id`, batch.Scope.TenantID, batch.Scope.WorkspaceID, report.ID, report.CommunityID, report.RevisionID, report.Title, report.Summary, string(findings), report.Rank, report.Trust, report.AdmissionState, report.Stale, report.EvidenceCount, report.UnresolvedCount, report.ModelRoute, report.ModelFingerprint, report.PromptFingerprint, report.MembershipFingerprint, report.EvidenceFingerprint, report.ReviewVersion); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET state='ready',updated_at=clock_timestamp() WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND id=$4::uuid AND state='importing'`, batch.Scope.TenantID, batch.Scope.WorkspaceID, batch.ConfigurationID, batch.RevisionID)
	if err != nil || tag.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("hosted graph revision transition conflict")
		}
		return err
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) ClaimGraphCompletion(ctx context.Context, event graphworker.CompletionEvent, owner string, lease time.Duration, now time.Time) (bool, error) {
	tx, err := r.begin(ctx, event.Scope)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var claimed bool
	err = tx.QueryRow(ctx, `INSERT INTO saas_graph_completion_events(tenant_id,workspace_id,event_id,job_id,revision_id,state,lease_owner,lease_expires_at,created_at,updated_at) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5::uuid,'processing',$6,$7,$8,$8) ON CONFLICT(tenant_id,event_id) DO UPDATE SET lease_owner=excluded.lease_owner,lease_expires_at=excluded.lease_expires_at,updated_at=excluded.updated_at WHERE saas_graph_completion_events.state='processing' AND (saas_graph_completion_events.lease_owner=excluded.lease_owner OR saas_graph_completion_events.lease_expires_at<=excluded.updated_at) RETURNING true`, event.Scope.TenantID, event.Scope.WorkspaceID, event.ID, event.JobID, event.RevisionID, owner, now.Add(lease), now).Scan(&claimed)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, tx.Commit(ctx)
		}
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return claimed, nil
}

func (r *GraphIndexRepository) FinishGraphCompletion(ctx context.Context, event graphworker.CompletionEvent, owner string, now time.Time) error {
	tx, err := r.begin(ctx, event.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE saas_graph_completion_events SET state='completed',completed_at=$5,updated_at=$5,lease_expires_at=NULL WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND event_id=$3 AND lease_owner=$4 AND state='processing'`, event.Scope.TenantID, event.Scope.WorkspaceID, event.ID, owner, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("graph completion lease conflict")
	}
	if tag, err = tx.Exec(ctx, `UPDATE saas_graph_jobs SET state='completed',failure_code='',lease_owner='',lease_expires_at=NULL,updated_at=$4 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state='running'`, event.Scope.TenantID, event.Scope.WorkspaceID, event.JobID, now); err != nil || tag.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("graph job completion state conflict")
		}
		return err
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) PrepareGraphImport(ctx context.Context, event graphworker.CompletionEvent, manifest contracts.GraphArtifactManifest, now time.Time) (bool, error) {
	tx, err := r.begin(ctx, event.Scope)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var adapterName, adapterVersion, artifactSchema, promptFingerprint, indexMethod string
	if err := tx.QueryRow(ctx, `SELECT adapter_name,adapter_version,artifact_schema_version,prompt_fingerprint,index_method FROM saas_graph_configurations WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid`, event.Scope.TenantID, event.Scope.WorkspaceID, event.ConfigurationID).Scan(&adapterName, &adapterVersion, &artifactSchema, &promptFingerprint, &indexMethod); err != nil {
		return false, err
	}
	if manifest.AdapterName != adapterName || manifest.AdapterVersion != adapterVersion || manifest.ArtifactSchemaVersion != artifactSchema || manifest.PromptFingerprint != promptFingerprint || string(manifest.IndexMethod) != indexMethod {
		return false, fmt.Errorf("graph artifact configuration identity mismatch")
	}
	var state core.GraphRevisionState
	if err := tx.QueryRow(ctx, `SELECT state FROM saas_graph_revisions WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND id=$4::uuid FOR UPDATE`, event.Scope.TenantID, event.Scope.WorkspaceID, event.ConfigurationID, event.RevisionID).Scan(&state); err != nil {
		return false, err
	}
	switch state {
	case core.GraphRevisionReady, core.GraphRevisionActive:
		return false, tx.Commit(ctx)
	case core.GraphRevisionImporting:
		return true, tx.Commit(ctx)
	case core.GraphRevisionIndexing:
	default:
		return false, fmt.Errorf("graph revision is not resumable for import")
	}
	tag, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET state='validating',updated_at=$5 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND configuration_id=$3::uuid AND id=$4::uuid AND state='indexing'`, event.Scope.TenantID, event.Scope.WorkspaceID, event.ConfigurationID, event.RevisionID, now)
	if err != nil || tag.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("graph revision validation transition conflict")
		}
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_graph_revisions SET state='importing',updated_at=$4 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state='validating'`, event.Scope.TenantID, event.Scope.WorkspaceID, event.RevisionID, now); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (r *GraphIndexRepository) RecordGraphFailure(ctx context.Context, event graphworker.CompletionEvent, now time.Time) error {
	tx, err := r.begin(ctx, event.Scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE saas_graph_jobs SET failure_code=$4,updated_at=$5 WHERE tenant_id=$1::uuid AND workspace_id=$2::uuid AND id=$3::uuid AND state='running'`, event.Scope.TenantID, event.Scope.WorkspaceID, event.JobID, event.FailureCode, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *GraphIndexRepository) AppendGraphOperatorAudit(ctx context.Context, scope core.GraphScope, operation, actor, requestID string, metadata map[string]string) error {
	tx, err := r.begin(ctx, scope)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	safe := map[string]any{}
	for key, value := range metadata {
		safe[key] = value
	}
	err = audit.Append(ctx, tx, audit.Event{TenantID: scope.TenantID, ID: uuid.NewString(), OccurredAt: time.Now().UTC(), ActorType: "service", ActorID: actor, Service: "graph-index", Operation: operation, Outcome: "success", RequestID: requestID, TraceID: requestID, TargetType: "workspace", TargetID: scope.WorkspaceID, PolicyVersion: "graph-operator-v1", ReasonCode: "authorized", SafeMetadata: safe})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func hostedGraphDerivedUUID(kind, value string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(kind+"\x00"+value)).String()
}
