package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, now: now}
}

func (s *Service) Record(ctx context.Context, request auth.RequestContext, service, operation, outcome, targetType, targetID, reason string, metadata map[string]any) error {
	if s == nil || s.pool == nil || request.TenantID == "" {
		return errors.New("audit service is not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", request.TenantID); err != nil {
		return err
	}
	err = Append(ctx, tx, Event{TenantID: request.TenantID, OccurredAt: s.now().UTC(), ActorType: actorType(request), ActorID: request.AccountID, CredentialRef: request.CredentialID, SessionRef: request.SessionID, Service: service, Operation: operation, Outcome: outcome, RequestID: request.RequestID, TraceID: request.TraceID, TargetType: targetType, TargetID: targetID, PolicyVersion: "hosted-v1", ReasonCode: reason, SafeMetadata: metadata})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AuthorizationDenied records an authenticated tenant-selection mismatch under
// the subject's authoritative tenant. The probed identifier is irreversibly
// tokenized before it reaches the ledger.
func (s *Service) AuthorizationDenied(ctx context.Context, subjectID, selectedTenantID, requestID, traceID string) {
	if s == nil || s.pool == nil || subjectID == "" || selectedTenantID == "" {
		return
	}
	var tenantID, accountID string
	if err := s.pool.QueryRow(ctx, `SELECT t.id::text,a.id::text FROM saas_accounts a JOIN saas_tenants t ON t.personal_owner_account_id=a.id WHERE a.external_subject=$1`, subjectID).Scan(&tenantID, &accountID); err != nil {
		return
	}
	hash := sha256.Sum256([]byte(selectedTenantID))
	request := auth.RequestContext{TenantID: tenantID, AccountID: accountID, RequestID: requestID, TraceID: traceID}
	_ = s.Record(ctx, request, "api", "authorization.tenant_select", "denied", "tenant_probe", hex.EncodeToString(hash[:16]), "cross_tenant", map[string]any{"selection_present": true})
}

func actorType(request auth.RequestContext) string {
	if request.CredentialID != "" {
		return "agent_credential"
	}
	return "member"
}

func NewRequestIDs() (string, string) { return uuid.NewString(), uuid.NewString() }
