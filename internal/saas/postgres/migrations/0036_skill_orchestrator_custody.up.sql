CREATE TABLE saas_skill_orchestrator_legal_holds (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    environment text NOT NULL,
    id uuid NOT NULL,
    target_kind text NOT NULL CHECK (target_kind IN ('workspace','workflow','job','configuration','safety_signal')),
    target_id text NOT NULL CHECK (octet_length(target_id) BETWEEN 1 AND 256),
    reason text NOT NULL CHECK (octet_length(reason) BETWEEN 1 AND 512),
    state text NOT NULL CHECK (state IN ('active','released')),
    created_at timestamptz NOT NULL,
    released_at timestamptz,
    PRIMARY KEY (tenant_id,id),
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX saas_skill_orchestrator_legal_holds_active
    ON saas_skill_orchestrator_legal_holds(tenant_id,workspace_id,environment,target_kind,target_id)
    WHERE state='active';

CREATE TABLE saas_skill_orchestrator_tombstones (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    environment text NOT NULL,
    record_kind text NOT NULL CHECK (record_kind IN ('workflow','job','configuration','safety_signal')),
    record_id text NOT NULL CHECK (octet_length(record_id) BETWEEN 1 AND 256),
    deleted_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,workspace_id,environment,record_kind,record_id),
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE
);

ALTER TABLE saas_skill_orchestrator_legal_holds ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_legal_holds FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_legal_holds USING (tenant_id=current_setting('app.tenant_id',true)::uuid AND workspace_id=current_setting('app.workspace_id',true)::uuid) WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid AND workspace_id=current_setting('app.workspace_id',true)::uuid);
ALTER TABLE saas_skill_orchestrator_tombstones ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_tombstones FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_tombstones USING (tenant_id=current_setting('app.tenant_id',true)::uuid AND workspace_id=current_setting('app.workspace_id',true)::uuid) WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid AND workspace_id=current_setting('app.workspace_id',true)::uuid);

GRANT SELECT ON saas_skill_orchestrator_tombstones TO agent_memory_skill_worker,agent_memory_skill_reconciler;
