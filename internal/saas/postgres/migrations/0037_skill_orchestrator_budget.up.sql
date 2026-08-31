CREATE TABLE saas_skill_orchestrator_budget_accounts (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    environment text NOT NULL,
    policy_version bigint NOT NULL CHECK (policy_version > 0),
    period_start timestamptz NOT NULL,
    limit_units bigint NOT NULL CHECK (limit_units > 0),
    reserved_units bigint NOT NULL DEFAULT 0 CHECK (reserved_units >= 0),
    committed_units bigint NOT NULL DEFAULT 0 CHECK (committed_units >= 0),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,workspace_id,environment,policy_version,period_start),
    CHECK (reserved_units + committed_units <= limit_units),
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE
);

CREATE TABLE saas_skill_orchestrator_budget_reservations (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    environment text NOT NULL,
    job_id uuid NOT NULL,
    policy_version bigint NOT NULL CHECK (policy_version > 0),
    period_start timestamptz NOT NULL,
    reserved_units bigint NOT NULL CHECK (reserved_units > 0),
    committed_units bigint NOT NULL DEFAULT 0 CHECK (committed_units >= 0 AND committed_units <= reserved_units),
    state text NOT NULL CHECK (state IN ('reserved','committed','released')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,workspace_id,environment,job_id),
    FOREIGN KEY (tenant_id,workspace_id,environment,policy_version,period_start)
        REFERENCES saas_skill_orchestrator_budget_accounts(tenant_id,workspace_id,environment,policy_version,period_start)
);

CREATE INDEX saas_skill_orchestrator_budget_expiry
    ON saas_skill_orchestrator_budget_reservations(tenant_id,workspace_id,environment,state,expires_at);

ALTER TABLE saas_skill_orchestrator_budget_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_budget_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_budget_accounts
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid AND workspace_id=current_setting('app.workspace_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid AND workspace_id=current_setting('app.workspace_id',true)::uuid);

ALTER TABLE saas_skill_orchestrator_budget_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_budget_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_budget_reservations
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid AND workspace_id=current_setting('app.workspace_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid AND workspace_id=current_setting('app.workspace_id',true)::uuid);

GRANT SELECT,INSERT,UPDATE ON saas_skill_orchestrator_budget_accounts TO agent_memory_skill_worker;
GRANT SELECT,INSERT,UPDATE ON saas_skill_orchestrator_budget_reservations TO agent_memory_skill_worker;
