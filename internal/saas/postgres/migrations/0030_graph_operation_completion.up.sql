ALTER TABLE saas_graph_edge_versions ADD COLUMN origin text NOT NULL DEFAULT 'inferred' CHECK (origin IN ('inferred','deterministic'));
ALTER TABLE saas_graph_edge_versions ADD COLUMN provenance_approved boolean NOT NULL DEFAULT false;
ALTER TABLE saas_graph_communities ADD COLUMN configuration_id uuid;
ALTER TABLE saas_graph_communities ADD COLUMN membership_fingerprint text NOT NULL DEFAULT '';
ALTER TABLE saas_graph_communities ADD COLUMN evidence_fingerprint text NOT NULL DEFAULT '';
ALTER TABLE saas_graph_reports ADD COLUMN admission_state text NOT NULL DEFAULT '';
ALTER TABLE saas_graph_reports ADD COLUMN model_route text NOT NULL DEFAULT '';
ALTER TABLE saas_graph_reports ADD COLUMN model_fingerprint text NOT NULL DEFAULT '';
ALTER TABLE saas_graph_reports ADD COLUMN prompt_fingerprint text NOT NULL DEFAULT '';
ALTER TABLE saas_graph_reports ADD COLUMN membership_fingerprint text NOT NULL DEFAULT '';
ALTER TABLE saas_graph_reports ADD COLUMN evidence_fingerprint text NOT NULL DEFAULT '';
ALTER TABLE saas_graph_reports ADD COLUMN review_version bigint NOT NULL DEFAULT 0;

CREATE TABLE saas_graph_completion_events (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    event_id text NOT NULL,
    job_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('processing','completed')),
    lease_owner text NOT NULL,
    lease_expires_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,event_id),
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id,job_id) REFERENCES saas_graph_jobs(tenant_id,id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id,revision_id) REFERENCES saas_graph_revisions(tenant_id,id) ON DELETE CASCADE
);
ALTER TABLE saas_graph_completion_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_completion_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_completion_events USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

CREATE TABLE saas_graph_change_journal (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    subject_kind text NOT NULL,
    subject_id text NOT NULL,
    subject_fingerprint text NOT NULL,
    projection_version text NOT NULL,
    configuration_version text NOT NULL,
    change_kind text NOT NULL,
    occurred_at timestamptz NOT NULL,
    processed_revision_id uuid,
    PRIMARY KEY (tenant_id,id),
    UNIQUE (tenant_id,workspace_id,subject_kind,subject_id,subject_fingerprint,projection_version,configuration_version),
    FOREIGN KEY (tenant_id,workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE
);
ALTER TABLE saas_graph_change_journal ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_change_journal FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_change_journal USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
