CREATE TABLE saas_memory_proposals (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    requested_by uuid NOT NULL,
    memory_type text NOT NULL CHECK(memory_type IN ('episodic','semantic','procedural','outcome')),
    content text NOT NULL,
    transformation text NOT NULL CHECK(transformation IN ('summary','interpretation','synthesis','user_edit')),
    transformation_version text NOT NULL,
    evidence jsonb NOT NULL,
    status text NOT NULL CHECK(status IN ('suggested','accepted','rejected')),
    memory_id uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    reviewed_at timestamptz,
    PRIMARY KEY(tenant_id,id),
    FOREIGN KEY(tenant_id,workspace_id) REFERENCES saas_workspaces(tenant_id,id),
    FOREIGN KEY(tenant_id,requested_by) REFERENCES saas_memberships(tenant_id,account_id),
    FOREIGN KEY(tenant_id,memory_id) REFERENCES saas_memories(tenant_id,id)
);
CREATE INDEX saas_memory_proposals_review
    ON saas_memory_proposals(tenant_id,requested_by,status,created_at);
ALTER TABLE saas_memory_proposals ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_memory_proposals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_memory_proposals
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);
