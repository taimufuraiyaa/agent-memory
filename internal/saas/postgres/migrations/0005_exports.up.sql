CREATE TABLE saas_exports (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    workspace_id uuid,
    state text NOT NULL CHECK (state IN ('queued','running','ready','failed','expired')),
    object_key text NOT NULL DEFAULT '',
    checksum_sha256 text NOT NULL DEFAULT '',
    safe_error_code text NOT NULL DEFAULT '',
    requested_at timestamptz NOT NULL,
    completed_at timestamptz,
    expires_at timestamptz,
    PRIMARY KEY (tenant_id,id),
    FOREIGN KEY (tenant_id,account_id) REFERENCES saas_memberships(tenant_id,account_id),
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES saas_workspaces(tenant_id,id)
);
ALTER TABLE saas_exports ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_exports FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_exports
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);
CREATE INDEX saas_exports_queued ON saas_exports(tenant_id,requested_at) WHERE state='queued';
