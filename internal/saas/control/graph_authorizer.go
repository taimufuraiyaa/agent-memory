package control

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
)

// AuthorizeGraphWorkspace binds every hosted graph operation to the active
// tenant membership and an active workspace. The permission has already been
// checked by the transport, but is accepted here to keep the authorization
// boundary explicit at each call site.
func (s *PostgresStore) AuthorizeGraphWorkspace(r *http.Request, caller auth.RequestContext, workspaceID, _ string) error {
	if s == nil || s.pool == nil || r == nil || strings.TrimSpace(caller.TenantID) == "" || strings.TrimSpace(caller.AccountID) == "" || strings.TrimSpace(workspaceID) == "" {
		return auth.ErrTenantUnavailable
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		return auth.ErrTenantUnavailable
	}
	defer func() { _ = tx.Rollback(r.Context()) }()
	if _, err := tx.Exec(r.Context(), "SELECT set_config('app.tenant_id', $1, true)", caller.TenantID); err != nil {
		return auth.ErrTenantUnavailable
	}
	var allowed bool
	err = tx.QueryRow(r.Context(), `SELECT EXISTS(
		SELECT 1 FROM saas_workspaces w
		JOIN saas_memberships m ON m.tenant_id=w.tenant_id AND m.account_id=$3::uuid
		WHERE w.id=$1::uuid AND w.tenant_id=$2::uuid AND w.state='active' AND m.state='active'
	)`, workspaceID, caller.TenantID, caller.AccountID).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) || err != nil || !allowed {
		return auth.ErrTenantUnavailable
	}
	return nil
}
