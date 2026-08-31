CREATE TABLE saas_skill_orchestrator_reconciliation_partitions (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    environment text NOT NULL,
    owner text NOT NULL DEFAULT '',
    fence bigint NOT NULL DEFAULT 0 CHECK (fence >= 0),
    lease_expires_at timestamptz,
    restore_paused boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, workspace_id, environment),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX idx_skill_reconciliation_partitions_claim
    ON saas_skill_orchestrator_reconciliation_partitions(restore_paused, lease_expires_at, tenant_id, workspace_id, environment);

ALTER TABLE saas_skill_orchestrator_reconciliation_partitions ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_reconciliation_partitions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_reconciliation_partitions
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid);
