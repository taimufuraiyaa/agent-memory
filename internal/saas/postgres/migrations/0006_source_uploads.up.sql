ALTER TABLE saas_sources DROP CONSTRAINT saas_sources_state_check;
ALTER TABLE saas_sources ADD CONSTRAINT saas_sources_state_check CHECK
    (state IN ('pending','uploading','validating','processing','indexing','ready','failed','disabled','deleting','deleted'));

CREATE TABLE saas_tenant_entitlements (
    tenant_id uuid PRIMARY KEY REFERENCES saas_tenants(id),
    source_upload_enabled boolean NOT NULL DEFAULT true,
    max_source_bytes bigint NOT NULL DEFAULT 104857600,
    max_source_count integer NOT NULL DEFAULT 100,
    max_concurrent_uploads integer NOT NULL DEFAULT 3,
    updated_at timestamptz NOT NULL
);
INSERT INTO saas_tenant_entitlements(tenant_id,updated_at) SELECT id,clock_timestamp() FROM saas_tenants;
ALTER TABLE saas_tenant_entitlements ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_tenant_entitlements FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_tenant_entitlements
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);

CREATE TABLE saas_upload_grants (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    source_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    account_id uuid NOT NULL,
    filename text NOT NULL,
    media_type text NOT NULL,
    expected_size bigint NOT NULL,
    expected_sha256 text NOT NULL,
    rights_basis text NOT NULL,
    attestation_receipt_id uuid NOT NULL,
    token_hash bytea NOT NULL,
    quarantine_object_key text NOT NULL,
    state text NOT NULL CHECK (state IN ('issued','uploading','uploaded','failed','expired')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    consumed_at timestamptz,
    PRIMARY KEY (tenant_id,id),
    UNIQUE (tenant_id,source_id),
    FOREIGN KEY (tenant_id,source_id) REFERENCES saas_sources(tenant_id,id),
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES saas_workspaces(tenant_id,id),
    FOREIGN KEY (tenant_id,account_id) REFERENCES saas_memberships(tenant_id,account_id)
);
ALTER TABLE saas_upload_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_upload_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_upload_grants
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);
CREATE INDEX saas_upload_grants_pending ON saas_upload_grants(tenant_id,state,expires_at);
