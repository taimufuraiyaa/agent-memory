CREATE TABLE saas_model_usage (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    source_id uuid,
    source_version bigint NOT NULL DEFAULT 0,
    operation text NOT NULL CHECK(operation IN ('embed','generate')),
    provider text NOT NULL,
    model text NOT NULL,
    dimensions integer NOT NULL DEFAULT 0,
    input_tokens integer NOT NULL,
    output_tokens integer NOT NULL,
    estimated_cost_micros bigint NOT NULL,
    outcome text NOT NULL CHECK(outcome IN ('success','failed')),
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,id)
);
CREATE INDEX saas_model_usage_billing ON saas_model_usage(tenant_id,occurred_at,provider,model);
ALTER TABLE saas_model_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_model_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_model_usage
    USING (tenant_id=current_setting('app.tenant_id',true)::uuid)
    WITH CHECK (tenant_id=current_setting('app.tenant_id',true)::uuid);
