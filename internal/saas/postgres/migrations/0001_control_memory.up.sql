CREATE TABLE saas_accounts (
    id uuid PRIMARY KEY,
    external_subject text NOT NULL UNIQUE,
    verified_email text NOT NULL,
    display_name text NOT NULL DEFAULT '',
    state text NOT NULL CHECK (state IN ('active', 'suspended', 'deleting', 'deleted')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE saas_tenants (
    id uuid PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('personal')),
    state text NOT NULL CHECK (state IN ('active', 'suspended', 'deleting', 'deleted')),
    personal_owner_account_id uuid NOT NULL UNIQUE REFERENCES saas_accounts(id),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE saas_memberships (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    account_id uuid NOT NULL REFERENCES saas_accounts(id),
    role text NOT NULL CHECK (role IN ('owner')),
    state text NOT NULL CHECK (state IN ('active', 'suspended', 'revoked')),
    capability_version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, account_id)
);

CREATE TABLE saas_onboarding_states (
    tenant_id uuid NOT NULL,
    account_id uuid NOT NULL,
    step text NOT NULL,
    completed_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, account_id),
    FOREIGN KEY (tenant_id, account_id) REFERENCES saas_memberships(tenant_id, account_id)
);

CREATE TABLE saas_sessions (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    provider_session_id text NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, provider_session_id),
    FOREIGN KEY (tenant_id, account_id) REFERENCES saas_memberships(tenant_id, account_id)
);

CREATE TABLE saas_api_credentials (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    account_id uuid NOT NULL,
    verifier_hash bytea NOT NULL,
    label text NOT NULL,
    scopes text[] NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, account_id) REFERENCES saas_memberships(tenant_id, account_id)
);

CREATE TABLE saas_attestation_receipts (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    subject_id uuid NOT NULL REFERENCES saas_accounts(id),
    policy_version text NOT NULL,
    statement_digest text NOT NULL,
    accepted_statement_ids jsonb NOT NULL,
    accepted_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    request_id text NOT NULL,
    user_agent text NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

CREATE INDEX saas_attestation_receipts_latest
    ON saas_attestation_receipts (tenant_id, subject_id, accepted_at DESC, id DESC);

CREATE TABLE saas_attestation_audit_events (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    subject_id uuid NOT NULL REFERENCES saas_accounts(id),
    operation text NOT NULL,
    outcome text NOT NULL,
    policy_version text NOT NULL,
    receipt_id uuid,
    request_id text NOT NULL,
    reason text NOT NULL,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, receipt_id) REFERENCES saas_attestation_receipts(tenant_id, id)
);

CREATE TABLE saas_workspaces (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    name text NOT NULL,
    state text NOT NULL CHECK (state IN ('active', 'deleting', 'deleted')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, name)
);

CREATE TABLE saas_notes (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    title text NOT NULL,
    body text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id, id)
);

CREATE TABLE saas_sources (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('pending', 'ready', 'failed', 'disabled', 'deleting', 'deleted')),
    rights_basis text NOT NULL,
    attestation_receipt_id uuid NOT NULL,
    active_version bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id, id),
    FOREIGN KEY (tenant_id, attestation_receipt_id) REFERENCES saas_attestation_receipts(tenant_id, id)
);

CREATE TABLE saas_source_versions (
    tenant_id uuid NOT NULL,
    source_id uuid NOT NULL,
    version bigint NOT NULL,
    content_sha256 text NOT NULL,
    media_type text NOT NULL,
    parser_version text NOT NULL,
    normalization_version text NOT NULL,
    vault_object_key text NOT NULL,
    published_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, source_id, version),
    FOREIGN KEY (tenant_id, source_id) REFERENCES saas_sources(tenant_id, id),
    UNIQUE (tenant_id, content_sha256)
);

CREATE TABLE saas_memories (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    memory_type text NOT NULL,
    content text NOT NULL,
    content_hash text NOT NULL,
    source_kind text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id, id),
    UNIQUE (tenant_id, workspace_id, content_hash)
);

CREATE TABLE saas_feedback (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    memory_id uuid NOT NULL,
    request_id text NOT NULL,
    outcome text NOT NULL,
    reason_category text NOT NULL DEFAULT '',
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, memory_id) REFERENCES saas_memories(tenant_id, id),
    UNIQUE (tenant_id, memory_id, request_id)
);

CREATE TABLE saas_sessions_memory (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('active', 'completed', 'failed')),
    started_at timestamptz NOT NULL,
    completed_at timestamptz,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id, id)
);

CREATE TABLE saas_jobs (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    job_type text NOT NULL,
    subject_type text NOT NULL,
    subject_id uuid NOT NULL,
    deterministic_key text NOT NULL,
    state text NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL,
    started_at timestamptz,
    finished_at timestamptz,
    safe_error_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, deterministic_key)
);

CREATE TABLE saas_lineage_edges (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    from_type text NOT NULL,
    from_id uuid NOT NULL,
    to_type text NOT NULL,
    to_id uuid NOT NULL,
    transformation text NOT NULL,
    transformation_version text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, from_type, from_id, to_type, to_id, transformation, transformation_version)
);

CREATE TABLE saas_outbox (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    event_type text NOT NULL,
    spec_version text NOT NULL,
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    published_at timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    last_error_code text NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, id)
);

CREATE INDEX saas_outbox_unpublished
    ON saas_outbox (next_attempt_at, occurred_at) WHERE published_at IS NULL;

CREATE TABLE saas_deletion_operations (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    target_type text NOT NULL,
    target_id uuid NOT NULL,
    policy_version text NOT NULL,
    state text NOT NULL CHECK (state IN ('requested', 'revoked', 'purging', 'verifying', 'completed', 'failed', 'held')),
    requested_at timestamptz NOT NULL,
    completed_at timestamptz,
    receipt_sha256 text NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, target_type, target_id)
);

CREATE TABLE saas_audit_events (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    operation text NOT NULL,
    outcome text NOT NULL,
    request_id text NOT NULL,
    correlation_id text NOT NULL,
    target_type text NOT NULL,
    target_id text NOT NULL,
    safe_metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

ALTER TABLE saas_memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_memberships FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_memberships USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_onboarding_states ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_onboarding_states FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_onboarding_states USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_sessions USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_api_credentials ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_api_credentials FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_api_credentials USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_attestation_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_attestation_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_attestation_receipts USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_attestation_audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_attestation_audit_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_attestation_audit_events USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_workspaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_workspaces FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_workspaces USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_notes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_notes USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_sources FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_sources USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_source_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_source_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_source_versions USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_memories ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_memories FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_memories USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_feedback ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_feedback FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_feedback USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_sessions_memory ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_sessions_memory FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_sessions_memory USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_jobs USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_lineage_edges ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_lineage_edges FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_lineage_edges USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_outbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_outbox FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_outbox USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_deletion_operations ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_deletion_operations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_deletion_operations USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE saas_audit_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_audit_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_audit_events USING (tenant_id = current_setting('app.tenant_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

