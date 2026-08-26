CREATE TABLE saas_graph_configurations (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    enabled boolean NOT NULL DEFAULT false,
    adapter_name text NOT NULL,
    adapter_version text NOT NULL,
    index_method text NOT NULL CHECK (index_method IN ('standard', 'fast')),
    projection_version text NOT NULL,
    artifact_schema_version text NOT NULL,
    prompt_fingerprint text NOT NULL,
    model_route text NOT NULL,
    active_revision_id uuid,
    previous_revision_id uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, workspace_id, version),
    UNIQUE (tenant_id, workspace_id, id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE saas_graph_revisions (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    configuration_id uuid NOT NULL,
    base_revision_id uuid,
    state text NOT NULL CHECK (state IN ('queued', 'projecting', 'indexing', 'validating', 'importing', 'evaluating', 'ready', 'active', 'previous', 'failed', 'cancelled')),
    cutoff_sequence bigint NOT NULL DEFAULT 0 CHECK (cutoff_sequence >= 0),
    cutoff_event_time timestamptz,
    cutoff_digest text NOT NULL DEFAULT '',
    projection_hash text NOT NULL DEFAULT '',
    artifact_hash text NOT NULL DEFAULT '',
    previous_revision_id uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, workspace_id, configuration_id, id),
    FOREIGN KEY (tenant_id, workspace_id, configuration_id) REFERENCES saas_graph_configurations(tenant_id, workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, base_revision_id) REFERENCES saas_graph_revisions(tenant_id, id),
    FOREIGN KEY (tenant_id, previous_revision_id) REFERENCES saas_graph_revisions(tenant_id, id)
);

ALTER TABLE saas_graph_configurations
    ADD CONSTRAINT saas_graph_configurations_active_revision_fk
    FOREIGN KEY (tenant_id, active_revision_id) REFERENCES saas_graph_revisions(tenant_id, id);
ALTER TABLE saas_graph_configurations
    ADD CONSTRAINT saas_graph_configurations_previous_revision_fk
    FOREIGN KEY (tenant_id, previous_revision_id) REFERENCES saas_graph_revisions(tenant_id, id);

CREATE TABLE saas_graph_jobs (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    configuration_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    idempotency_key text NOT NULL,
    state text NOT NULL CHECK (state IN ('queued', 'running', 'completed', 'failed', 'cancelled', 'dead_letter')),
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    lease_owner text NOT NULL DEFAULT '',
    lease_expires_at timestamptz,
    failure_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, workspace_id, configuration_id, idempotency_key),
    FOREIGN KEY (tenant_id, workspace_id, configuration_id) REFERENCES saas_graph_configurations(tenant_id, workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, revision_id) REFERENCES saas_graph_revisions(tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE saas_graph_entities (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    trust text NOT NULL CHECK (trust IN ('proposed', 'reviewed', 'approved', 'rejected', 'superseded', 'quarantined', 'stale', 'deleted')),
    record_version bigint NOT NULL DEFAULT 1 CHECK (record_version > 0),
    first_revision_id uuid NOT NULL,
    last_revision_id uuid NOT NULL,
    superseded_by uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, workspace_id, id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, first_revision_id) REFERENCES saas_graph_revisions(tenant_id, id),
    FOREIGN KEY (tenant_id, last_revision_id) REFERENCES saas_graph_revisions(tenant_id, id),
    FOREIGN KEY (tenant_id, superseded_by) REFERENCES saas_graph_entities(tenant_id, id)
);

CREATE TABLE saas_graph_entity_versions (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    entity_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    external_id text NOT NULL,
    name text NOT NULL,
    entity_type text NOT NULL,
    description text NOT NULL,
    aliases jsonb NOT NULL DEFAULT '[]'::jsonb,
    occurrence_count bigint NOT NULL DEFAULT 0 CHECK (occurrence_count >= 0),
    degree bigint NOT NULL DEFAULT 0 CHECK (degree >= 0),
    PRIMARY KEY (tenant_id, entity_id, revision_id),
    FOREIGN KEY (tenant_id, workspace_id, entity_id) REFERENCES saas_graph_entities(tenant_id, workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, revision_id) REFERENCES saas_graph_revisions(tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE saas_graph_entity_evidence (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    entity_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    evidence_id uuid NOT NULL,
    canonical_kind text NOT NULL,
    canonical_id text NOT NULL,
    canonical_fingerprint text NOT NULL,
    locator text NOT NULL DEFAULT '',
    occurrence_count bigint NOT NULL DEFAULT 0 CHECK (occurrence_count >= 0),
    PRIMARY KEY (tenant_id, entity_id, revision_id, evidence_id),
    FOREIGN KEY (tenant_id, entity_id, revision_id) REFERENCES saas_graph_entity_versions(tenant_id, entity_id, revision_id) ON DELETE CASCADE
);

CREATE TABLE saas_graph_edges (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    source_entity_id uuid NOT NULL,
    target_entity_id uuid NOT NULL,
    normalized_kind text NOT NULL,
    external_kind text NOT NULL DEFAULT '',
    trust text NOT NULL CHECK (trust IN ('proposed', 'reviewed', 'approved', 'rejected', 'superseded', 'quarantined', 'stale', 'deleted')),
    record_version bigint NOT NULL DEFAULT 1 CHECK (record_version > 0),
    first_revision_id uuid NOT NULL,
    last_revision_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, workspace_id, id),
    FOREIGN KEY (tenant_id, workspace_id, source_entity_id) REFERENCES saas_graph_entities(tenant_id, workspace_id, id),
    FOREIGN KEY (tenant_id, workspace_id, target_entity_id) REFERENCES saas_graph_entities(tenant_id, workspace_id, id),
    FOREIGN KEY (tenant_id, first_revision_id) REFERENCES saas_graph_revisions(tenant_id, id),
    FOREIGN KEY (tenant_id, last_revision_id) REFERENCES saas_graph_revisions(tenant_id, id)
);

CREATE TABLE saas_graph_edge_versions (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    edge_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    external_id text NOT NULL,
    description text NOT NULL,
    weight double precision NOT NULL CHECK (weight >= 0 AND weight <= 1),
    PRIMARY KEY (tenant_id, edge_id, revision_id),
    FOREIGN KEY (tenant_id, workspace_id, edge_id) REFERENCES saas_graph_edges(tenant_id, workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, revision_id) REFERENCES saas_graph_revisions(tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE saas_graph_edge_evidence (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    edge_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    evidence_id uuid NOT NULL,
    canonical_kind text NOT NULL,
    canonical_id text NOT NULL,
    canonical_fingerprint text NOT NULL,
    locator text NOT NULL DEFAULT '',
    occurrence_count bigint NOT NULL DEFAULT 0 CHECK (occurrence_count >= 0),
    PRIMARY KEY (tenant_id, edge_id, revision_id, evidence_id),
    FOREIGN KEY (tenant_id, edge_id, revision_id) REFERENCES saas_graph_edge_versions(tenant_id, edge_id, revision_id) ON DELETE CASCADE
);

CREATE TABLE saas_graph_communities (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    revision_id uuid NOT NULL,
    external_id text NOT NULL,
    parent_id uuid,
    level integer NOT NULL CHECK (level >= 0),
    entity_count bigint NOT NULL DEFAULT 0 CHECK (entity_count >= 0),
    edge_count bigint NOT NULL DEFAULT 0 CHECK (edge_count >= 0),
    source_count bigint NOT NULL DEFAULT 0 CHECK (source_count >= 0),
    unresolved_count bigint NOT NULL DEFAULT 0 CHECK (unresolved_count >= 0),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, workspace_id, id),
    FOREIGN KEY (tenant_id, revision_id) REFERENCES saas_graph_revisions(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, parent_id) REFERENCES saas_graph_communities(tenant_id, id)
);

CREATE TABLE saas_graph_community_members (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    community_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('entity', 'edge', 'text_unit')),
    target_id text NOT NULL,
    PRIMARY KEY (tenant_id, community_id, revision_id, kind, target_id),
    FOREIGN KEY (tenant_id, workspace_id, community_id) REFERENCES saas_graph_communities(tenant_id, workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, revision_id) REFERENCES saas_graph_revisions(tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE saas_graph_reports (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    community_id uuid NOT NULL,
    revision_id uuid NOT NULL,
    title text NOT NULL,
    summary text NOT NULL,
    findings jsonb NOT NULL DEFAULT '[]'::jsonb,
    rank double precision NOT NULL DEFAULT 0,
    trust text NOT NULL CHECK (trust IN ('proposed', 'reviewed', 'approved', 'rejected', 'superseded', 'quarantined', 'stale', 'deleted')),
    stale boolean NOT NULL DEFAULT false,
    evidence_count bigint NOT NULL DEFAULT 0 CHECK (evidence_count >= 0),
    unresolved_count bigint NOT NULL DEFAULT 0 CHECK (unresolved_count >= 0),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, workspace_id, id),
    FOREIGN KEY (tenant_id, workspace_id, community_id) REFERENCES saas_graph_communities(tenant_id, workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, revision_id) REFERENCES saas_graph_revisions(tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE saas_graph_reviews (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    action text NOT NULL DEFAULT '',
    target_kind text NOT NULL CHECK (target_kind IN ('entity', 'edge', 'report')),
    target_id uuid NOT NULL,
    from_state text NOT NULL,
    to_state text NOT NULL,
    expected_version bigint NOT NULL CHECK (expected_version > 0),
    reason text NOT NULL DEFAULT '',
    reviewer_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id, id) ON DELETE CASCADE
);

CREATE TABLE saas_graph_feedback (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    request_id text NOT NULL,
    target_kind text NOT NULL,
    target_id text NOT NULL,
    outcome text NOT NULL,
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX saas_graph_jobs_claim ON saas_graph_jobs (tenant_id, workspace_id, state, lease_expires_at, created_at);
CREATE INDEX saas_graph_entities_query ON saas_graph_entities (tenant_id, workspace_id, trust, updated_at DESC);
CREATE INDEX saas_graph_edges_query ON saas_graph_edges (tenant_id, workspace_id, trust, source_entity_id, target_entity_id);
CREATE INDEX saas_graph_reports_query ON saas_graph_reports (tenant_id, workspace_id, stale, trust, rank DESC);
CREATE INDEX saas_graph_entity_evidence_canonical ON saas_graph_entity_evidence (tenant_id, workspace_id, canonical_kind, canonical_id);
CREATE INDEX saas_graph_edge_evidence_canonical ON saas_graph_edge_evidence (tenant_id, workspace_id, canonical_kind, canonical_id);
CREATE INDEX saas_graph_reviews_target ON saas_graph_reviews (tenant_id, workspace_id, target_kind, target_id, created_at DESC);
CREATE INDEX saas_graph_feedback_target ON saas_graph_feedback (tenant_id, workspace_id, target_kind, target_id, created_at DESC);

ALTER TABLE saas_graph_configurations ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_configurations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_configurations USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_revisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_revisions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_revisions USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_jobs USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_entities ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_entities FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_entities USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_entity_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_entity_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_entity_versions USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_entity_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_entity_evidence FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_entity_evidence USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_edges ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_edges FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_edges USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_edge_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_edge_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_edge_versions USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_edge_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_edge_evidence FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_edge_evidence USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_communities ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_communities FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_communities USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_community_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_community_members FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_community_members USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_reports FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_reports USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_reviews ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_reviews FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_reviews USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
ALTER TABLE saas_graph_feedback ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_graph_feedback FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_graph_feedback USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
