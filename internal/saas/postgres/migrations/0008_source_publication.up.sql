ALTER TABLE saas_jobs ADD COLUMN lease_expires_at timestamptz;

ALTER TABLE saas_source_versions
    DROP CONSTRAINT saas_source_versions_tenant_id_content_sha256_key;
CREATE INDEX saas_source_versions_content_lookup
    ON saas_source_versions(tenant_id, content_sha256);

CREATE TABLE saas_source_nodes (
    tenant_id uuid NOT NULL,
    source_id uuid NOT NULL,
    source_version bigint NOT NULL,
    id text NOT NULL,
    parent_id text,
    kind text NOT NULL,
    ordinal integer NOT NULL,
    title text NOT NULL,
    start_offset integer NOT NULL,
    end_offset integer NOT NULL,
    explicit boolean NOT NULL,
    PRIMARY KEY (tenant_id, source_id, source_version, id),
    FOREIGN KEY (tenant_id, source_id, source_version)
        REFERENCES saas_source_versions(tenant_id, source_id, version) ON DELETE CASCADE
);

CREATE TABLE saas_source_passages (
    tenant_id uuid NOT NULL,
    source_id uuid NOT NULL,
    source_version bigint NOT NULL,
    id text NOT NULL,
    structural_node_id text NOT NULL,
    text_content text NOT NULL,
    fingerprint text NOT NULL,
    locator jsonb NOT NULL,
    PRIMARY KEY (tenant_id, source_id, source_version, id),
    FOREIGN KEY (tenant_id, source_id, source_version)
        REFERENCES saas_source_versions(tenant_id, source_id, version) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, source_id, source_version, structural_node_id)
        REFERENCES saas_source_nodes(tenant_id, source_id, source_version, id)
);

CREATE TABLE saas_source_citations (
    tenant_id uuid NOT NULL,
    source_id uuid NOT NULL,
    source_version bigint NOT NULL,
    id text NOT NULL,
    passage_id text NOT NULL,
    structural_node_id text NOT NULL,
    passage_fingerprint text NOT NULL,
    locator jsonb NOT NULL,
    PRIMARY KEY (tenant_id, source_id, source_version, id),
    FOREIGN KEY (tenant_id, source_id, source_version, passage_id)
        REFERENCES saas_source_passages(tenant_id, source_id, source_version, id) ON DELETE CASCADE
);

CREATE INDEX saas_source_passages_source
    ON saas_source_passages(tenant_id, source_id, source_version);

ALTER TABLE saas_source_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_source_nodes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_source_nodes
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);

ALTER TABLE saas_source_passages ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_source_passages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_source_passages
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);

ALTER TABLE saas_source_citations ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_source_citations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_source_citations
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);
