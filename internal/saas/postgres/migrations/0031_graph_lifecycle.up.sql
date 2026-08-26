CREATE TABLE saas_graph_deletion_tombstones (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    canonical_kind text NOT NULL,
    canonical_id text NOT NULL,
    deleted_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,workspace_id,canonical_kind,canonical_id),
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE
);

CREATE TABLE saas_graph_repair_queue (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    canonical_kind text NOT NULL,
    canonical_id text NOT NULL,
    affected_entities bigint NOT NULL DEFAULT 0 CHECK (affected_entities >= 0),
    affected_edges bigint NOT NULL DEFAULT 0 CHECK (affected_edges >= 0),
    affected_reports bigint NOT NULL DEFAULT 0 CHECK (affected_reports >= 0),
    deadline_at timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('queued','running','completed','failed')),
    PRIMARY KEY (tenant_id,workspace_id,canonical_kind,canonical_id),
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE
);

ALTER TABLE saas_graph_deletion_tombstones ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_deletion_tombstones FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_deletion_tombstones USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_repair_queue ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_repair_queue FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_repair_queue USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

CREATE INDEX saas_graph_deletion_tombstones_deleted_at ON saas_graph_deletion_tombstones (tenant_id,workspace_id,deleted_at);
CREATE INDEX saas_graph_repair_queue_claim ON saas_graph_repair_queue (tenant_id,state,deadline_at);
