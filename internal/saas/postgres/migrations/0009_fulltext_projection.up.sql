CREATE TABLE saas_fulltext_documents (
    tenant_id uuid NOT NULL,
    source_id uuid NOT NULL,
    source_version bigint NOT NULL,
    passage_id text NOT NULL,
    structural_node_id text NOT NULL,
    text_content text NOT NULL,
    locator jsonb NOT NULL,
    search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', text_content)) STORED,
    projected_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, source_id, source_version, passage_id),
    FOREIGN KEY (tenant_id, source_id, source_version, passage_id)
        REFERENCES saas_source_passages(tenant_id, source_id, source_version, id) ON DELETE CASCADE
);
CREATE INDEX saas_fulltext_documents_search
    ON saas_fulltext_documents USING gin(search_vector);

CREATE TABLE saas_source_projections (
    tenant_id uuid NOT NULL,
    source_id uuid NOT NULL,
    source_version bigint NOT NULL,
    projection_kind text NOT NULL,
    projection_version text NOT NULL,
    state text NOT NULL CHECK(state IN ('ready','failed')),
    document_count integer NOT NULL,
    safe_error_code text NOT NULL DEFAULT '',
    projected_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, source_id, source_version, projection_kind),
    FOREIGN KEY (tenant_id, source_id, source_version)
        REFERENCES saas_source_versions(tenant_id, source_id, version) ON DELETE CASCADE
);

ALTER TABLE saas_fulltext_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_fulltext_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_fulltext_documents
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);

ALTER TABLE saas_source_projections ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_source_projections FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_source_projections
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);
