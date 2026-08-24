CREATE TABLE saas_passage_signals (
    tenant_id uuid NOT NULL,
    source_id uuid NOT NULL,
    source_version bigint NOT NULL,
    passage_id text NOT NULL,
    decay_score double precision NOT NULL DEFAULT 0 CHECK(decay_score BETWEEN 0 AND 1),
    salience_score double precision NOT NULL DEFAULT 0 CHECK(salience_score BETWEEN 0 AND 1),
    suppression_score double precision NOT NULL DEFAULT 0 CHECK(suppression_score BETWEEN 0 AND 1),
    useful_count integer NOT NULL DEFAULT 0,
    rejected_count integer NOT NULL DEFAULT 0,
    harmful_count integer NOT NULL DEFAULT 0,
    last_helpful_at timestamptz,
    last_rejected_at timestamptz,
    suppression_until timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,source_id,source_version,passage_id),
    FOREIGN KEY(tenant_id,source_id,source_version,passage_id)
        REFERENCES saas_source_passages(tenant_id,source_id,source_version,id) ON DELETE CASCADE
);

CREATE TABLE saas_passage_feedback (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    source_id uuid NOT NULL,
    source_version bigint NOT NULL,
    passage_id text NOT NULL,
    rating text NOT NULL CHECK(rating IN ('helpful','rejected','harmful')),
    actor_id text NOT NULL,
    request_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,id),
    FOREIGN KEY(tenant_id,source_id,source_version,passage_id)
        REFERENCES saas_source_passages(tenant_id,source_id,source_version,id) ON DELETE CASCADE
);

CREATE INDEX saas_passage_feedback_passage
    ON saas_passage_feedback(tenant_id,source_id,source_version,passage_id,occurred_at);

ALTER TABLE saas_passage_signals ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_passage_signals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_passage_signals
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);

ALTER TABLE saas_passage_feedback ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_passage_feedback FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_passage_feedback
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);
