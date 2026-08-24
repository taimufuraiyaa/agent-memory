CREATE TABLE saas_plans (
    id text PRIMARY KEY,
    version text NOT NULL,
    name text NOT NULL,
    state text NOT NULL CHECK(state IN ('active','retired')),
    price_micros bigint NOT NULL,
    currency text NOT NULL,
    limits jsonb NOT NULL,
    created_at timestamptz NOT NULL
);
INSERT INTO saas_plans(id,version,name,state,price_micros,currency,limits,created_at) VALUES
('trial','plan-v1','Trial','active',0,'USD','{"max_source_bytes":10485760,"max_source_count":5,"max_concurrent_uploads":1,"max_concurrent_jobs":1,"max_requests_per_minute":60,"max_tokens_per_month":100000,"max_storage_bytes":52428800}',clock_timestamp()),
('individual','plan-v1','Individual','active',12000000,'USD','{"max_source_bytes":104857600,"max_source_count":100,"max_concurrent_uploads":3,"max_concurrent_jobs":3,"max_requests_per_minute":300,"max_tokens_per_month":5000000,"max_storage_bytes":10737418240}',clock_timestamp());

CREATE TABLE saas_subscriptions (
    tenant_id uuid PRIMARY KEY REFERENCES saas_tenants(id),
    provider_customer_ref text NOT NULL DEFAULT '',
    provider_subscription_ref text NOT NULL DEFAULT '',
    plan_id text NOT NULL REFERENCES saas_plans(id),
    state text NOT NULL CHECK(state IN ('trialing','active','past_due','canceled')),
    grace_expires_at timestamptz,
    current_period_ends_at timestamptz,
    last_provider_event_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE saas_billing_webhook_events (
    provider text NOT NULL,
    provider_event_id text NOT NULL,
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    event_type text NOT NULL,
    provider_created_at timestamptz NOT NULL,
    payload_sha256 text NOT NULL,
    applied boolean NOT NULL,
    received_at timestamptz NOT NULL,
    PRIMARY KEY(provider,provider_event_id)
);

CREATE TABLE saas_usage_events (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    usage_key text NOT NULL,
    metric text NOT NULL CHECK(metric IN ('storage_bytes','passages','embeddings','generation_tokens','embedding_tokens','api_requests','jobs','exports')),
    quantity bigint NOT NULL CHECK(quantity>=0),
    source_type text NOT NULL,
    source_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    safe_metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK(saas_audit_safe_json(safe_metadata)),
    PRIMARY KEY(tenant_id,id),
    UNIQUE(tenant_id,usage_key)
);

CREATE TABLE saas_usage_aggregates (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    period_start date NOT NULL,
    metric text NOT NULL,
    quantity bigint NOT NULL,
    reconciled_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,period_start,metric)
);

CREATE TABLE saas_request_rate_windows (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    window_start timestamptz NOT NULL,
    request_count integer NOT NULL,
    PRIMARY KEY(tenant_id,window_start)
);

CREATE TABLE saas_invoices (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    provider_invoice_ref text NOT NULL,
    state text NOT NULL CHECK(state IN ('draft','open','paid','void','uncollectible')),
    amount_due_micros bigint NOT NULL,
    currency text NOT NULL,
    hosted_url text NOT NULL DEFAULT '',
    issued_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,id),
    UNIQUE(tenant_id,provider_invoice_ref)
);

ALTER TABLE saas_tenant_entitlements
    ADD COLUMN plan_id text NOT NULL DEFAULT 'trial' REFERENCES saas_plans(id),
    ADD COLUMN entitlement_version text NOT NULL DEFAULT 'plan-v1',
    ADD COLUMN max_concurrent_jobs integer NOT NULL DEFAULT 1,
    ADD COLUMN max_requests_per_minute integer NOT NULL DEFAULT 60,
    ADD COLUMN max_tokens_per_month bigint NOT NULL DEFAULT 100000,
    ADD COLUMN max_storage_bytes bigint NOT NULL DEFAULT 52428800,
    ADD COLUMN billing_state text NOT NULL DEFAULT 'trialing';

INSERT INTO saas_subscriptions(tenant_id,plan_id,state,last_provider_event_at,updated_at)
SELECT id,'trial','trialing',created_at,created_at FROM saas_tenants ON CONFLICT DO NOTHING;

CREATE INDEX saas_usage_events_metric_time ON saas_usage_events(tenant_id,metric,occurred_at);
CREATE INDEX saas_billing_webhook_tenant ON saas_billing_webhook_events(tenant_id,provider_created_at DESC);
DO $$ DECLARE table_name text; BEGIN
    FOREACH table_name IN ARRAY ARRAY['saas_subscriptions','saas_billing_webhook_events','saas_usage_events','saas_usage_aggregates','saas_request_rate_windows','saas_invoices'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',table_name);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id=current_setting(''app.tenant_id'',true)::uuid) WITH CHECK (tenant_id=current_setting(''app.tenant_id'',true)::uuid)',table_name);
    END LOOP;
END $$;
