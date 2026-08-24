CREATE TABLE saas_plan_change_requests (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    action text NOT NULL CHECK(action IN ('upgrade','cancel')),
    requested_plan_id text NOT NULL,
    state text NOT NULL CHECK(state IN ('queued','sent','applied','failed')),
    idempotency_key text NOT NULL,
    safe_error_code text NOT NULL DEFAULT '',
    requested_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,id),
    UNIQUE(tenant_id,idempotency_key)
);
ALTER TABLE saas_plan_change_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_plan_change_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_plan_change_requests USING(tenant_id=current_setting('app.tenant_id',true)::uuid) WITH CHECK(tenant_id=current_setting('app.tenant_id',true)::uuid);
