CREATE TABLE saas_import_operations (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    bundle_sha256 text NOT NULL,
    idempotency_key text NOT NULL,
    state text NOT NULL CHECK(state IN ('validating','running','completed','failed')),
    report jsonb NOT NULL DEFAULT '{"imported":[],"merged":[],"skipped":[],"failed":[]}'::jsonb,
    safe_error_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    completed_at timestamptz,
    PRIMARY KEY(tenant_id,id),
    UNIQUE(tenant_id,idempotency_key),
    UNIQUE(tenant_id,bundle_sha256)
);
CREATE TABLE saas_import_items (
    tenant_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    item_type text NOT NULL CHECK(item_type IN ('memory','note','source')),
    external_id text NOT NULL,
    state text NOT NULL CHECK(state IN ('pending','processing','imported','merged','skipped','failed')),
    result_id text NOT NULL DEFAULT '',
    reason_code text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,operation_id,item_type,external_id),
    FOREIGN KEY(tenant_id,operation_id) REFERENCES saas_import_operations(tenant_id,id)
);
DO $$ DECLARE table_name text; BEGIN FOREACH table_name IN ARRAY ARRAY['saas_import_operations','saas_import_items'] LOOP EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',table_name);EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',table_name);EXECUTE format('CREATE POLICY tenant_isolation ON %I USING(tenant_id=current_setting(''app.tenant_id'',true)::uuid) WITH CHECK(tenant_id=current_setting(''app.tenant_id'',true)::uuid)',table_name);END LOOP;END $$;
