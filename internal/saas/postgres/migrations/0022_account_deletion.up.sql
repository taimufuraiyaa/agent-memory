ALTER TABLE saas_deletion_operations ADD COLUMN execute_after timestamptz NOT NULL DEFAULT clock_timestamp();
CREATE TABLE saas_account_deletion_policies (
    version text PRIMARY KEY,
    mode text NOT NULL CHECK(mode IN ('immediate','cooling_off')),
    cooling_off_seconds bigint NOT NULL,
    state text NOT NULL CHECK(state IN ('active','retired')),
    migration_plan text NOT NULL,
    customer_impact text NOT NULL,
    effective_at timestamptz NOT NULL
);
CREATE UNIQUE INDEX saas_account_deletion_policy_active ON saas_account_deletion_policies(state) WHERE state='active';
INSERT INTO saas_account_deletion_policies VALUES('account-deletion-v1','cooling_off',604800,'active','existing requests retain their recorded execute_after','access revokes immediately; physical purge begins after seven days',clock_timestamp());

CREATE TABLE saas_audit_pseudonymization (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    actor_id text NOT NULL,
    pseudonym text NOT NULL,
    operation_id uuid NOT NULL,
    applied_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,actor_id),
    FOREIGN KEY(tenant_id,operation_id) REFERENCES saas_deletion_operations(tenant_id,id)
);
ALTER TABLE saas_audit_pseudonymization ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_audit_pseudonymization FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_audit_pseudonymization USING(tenant_id=current_setting('app.tenant_id',true)::uuid) WITH CHECK(tenant_id=current_setting('app.tenant_id',true)::uuid);
