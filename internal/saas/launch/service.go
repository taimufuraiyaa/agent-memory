// Package launch enforces staged alpha, beta, and public rollout controls.
package launch

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
)

var (
	ErrSignupClosed       = errors.New("signup is not available")
	ErrInvitationRequired = errors.New("a valid invitation is required")
	ErrGeographyBlocked   = errors.New("signup is not available in this country")
	ErrAgeRestricted      = errors.New("minimum age confirmation is required")
	ErrSignupRateLimited  = errors.New("signup rate limit exceeded")
	ErrAccountCapReached  = errors.New("signup account cap reached")
	ErrFeatureDisabled    = errors.New("feature is disabled by rollout policy")
)

type Policy struct {
	Phase, Version                               string
	SignupEnabled, InvitationRequired            bool
	AllowedCountries                             []string
	MinimumAge, AccountCap, TrialDays, SourceCap int
	SignupRatePerHour, AbuseRejectionLimit       int
}

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

func (s *Service) CreateInvitation(ctx context.Context, email, createdBy string, maxUses int, expiresAt time.Time) (string, error) {
	if s == nil || s.pool == nil || !strings.Contains(email, "@") || strings.TrimSpace(createdBy) == "" || maxUses < 1 || !expiresAt.After(s.now()) {
		return "", errors.New("invalid invitation")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	token := "am_inv_" + base64.RawURLEncoding.EncodeToString(secret)
	_, err := s.pool.Exec(ctx, `INSERT INTO saas_launch_invitations(token_sha256,email_sha256,state,max_uses,expires_at,created_by,created_at) VALUES($1,$2,'active',$3,$4,$5,$6)`, digest(token), digest(strings.ToLower(strings.TrimSpace(email))), maxUses, expiresAt.UTC(), strings.TrimSpace(createdBy), s.now().UTC())
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) SetSignupEnabled(ctx context.Context, actorID, reason string, enabled bool) error {
	actorID, reason = strings.TrimSpace(actorID), strings.TrimSpace(reason)
	if s == nil || s.pool == nil || actorID == "" || reason == "" || len(actorID) > 128 || len(reason) > 128 {
		return errors.New("invalid signup policy change")
	}
	_, err := s.pool.Exec(ctx, `UPDATE saas_launch_policy SET signup_enabled=$1,updated_by=$2,reason_code=$3,updated_at=$4 WHERE singleton=true`, enabled, actorID, reason, s.now().UTC())
	return err
}

// Reserve implements control.SignupAdmission. It stores hashes only and holds
// one invitation/account-cap slot until signup commits or the reservation expires.
func (s *Service) Reserve(ctx context.Context, identity control.VerifiedIdentity, input control.SignupContext) (string, error) {
	if s == nil || s.pool == nil {
		return "", ErrSignupClosed
	}
	now := s.now().UTC()
	emailHash, subjectHash := digest(strings.ToLower(strings.TrimSpace(identity.Email))), digest(identity.ExternalSubject)
	networkHash := digest(normalizeNetwork(input.NetworkAddress))
	country := strings.ToUpper(strings.TrimSpace(input.Country))
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	policy, err := loadPolicy(ctx, tx)
	if err != nil {
		return "", err
	}
	reject := func(reason string, cause error) (string, error) {
		_, _ = tx.Exec(ctx, `INSERT INTO saas_signup_attempts(id,email_sha256,network_sha256,country,phase,outcome,reason_code,occurred_at) VALUES($1,$2,$3,$4,$5,'rejected',$6,$7)`, uuid.NewString(), emailHash, networkHash, country, policy.Phase, reason, now)
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return "", commitErr
		}
		return "", cause
	}
	if !policy.SignupEnabled {
		return reject("signup_closed", ErrSignupClosed)
	}
	if !input.AgeConfirmed {
		return reject("age_confirmation_missing", ErrAgeRestricted)
	}
	if !input.CountryVerified || !contains(policy.AllowedCountries, country) {
		return reject("geography_blocked", ErrGeographyBlocked)
	}
	var attempts, rejections int
	if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE outcome='rejected') FROM saas_signup_attempts WHERE network_sha256=$1 AND occurred_at>=$2`, networkHash, now.Add(-time.Hour)).Scan(&attempts, &rejections); err != nil {
		return "", err
	}
	if attempts >= policy.SignupRatePerHour || rejections >= policy.AbuseRejectionLimit {
		return reject("rate_or_abuse_limit", ErrSignupRateLimited)
	}
	var accounts, reservations int
	if err := tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM saas_accounts),(SELECT count(*) FROM saas_signup_reservations WHERE state='reserved' AND expires_at>$1)`, now).Scan(&accounts, &reservations); err != nil {
		return "", err
	}
	if accounts+reservations >= policy.AccountCap {
		return reject("account_cap", ErrAccountCapReached)
	}
	var invitationHash any
	if policy.InvitationRequired {
		hash := digest(strings.TrimSpace(input.InvitationToken))
		var state, invitedEmail string
		var maxUses, reservedUses, completedUses int
		var expiresAt time.Time
		if err := tx.QueryRow(ctx, `SELECT state,email_sha256,max_uses,reserved_uses,completed_uses,expires_at FROM saas_launch_invitations WHERE token_sha256=$1 FOR UPDATE`, hash).Scan(&state, &invitedEmail, &maxUses, &reservedUses, &completedUses, &expiresAt); err != nil || state != "active" || invitedEmail != emailHash || !expiresAt.After(now) || reservedUses+completedUses >= maxUses {
			return reject("invitation_invalid", ErrInvitationRequired)
		}
		if _, err := tx.Exec(ctx, `UPDATE saas_launch_invitations SET reserved_uses=reserved_uses+1 WHERE token_sha256=$1`, hash); err != nil {
			return "", err
		}
		invitationHash = hash
	}
	id := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO saas_signup_reservations(id,external_subject_sha256,email_sha256,network_sha256,invitation_sha256,country,policy_version,state,reserved_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,'reserved',$8,$9)`, id, subjectHash, emailHash, networkHash, invitationHash, country, policy.Version, now, now.Add(10*time.Minute)); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_signup_attempts(id,email_sha256,network_sha256,country,phase,outcome,reason_code,occurred_at) VALUES($1,$2,$3,$4,$5,'reserved','policy_passed',$6)`, uuid.NewString(), emailHash, networkHash, country, policy.Phase, now); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Service) Commit(ctx context.Context, reservationID string, account control.PersonalAccount) error {
	now := s.now().UTC()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var invitation *string
	var policyVersion, state string
	if err := tx.QueryRow(ctx, `SELECT invitation_sha256,policy_version,state FROM saas_signup_reservations WHERE id=$1 FOR UPDATE`, reservationID).Scan(&invitation, &policyVersion, &state); err != nil || state != "reserved" {
		return ErrSignupClosed
	}
	policy, err := loadPolicy(ctx, tx)
	if err != nil {
		return err
	}
	if invitation != nil {
		if _, err := tx.Exec(ctx, `UPDATE saas_launch_invitations SET reserved_uses=reserved_uses-1,completed_uses=completed_uses+1,state=CASE WHEN completed_uses+1>=max_uses THEN 'exhausted' ELSE state END WHERE token_sha256=$1`, *invitation); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_signup_reservations SET state='completed',completed_at=$2 WHERE id=$1`, reservationID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", account.TenantID); err != nil {
		return err
	}
	flags := `{"source_upload":true,"generation":true,"exports":true}`
	if _, err := tx.Exec(ctx, `INSERT INTO saas_tenant_launch_controls(tenant_id,source_cap,trial_expires_at,feature_flags,policy_version,updated_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(tenant_id) DO NOTHING`, account.TenantID, policy.SourceCap, now.Add(time.Duration(policy.TrialDays)*24*time.Hour), flags, policyVersion, now); err != nil {
		return err
	}
	trialEnd := now.Add(time.Duration(policy.TrialDays) * 24 * time.Hour)
	if _, err := tx.Exec(ctx, `UPDATE saas_tenant_entitlements SET max_source_count=LEAST(max_source_count,$2),updated_at=$3 WHERE tenant_id=$1`, account.TenantID, policy.SourceCap, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE saas_subscriptions SET current_period_ends_at=$2,updated_at=$3 WHERE tenant_id=$1 AND state='trialing'`, account.TenantID, trialEnd, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saas_product_analytics(tenant_id,id,event_name,outcome,safe_dimensions,occurred_at) VALUES($1,$2,'signup_completed','success',$3,$4)`, account.TenantID, uuid.NewString(), map[string]any{"phase": policy.Phase, "policy_version": policyVersion}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) Cancel(ctx context.Context, reservationID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var invitation *string
	var state string
	if err := tx.QueryRow(ctx, `SELECT invitation_sha256,state FROM saas_signup_reservations WHERE id=$1 FOR UPDATE`, reservationID).Scan(&invitation, &state); err != nil {
		return err
	}
	if state != "reserved" {
		return nil
	}
	if invitation != nil {
		if _, err := tx.Exec(ctx, `UPDATE saas_launch_invitations SET reserved_uses=GREATEST(reserved_uses-1,0) WHERE token_sha256=$1`, *invitation); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE saas_signup_reservations SET state='cancelled' WHERE id=$1`, reservationID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) FeatureEnabled(ctx context.Context, request auth.RequestContext, name string) (bool, error) {
	if s == nil || s.pool == nil || request.TenantID == "" || !safeFlag(name) {
		return false, ErrFeatureDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", request.TenantID); err != nil {
		return false, err
	}
	var enabled bool
	err = tx.QueryRow(ctx, `SELECT workload_mode NOT IN ('read_only','uploads_paused') AND COALESCE((feature_flags->$2)::boolean,false) FROM saas_tenant_launch_controls WHERE tenant_id=$1`, request.TenantID, name).Scan(&enabled)
	return enabled, err
}

func (s *Service) SetFeature(ctx context.Context, name string, enabled bool) error {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("account:manage") || !safeFlag(name) {
		return ErrFeatureDisabled
	}
	return s.updateControls(ctx, request, "feature_flag", name, func(tx pgx.Tx, now time.Time) error {
		_, err := tx.Exec(ctx, `UPDATE saas_tenant_launch_controls SET feature_flags=jsonb_set(feature_flags,ARRAY[$2],to_jsonb($3::boolean),true),updated_at=$4 WHERE tenant_id=$1`, request.TenantID, name, enabled, now)
		return err
	})
}

func (s *Service) SetWorkloadMode(ctx context.Context, mode string) error {
	request, ok := auth.FromContext(ctx)
	if !ok || !request.Can("account:manage") || (mode != "normal" && mode != "reduced" && mode != "read_only" && mode != "uploads_paused") {
		return ErrFeatureDisabled
	}
	return s.updateControls(ctx, request, "workload_mode", mode, func(tx pgx.Tx, now time.Time) error {
		_, err := tx.Exec(ctx, `UPDATE saas_tenant_launch_controls SET workload_mode=$2,updated_at=$3 WHERE tenant_id=$1`, request.TenantID, mode, now)
		return err
	})
}

func (s *Service) updateControls(ctx context.Context, request auth.RequestContext, operation, value string, update func(pgx.Tx, time.Time) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SELECT set_config('app.tenant_id',$1,true)", request.TenantID); err != nil {
		return err
	}
	now := s.now().UTC()
	if err := update(tx, now); err != nil {
		return err
	}
	requestID, traceID := request.RequestID, request.TraceID
	if requestID == "" || traceID == "" {
		requestID, traceID = audit.NewRequestIDs()
	}
	if err := audit.Append(ctx, tx, audit.Event{TenantID: request.TenantID, OccurredAt: now, ActorType: "member", ActorID: request.AccountID, Service: "control", Operation: "launch." + operation + ".update", Outcome: "success", RequestID: requestID, TraceID: traceID, TargetType: "tenant", TargetID: request.TenantID, PolicyVersion: "launch-v1", ReasonCode: value, SafeMetadata: map[string]any{"control": operation, "value": value}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func loadPolicy(ctx context.Context, tx pgx.Tx) (Policy, error) {
	var p Policy
	err := tx.QueryRow(ctx, `SELECT phase,signup_enabled,invitation_required,allowed_countries,minimum_age,account_cap,trial_days,source_cap,signup_rate_per_hour,abuse_rejection_limit,policy_version FROM saas_launch_policy WHERE singleton=true FOR SHARE`).Scan(&p.Phase, &p.SignupEnabled, &p.InvitationRequired, &p.AllowedCountries, &p.MinimumAge, &p.AccountCap, &p.TrialDays, &p.SourceCap, &p.SignupRatePerHour, &p.AbuseRejectionLimit, &p.Version)
	return p, err
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}

func normalizeNetwork(address string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(address))
	if err == nil {
		return host
	}
	return strings.TrimSpace(address)
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func safeFlag(name string) bool {
	switch name {
	case "source_upload", "generation", "exports":
		return true
	default:
		return false
	}
}
