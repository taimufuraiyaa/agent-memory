CREATE TABLE saas_legal_cases (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    source_id uuid NOT NULL,
    case_type text NOT NULL CHECK(case_type IN ('rights_notice','repeat_abuse')),
    jurisdiction text NOT NULL,
    claimant_ref text NOT NULL,
    state text NOT NULL CHECK(state IN ('received','invalid','validated','source_disabled','user_notified','response_received','counter_notice_received','restored','deletion_requested','closed')),
    priority text NOT NULL CHECK(priority IN ('normal','urgent')),
    received_at timestamptz NOT NULL,
    validation_due_at timestamptz NOT NULL,
    response_due_at timestamptz,
    resolution_due_at timestamptz,
    closed_at timestamptz,
    PRIMARY KEY(tenant_id,id),
    FOREIGN KEY(tenant_id,source_id) REFERENCES saas_sources(tenant_id,id)
);

CREATE TABLE saas_legal_case_transitions (
    tenant_id uuid NOT NULL,
    case_id uuid NOT NULL,
    id uuid NOT NULL,
    from_state text NOT NULL,
    to_state text NOT NULL,
    reason_code text NOT NULL,
    actor_id text NOT NULL,
    evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,case_id,id),
    FOREIGN KEY(tenant_id,case_id) REFERENCES saas_legal_cases(tenant_id,id)
);

CREATE TABLE saas_repeat_abuse_decisions (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    case_ids uuid[] NOT NULL,
    decision text NOT NULL CHECK(decision IN ('no_action','warning','upload_restriction','suspension')),
    reason_code text NOT NULL,
    reviewed_by text NOT NULL,
    review_due_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,id)
);

CREATE INDEX saas_legal_cases_state_due ON saas_legal_cases(tenant_id,state,validation_due_at);
CREATE INDEX saas_repeat_abuse_account ON saas_repeat_abuse_decisions(tenant_id,account_id,created_at DESC);
DO $$ DECLARE table_name text; BEGIN
    FOREACH table_name IN ARRAY ARRAY['saas_legal_cases','saas_legal_case_transitions','saas_repeat_abuse_decisions'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',table_name);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id=current_setting(''app.tenant_id'',true)::uuid) WITH CHECK (tenant_id=current_setting(''app.tenant_id'',true)::uuid)',table_name);
    END LOOP;
END $$;

