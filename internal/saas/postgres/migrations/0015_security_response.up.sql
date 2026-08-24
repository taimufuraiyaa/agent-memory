CREATE TABLE saas_security_findings (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    rule_id text NOT NULL,
    severity text NOT NULL CHECK (severity IN ('low','medium','high','critical')),
    summary_code text NOT NULL,
    state text NOT NULL CHECK (state IN ('open','contained','false_positive','overridden','resolved')),
    evidence_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
    first_observed_at timestamptz NOT NULL,
    last_observed_at timestamptz NOT NULL,
    reviewed_by text NOT NULL DEFAULT '',
    review_reason text NOT NULL DEFAULT '',
    reviewed_at timestamptz,
    PRIMARY KEY (tenant_id,id),
    UNIQUE (tenant_id,rule_id,summary_code,first_observed_at)
);

CREATE TABLE saas_security_policies (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    action text NOT NULL CHECK (action IN ('rate_limit','credential_revoke','upload_quarantine','source_disable','tenant_suspend')),
    enabled boolean NOT NULL,
    minimum_severity text NOT NULL CHECK (minimum_severity IN ('low','medium','high','critical')),
    approval_required boolean NOT NULL,
    policy_version text NOT NULL,
    updated_by text NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,action)
);

CREATE TABLE saas_containment_actions (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    finding_id uuid NOT NULL,
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL,
    state text NOT NULL CHECK (state IN ('proposed','approved','executed','denied','overridden','failed')),
    policy_version text NOT NULL,
    requested_by text NOT NULL,
    approved_by text NOT NULL DEFAULT '',
    reason_code text NOT NULL,
    expires_at timestamptz,
    created_at timestamptz NOT NULL,
    executed_at timestamptz,
    PRIMARY KEY (tenant_id,id),
    FOREIGN KEY (tenant_id,finding_id) REFERENCES saas_security_findings(tenant_id,id)
);

CREATE TABLE saas_security_incidents (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    finding_id uuid NOT NULL,
    severity text NOT NULL,
    state text NOT NULL CHECK (state IN ('open','acknowledged','resolved')),
    page_required boolean NOT NULL,
    created_at timestamptz NOT NULL,
    acknowledged_at timestamptz,
    resolved_at timestamptz,
    PRIMARY KEY (tenant_id,id),
    FOREIGN KEY (tenant_id,finding_id) REFERENCES saas_security_findings(tenant_id,id)
);

CREATE TABLE saas_tenant_security_controls (
    tenant_id uuid PRIMARY KEY REFERENCES saas_tenants(id),
    rate_limited_until timestamptz,
    uploads_quarantined_until timestamptz,
    updated_at timestamptz NOT NULL
);

CREATE INDEX saas_security_findings_open_idx ON saas_security_findings(tenant_id,state,severity,last_observed_at DESC);
CREATE INDEX saas_containment_actions_state_idx ON saas_containment_actions(tenant_id,state,created_at DESC);

DO $$ DECLARE table_name text; BEGIN
    FOREACH table_name IN ARRAY ARRAY['saas_security_findings','saas_security_policies','saas_containment_actions','saas_security_incidents','saas_tenant_security_controls']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', table_name);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = current_setting(''app.tenant_id'', true)::uuid) WITH CHECK (tenant_id = current_setting(''app.tenant_id'', true)::uuid)', table_name);
    END LOOP;
END $$;

