CREATE TABLE saas_operator_assignments (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    operator_id text NOT NULL,
    role text NOT NULL CHECK (role IN ('support','trust','security_admin')),
    state text NOT NULL CHECK (state IN ('active','revoked')),
    granted_by text NOT NULL,
    granted_at timestamptz NOT NULL,
    revoked_at timestamptz,
    PRIMARY KEY (tenant_id,operator_id)
);

CREATE TABLE saas_operator_elevations (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    operator_id text NOT NULL,
    source_id uuid NOT NULL,
    ticket_ref text NOT NULL,
    reason_code text NOT NULL,
    state text NOT NULL CHECK (state IN ('requested','approved','denied','expired','revoked')),
    requested_at timestamptz NOT NULL,
    approved_by text NOT NULL DEFAULT '',
    approved_at timestamptz,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    PRIMARY KEY (tenant_id,id),
    FOREIGN KEY (tenant_id,operator_id) REFERENCES saas_operator_assignments(tenant_id,operator_id),
    FOREIGN KEY (tenant_id,source_id) REFERENCES saas_sources(tenant_id,id)
);
CREATE INDEX saas_operator_elevations_active_idx ON saas_operator_elevations(tenant_id,operator_id,source_id,expires_at DESC);

ALTER TABLE saas_operator_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_operator_assignments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_operator_assignments USING (tenant_id=current_setting('app.tenant_id',true)::uuid) WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);
ALTER TABLE saas_operator_elevations ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_operator_elevations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_operator_elevations USING (tenant_id=current_setting('app.tenant_id',true)::uuid) WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);

