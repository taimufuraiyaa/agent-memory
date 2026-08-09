CREATE TABLE saas_launch_policy (
    singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
    phase text NOT NULL CHECK(phase IN ('internal_alpha','private_beta','public_beta','ga')),
    signup_enabled boolean NOT NULL,
    invitation_required boolean NOT NULL,
    allowed_countries text[] NOT NULL,
    minimum_age integer NOT NULL CHECK(minimum_age BETWEEN 13 AND 21),
    account_cap integer NOT NULL CHECK(account_cap > 0),
    trial_days integer NOT NULL CHECK(trial_days BETWEEN 0 AND 365),
    source_cap integer NOT NULL CHECK(source_cap > 0),
    signup_rate_per_hour integer NOT NULL CHECK(signup_rate_per_hour > 0),
    abuse_rejection_limit integer NOT NULL CHECK(abuse_rejection_limit > 0),
    policy_version text NOT NULL,
    updated_at timestamptz NOT NULL
);

INSERT INTO saas_launch_policy(singleton,phase,signup_enabled,invitation_required,allowed_countries,minimum_age,account_cap,trial_days,source_cap,signup_rate_per_hour,abuse_rejection_limit,policy_version,updated_at)
VALUES(true,'internal_alpha',true,true,ARRAY['VN'],18,100,30,5,10,5,'launch-v1',clock_timestamp());

CREATE TABLE saas_launch_invitations (
    token_sha256 text PRIMARY KEY CHECK(length(token_sha256)=64),
    email_sha256 text NOT NULL CHECK(length(email_sha256)=64),
    state text NOT NULL CHECK(state IN ('active','revoked','exhausted','expired')),
    max_uses integer NOT NULL CHECK(max_uses > 0),
    reserved_uses integer NOT NULL DEFAULT 0 CHECK(reserved_uses >= 0),
    completed_uses integer NOT NULL DEFAULT 0 CHECK(completed_uses >= 0),
    expires_at timestamptz NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE saas_signup_reservations (
    id uuid PRIMARY KEY,
    external_subject_sha256 text NOT NULL CHECK(length(external_subject_sha256)=64),
    email_sha256 text NOT NULL CHECK(length(email_sha256)=64),
    network_sha256 text NOT NULL CHECK(length(network_sha256)=64),
    invitation_sha256 text REFERENCES saas_launch_invitations(token_sha256),
    country text NOT NULL,
    policy_version text NOT NULL,
    state text NOT NULL CHECK(state IN ('reserved','completed','cancelled','expired')),
    reserved_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    completed_at timestamptz
);
CREATE INDEX saas_signup_reservations_rate_idx ON saas_signup_reservations(network_sha256,reserved_at DESC);

CREATE TABLE saas_signup_attempts (
    id uuid PRIMARY KEY,
    email_sha256 text NOT NULL CHECK(length(email_sha256)=64),
    network_sha256 text NOT NULL CHECK(length(network_sha256)=64),
    country text NOT NULL,
    phase text NOT NULL,
    outcome text NOT NULL CHECK(outcome IN ('reserved','completed','rejected','cancelled')),
    reason_code text NOT NULL,
    occurred_at timestamptz NOT NULL
);
CREATE INDEX saas_signup_attempts_abuse_idx ON saas_signup_attempts(network_sha256,occurred_at DESC);

CREATE TABLE saas_tenant_launch_controls (
    tenant_id uuid PRIMARY KEY REFERENCES saas_tenants(id) ON DELETE CASCADE,
    source_cap integer NOT NULL CHECK(source_cap > 0),
    trial_expires_at timestamptz,
    feature_flags jsonb NOT NULL DEFAULT '{}'::jsonb,
    workload_mode text NOT NULL DEFAULT 'normal' CHECK(workload_mode IN ('normal','reduced','read_only','uploads_paused')),
    policy_version text NOT NULL,
    updated_at timestamptz NOT NULL
);
INSERT INTO saas_tenant_launch_controls(tenant_id,source_cap,trial_expires_at,feature_flags,policy_version,updated_at)
SELECT t.id,p.source_cap,t.created_at+(p.trial_days * interval '1 day'),'{"source_upload":true,"generation":true,"exports":true}'::jsonb,p.policy_version,clock_timestamp()
FROM saas_tenants t CROSS JOIN saas_launch_policy p
WHERE p.singleton=true
ON CONFLICT(tenant_id) DO NOTHING;
ALTER TABLE saas_tenant_launch_controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_tenant_launch_controls FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_saas_tenant_launch_controls ON saas_tenant_launch_controls
USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE saas_product_analytics (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id) ON DELETE CASCADE,
    id uuid NOT NULL,
    event_name text NOT NULL,
    outcome text NOT NULL,
    safe_dimensions jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,id)
);
ALTER TABLE saas_product_analytics ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_product_analytics FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation_saas_product_analytics ON saas_product_analytics
USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE saas_failure_ownership (
    failure_class text PRIMARY KEY,
    owner text NOT NULL,
    resolution_target_seconds bigint NOT NULL CHECK(resolution_target_seconds > 0),
    escalation_policy text NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE saas_game_day_drills (
    id uuid PRIMARY KEY,
    scenario text NOT NULL,
    owner text NOT NULL,
    outcome text NOT NULL CHECK(outcome IN ('passed','failed')),
    started_at timestamptz NOT NULL,
    completed_at timestamptz NOT NULL,
    evidence_sha256 text NOT NULL CHECK(length(evidence_sha256)=64),
    safe_summary jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE saas_release_evidence (
    id uuid PRIMARY KEY,
    gate text NOT NULL,
    metric text NOT NULL,
    value double precision NOT NULL,
    threshold double precision NOT NULL,
    comparator text NOT NULL CHECK(comparator IN ('lte','gte','eq')),
    owner text NOT NULL,
    observed_at timestamptz NOT NULL,
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    source_ref text NOT NULL,
    UNIQUE(gate,metric,window_start,window_end)
);
