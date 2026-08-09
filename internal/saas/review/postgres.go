package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) EvidenceTexts(ctx context.Context, request auth.RequestContext, workspaceID string, evidence []EvidenceRef) ([]string, error) {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return evidenceTexts(ctx, tx, request.TenantID, workspaceID, evidence)
}

func (r *PostgresRepository) Create(ctx context.Context, request auth.RequestContext, proposal Proposal) (Proposal, error) {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return Proposal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	texts, err := evidenceTexts(ctx, tx, request.TenantID, proposal.WorkspaceID, proposal.Evidence)
	if err != nil || len(texts) != len(proposal.Evidence) {
		return Proposal{}, ErrProposalForbidden
	}
	evidenceJSON, err := json.Marshal(proposal.Evidence)
	if err != nil {
		return Proposal{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_memory_proposals
		(tenant_id,id,workspace_id,requested_by,memory_type,content,transformation,transformation_version,evidence,status,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'suggested',$10,$10)`, request.TenantID, proposal.ID, proposal.WorkspaceID, request.AccountID, proposal.MemoryType, proposal.Content, proposal.Transformation, proposal.TransformationVersion, evidenceJSON, proposal.CreatedAt); err != nil {
		return Proposal{}, err
	}
	if err := proposalEvent(ctx, tx, request, "memory.proposal_created", "memory.proposal.create", proposal.ID, proposal.CreatedAt); err != nil {
		return Proposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}

func (r *PostgresRepository) Get(ctx context.Context, request auth.RequestContext, id string) (Proposal, error) {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return Proposal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return loadProposal(ctx, tx, request, id, false)
}

func (r *PostgresRepository) Update(ctx context.Context, request auth.RequestContext, id, content, transformation string, at time.Time) (Proposal, error) {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return Proposal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	proposal, err := loadProposal(ctx, tx, request, id, true)
	if err != nil || proposal.Status != core.ProposalSuggested {
		return Proposal{}, ErrProposalForbidden
	}
	texts, err := evidenceTexts(ctx, tx, request.TenantID, proposal.WorkspaceID, proposal.Evidence)
	if err != nil || rawSourceCopy(content, texts) {
		return Proposal{}, errors.New("proposal evidence or transformation is invalid")
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_memory_proposals SET content=$4,transformation=$5,updated_at=$6
		WHERE tenant_id=$1 AND id=$2 AND requested_by=$3 AND status='suggested'`, request.TenantID, id, request.AccountID, content, transformation, at); err != nil {
		return Proposal{}, err
	}
	if err := proposalEvent(ctx, tx, request, "memory.proposal_updated", "memory.proposal.update", id, at); err != nil {
		return Proposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Proposal{}, err
	}
	proposal.Content, proposal.Transformation, proposal.UpdatedAt = content, transformation, at
	return proposal, nil
}

func (r *PostgresRepository) Review(ctx context.Context, request auth.RequestContext, id string, accept bool, at time.Time) (Proposal, error) {
	tx, err := r.begin(ctx, request.TenantID)
	if err != nil {
		return Proposal{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	proposal, err := loadProposal(ctx, tx, request, id, true)
	if err != nil || proposal.Status != core.ProposalSuggested {
		return Proposal{}, ErrProposalForbidden
	}
	status, eventType, operation := core.ProposalRejected, "memory.proposal_rejected", "memory.proposal.reject"
	if accept {
		texts, err := evidenceTexts(ctx, tx, request.TenantID, proposal.WorkspaceID, proposal.Evidence)
		if err != nil || len(texts) != len(proposal.Evidence) || rawSourceCopy(proposal.Content, texts) {
			return Proposal{}, errors.New("proposal evidence is no longer valid")
		}
		memoryID, err := acceptMemory(ctx, tx, request, proposal, at)
		if err != nil {
			return Proposal{}, err
		}
		proposal.MemoryID = memoryID
		status, eventType, operation = core.ProposalAccepted, "memory.proposal_accepted", "memory.proposal.accept"
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_memory_proposals SET status=$4,memory_id=NULLIF($5,'')::uuid,reviewed_at=$6,updated_at=$6
		WHERE tenant_id=$1 AND id=$2 AND requested_by=$3 AND status='suggested'`, request.TenantID, id, request.AccountID, status, proposal.MemoryID, at); err != nil {
		return Proposal{}, err
	}
	if err := proposalEvent(ctx, tx, request, eventType, operation, id, at); err != nil {
		return Proposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Proposal{}, err
	}
	proposal.Status, proposal.ReviewedAt, proposal.UpdatedAt = status, &at, at
	return proposal, nil
}

func acceptMemory(ctx context.Context, tx pgx.Tx, request auth.RequestContext, proposal Proposal, at time.Time) (string, error) {
	hash := sha256.Sum256([]byte(request.TenantID + "|" + proposal.WorkspaceID + "|" + string(proposal.MemoryType) + "|" + proposal.Content))
	contentHash := hex.EncodeToString(hash[:])
	var memoryID string
	err := tx.QueryRow(ctx, `SELECT id::text FROM saas_memories WHERE tenant_id=$1 AND workspace_id=$2 AND content_hash=$3`, request.TenantID, proposal.WorkspaceID, contentHash).Scan(&memoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		memoryID = uuid.NewString()
		sourceJSON := []byte(`{"type":"consolidation"}`)
		idempotencyKey := "proposal:" + proposal.ID
		if _, err := tx.Exec(ctx, `INSERT INTO saas_memories
			(tenant_id,id,workspace_id,memory_type,content,content_hash,source_kind,source,entities,tags,keywords,confidence,storage_tier,idempotency_key,request_hash,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,'consolidation',$7,'{}','{}','[]',0.8,'vector',$8,$9,$10,$10)`,
			request.TenantID, memoryID, proposal.WorkspaceID, proposal.MemoryType, proposal.Content, contentHash, sourceJSON, idempotencyKey, contentHash, at); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	seen := map[string]struct{}{}
	for _, evidence := range proposal.Evidence {
		if _, exists := seen[evidence.SourceID]; exists {
			continue
		}
		seen[evidence.SourceID] = struct{}{}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_lineage_edges(tenant_id,id,from_type,from_id,to_type,to_id,transformation,transformation_version,created_at)
			VALUES($1,$2,'source',$3,'memory',$4,$5,$6,$7) ON CONFLICT DO NOTHING`, request.TenantID, uuid.NewString(), evidence.SourceID, memoryID, proposal.Transformation, proposal.TransformationVersion, at); err != nil {
			return "", err
		}
	}
	payload, _ := json.Marshal(map[string]string{"memory_id": memoryID, "proposal_id": proposal.ID})
	if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at)
		VALUES($1,$2,'memory.created','1.0','memory',$3,$4,$5,$5)`, request.TenantID, uuid.NewString(), memoryID, payload, at); err != nil {
		return "", err
	}
	return memoryID, nil
}

func evidenceTexts(ctx context.Context, tx pgx.Tx, tenantID, workspaceID string, evidence []EvidenceRef) ([]string, error) {
	texts := make([]string, 0, len(evidence))
	for _, ref := range evidence {
		var text string
		err := tx.QueryRow(ctx, `SELECT p.text_content FROM saas_source_passages p
			JOIN saas_sources s ON s.tenant_id=p.tenant_id AND s.id=p.source_id AND s.workspace_id=$2 AND s.active_version=p.source_version AND s.state='ready'
			JOIN saas_source_citations c ON c.tenant_id=p.tenant_id AND c.source_id=p.source_id AND c.source_version=p.source_version AND c.passage_id=p.id AND c.id=$6
			WHERE p.tenant_id=$1 AND p.source_id=$3 AND p.source_version=$4 AND p.id=$5`, tenantID, workspaceID, ref.SourceID, ref.SourceVersion, ref.PassageID, ref.CitationID).Scan(&text)
		if err != nil {
			return nil, err
		}
		texts = append(texts, text)
	}
	return texts, nil
}

func loadProposal(ctx context.Context, tx pgx.Tx, request auth.RequestContext, id string, lock bool) (Proposal, error) {
	query := `SELECT id::text,workspace_id::text,requested_by::text,memory_type,content,transformation,transformation_version,evidence,status,COALESCE(memory_id::text,''),created_at,updated_at,reviewed_at
		FROM saas_memory_proposals WHERE tenant_id=$1 AND id=$2 AND requested_by=$3`
	if lock {
		query += " FOR UPDATE"
	}
	var proposal Proposal
	var evidenceJSON []byte
	if err := tx.QueryRow(ctx, query, request.TenantID, id, request.AccountID).Scan(&proposal.ID, &proposal.WorkspaceID, &proposal.RequestedBy, &proposal.MemoryType, &proposal.Content, &proposal.Transformation, &proposal.TransformationVersion, &evidenceJSON, &proposal.Status, &proposal.MemoryID, &proposal.CreatedAt, &proposal.UpdatedAt, &proposal.ReviewedAt); err != nil {
		return Proposal{}, err
	}
	if err := json.Unmarshal(evidenceJSON, &proposal.Evidence); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}

func proposalEvent(ctx context.Context, tx pgx.Tx, request auth.RequestContext, eventType, operation, proposalID string, at time.Time) error {
	payload, _ := json.Marshal(map[string]string{"proposal_id": proposalID})
	if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at)
		VALUES($1,$2,$3,'1.0','memory_proposal',$4,$5,$6,$6)`, request.TenantID, uuid.NewString(), eventType, proposalID, payload, at); err != nil {
		return fmt.Errorf("append proposal outbox: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_audit_events(tenant_id,id,actor_type,actor_id,operation,outcome,request_id,correlation_id,target_type,target_id,occurred_at)
		VALUES($1,$2,'member',$3,$4,'success',$5,$6,'memory_proposal',$7,$8)`, request.TenantID, uuid.NewString(), request.AccountID, operation, request.RequestID, request.TraceID, proposalID, at); err != nil {
		return fmt.Errorf("append proposal audit: %w", err)
	}
	return nil
}

func (r *PostgresRepository) begin(ctx context.Context, tenant string) (pgx.Tx, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("memory review repository is not configured")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", tenant); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
