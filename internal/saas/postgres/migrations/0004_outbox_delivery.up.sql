ALTER TABLE saas_outbox ADD COLUMN claim_token uuid;
ALTER TABLE saas_outbox ADD COLUMN claimed_until timestamptz;
ALTER TABLE saas_outbox ADD COLUMN dead_lettered_at timestamptz;

CREATE INDEX saas_outbox_claimable ON saas_outbox (tenant_id, next_attempt_at, occurred_at)
    WHERE published_at IS NULL AND dead_lettered_at IS NULL;

CREATE TABLE saas_consumer_checkpoints (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    consumer_name text NOT NULL,
    event_id uuid NOT NULL,
    processed_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, consumer_name, event_id)
);

ALTER TABLE saas_consumer_checkpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_consumer_checkpoints FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_consumer_checkpoints
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid)
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);
