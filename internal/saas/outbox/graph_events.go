package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type GraphChangeInput struct {
	TenantID           string
	WorkspaceID        string
	SubjectKind        string
	SubjectID          string
	SubjectFingerprint string
	ChangeKind         string
	OccurredAt         time.Time
}

type GraphEventConfiguration struct {
	ID                string
	Version           int64
	ProjectionVersion string
}

type GraphOutboxEvent struct {
	ID          string          `json:"id"`
	TenantID    string          `json:"tenant_id"`
	AggregateID string          `json:"aggregate_id"`
	Payload     json.RawMessage `json:"payload"`
	OccurredAt  time.Time       `json:"occurred_at"`
}

// BuildGraphOutboxEvents creates content-free, deterministic events. Replaying
// the same canonical fingerprint for the same graph configuration yields the
// same UUID and therefore coalesces at the outbox primary key.
func BuildGraphOutboxEvents(input GraphChangeInput, configurations []GraphEventConfiguration) ([]GraphOutboxEvent, error) {
	for _, value := range []string{input.TenantID, input.WorkspaceID, input.SubjectKind, input.SubjectID, input.SubjectFingerprint, input.ChangeKind} {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("graph outbox identity is incomplete")
		}
	}
	if input.OccurredAt.IsZero() {
		return nil, fmt.Errorf("graph outbox occurred_at is required")
	}
	ordered := append([]GraphEventConfiguration(nil), configurations...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	events := make([]GraphOutboxEvent, 0, len(ordered))
	for _, configuration := range ordered {
		if strings.TrimSpace(configuration.ID) == "" || configuration.Version < 1 || strings.TrimSpace(configuration.ProjectionVersion) == "" {
			return nil, fmt.Errorf("graph outbox configuration is invalid")
		}
		identity := strings.Join([]string{input.TenantID, input.WorkspaceID, input.SubjectKind, input.SubjectID, input.SubjectFingerprint, configuration.ProjectionVersion, configuration.ID, fmt.Sprint(configuration.Version)}, "\x00")
		payload, err := json.Marshal(map[string]any{
			"workspace_id": input.WorkspaceID, "subject_kind": input.SubjectKind,
			"subject_fingerprint": input.SubjectFingerprint, "change_kind": input.ChangeKind,
			"configuration_id": configuration.ID, "configuration_version": configuration.Version,
			"projection_version": configuration.ProjectionVersion,
		})
		if err != nil {
			return nil, err
		}
		events = append(events, GraphOutboxEvent{
			ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)).String(), TenantID: input.TenantID,
			AggregateID: input.SubjectID, Payload: payload, OccurredAt: input.OccurredAt.UTC(),
		})
	}
	return events, nil
}

// AppendGraphChangeEventsTx is called inside the canonical PostgreSQL
// transaction. Downstream scheduling and GraphRAG execution are deliberately
// absent from this function.
func AppendGraphChangeEventsTx(ctx context.Context, tx pgx.Tx, input GraphChangeInput) error {
	rows, err := tx.Query(ctx, `SELECT id::text,version,projection_version FROM saas_graph_configurations WHERE tenant_id=$1 AND workspace_id=$2 AND enabled=true ORDER BY version,id`, input.TenantID, input.WorkspaceID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var configurations []GraphEventConfiguration
	for rows.Next() {
		var configuration GraphEventConfiguration
		if err := rows.Scan(&configuration.ID, &configuration.Version, &configuration.ProjectionVersion); err != nil {
			return err
		}
		configurations = append(configurations, configuration)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	events, err := BuildGraphOutboxEvents(input, configurations)
	if err != nil {
		return err
	}
	for _, event := range events {
		if _, err := tx.Exec(ctx, `INSERT INTO saas_outbox(tenant_id,id,event_type,spec_version,aggregate_type,aggregate_id,payload,occurred_at,next_attempt_at) VALUES($1,$2,'graph.change_requested','1.0','graph_subject',$3,$4,$5,$5) ON CONFLICT(tenant_id,id) DO NOTHING`, event.TenantID, event.ID, event.AggregateID, event.Payload, event.OccurredAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO saas_graph_change_journal(tenant_id,workspace_id,id,configuration_id,subject_kind,subject_id,subject_fingerprint,projection_version,configuration_version,change_kind,occurred_at)
			VALUES($1::uuid,($4::jsonb->>'workspace_id')::uuid,$2::uuid,($4::jsonb->>'configuration_id')::uuid,$4::jsonb->>'subject_kind',$3,$4::jsonb->>'subject_fingerprint',$4::jsonb->>'projection_version',$4::jsonb->>'configuration_version',$4::jsonb->>'change_kind',$5)
			ON CONFLICT(tenant_id,id) DO NOTHING`, event.TenantID, event.ID, event.AggregateID, event.Payload, event.OccurredAt); err != nil {
			return err
		}
	}
	return nil
}
