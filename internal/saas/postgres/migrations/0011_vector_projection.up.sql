CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE saas_source_projections
    DROP CONSTRAINT saas_source_projections_state_check;
ALTER TABLE saas_source_projections
    ADD CONSTRAINT saas_source_projections_state_check
    CHECK(state IN ('processing','ready','failed'));
ALTER TABLE saas_source_projections
    ADD COLUMN claim_token uuid,
    ADD COLUMN claimed_until timestamptz;

CREATE TABLE saas_vector_documents (
    tenant_id uuid NOT NULL,
    source_id uuid NOT NULL,
    source_version bigint NOT NULL,
    passage_id text NOT NULL,
    structural_node_id text NOT NULL,
    embedding vector(384) NOT NULL,
    provider text NOT NULL,
    model text NOT NULL,
    dimensions integer NOT NULL CHECK(dimensions=384),
    projected_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, source_id, source_version, passage_id),
    FOREIGN KEY (tenant_id, source_id, source_version, passage_id)
        REFERENCES saas_source_passages(tenant_id, source_id, source_version, id) ON DELETE CASCADE
);
CREATE INDEX saas_vector_documents_source
    ON saas_vector_documents(tenant_id,source_id,source_version);
CREATE INDEX saas_vector_documents_embedding
    ON saas_vector_documents USING hnsw (embedding vector_cosine_ops);

ALTER TABLE saas_vector_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_vector_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_vector_documents
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);
