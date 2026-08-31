CREATE TABLE saas_skill_orchestrator_workflows (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    environment text NOT NULL CHECK (octet_length(environment) BETWEEN 1 AND 64),
    skill_id text NOT NULL DEFAULT '',
    origin_kind text NOT NULL CHECK (origin_kind IN ('solution_episode','tool_lesson','safety_signal','operator','reconciliation')),
    origin_id text NOT NULL CHECK (octet_length(origin_id) BETWEEN 1 AND 256),
    workflow_kind text NOT NULL CHECK (workflow_kind IN ('automatic_revision','safety_rollback','materialization_recovery')),
    contract_version text NOT NULL CHECK (contract_version = 'skill-orchestrator/v1'),
    input_digest text NOT NULL CHECK (input_digest ~ '^sha256:[a-f0-9]{64}$'),
    state text NOT NULL CHECK (state IN ('open','paused','completed','cancelled','rejected','dead_lettered')),
    current_stage text NOT NULL CHECK (current_stage IN ('detect','build','evaluate','decide','start_canary','analyze_canary','activate','observe_safety','rollback','reconcile_materialization')),
    generation bigint NOT NULL CHECK (generation > 0),
    configuration_version bigint NOT NULL CHECK (configuration_version > 0),
    policy_digest text NOT NULL CHECK (policy_digest ~ '^sha256:[a-f0-9]{64}$'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    terminal_at timestamptz,
    PRIMARY KEY (tenant_id,id),
    UNIQUE (tenant_id,workspace_id,id),
    UNIQUE (tenant_id,workspace_id,environment,workflow_kind,origin_kind,origin_id,input_digest),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE
);

CREATE INDEX saas_skill_orchestrator_workflows_status
    ON saas_skill_orchestrator_workflows(tenant_id,workspace_id,environment,state,updated_at DESC,id);

CREATE TABLE saas_skill_orchestrator_jobs (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    workflow_id uuid NOT NULL,
    environment text NOT NULL CHECK (octet_length(environment) BETWEEN 1 AND 64),
    skill_id text NOT NULL DEFAULT '',
    stage text NOT NULL CHECK (stage IN ('detect','build','evaluate','decide','start_canary','analyze_canary','activate','observe_safety','rollback','reconcile_materialization')),
    contract_version text NOT NULL CHECK (contract_version = 'skill-orchestrator/v1'),
    input_digest text NOT NULL CHECK (input_digest ~ '^sha256:[a-f0-9]{64}$'),
    policy_version bigint NOT NULL CHECK (policy_version > 0),
    state text NOT NULL CHECK (state IN ('queued','blocked','running','retry_wait','completed','cancelled','dead_lettered')),
    priority integer NOT NULL CHECK (priority BETWEEN 0 AND 1000000),
    ready_at timestamptz NOT NULL,
    dependency_count integer NOT NULL DEFAULT 0 CHECK (dependency_count BETWEEN 0 AND 32),
    blocked_reason text NOT NULL DEFAULT '' CHECK (octet_length(blocked_reason) <= 128),
    attempt integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 100 AND attempt BETWEEN 0 AND max_attempts),
    lease_owner text NOT NULL DEFAULT '' CHECK (octet_length(lease_owner) <= 256),
    lease_expires_at timestamptz,
    fence bigint NOT NULL DEFAULT 0 CHECK (fence >= 0),
    timeout_at timestamptz,
    cancel_requested_at timestamptz,
    result_kind text NOT NULL DEFAULT '' CHECK (result_kind IN ('','succeeded','rejected','cancelled')),
    result_references jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(result_references) = 'array' AND octet_length(result_references::text) <= 8192),
    failure_class text NOT NULL DEFAULT '' CHECK (failure_class IN ('','contention','dependency_unavailable','insufficient_evidence','policy_block','permanent_validation','safety_rejection','cancellation','unknown_internal')),
    failure_code text NOT NULL DEFAULT '' CHECK (octet_length(failure_code) <= 128),
    replay_of_job_id uuid,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    completed_at timestamptz,
    PRIMARY KEY (tenant_id,id),
    UNIQUE (tenant_id,workspace_id,id),
    UNIQUE (tenant_id,workspace_id,workflow_id,stage,input_digest),
    FOREIGN KEY (tenant_id,workspace_id,workflow_id) REFERENCES saas_skill_orchestrator_workflows(tenant_id,workspace_id,id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id,workspace_id,replay_of_job_id) REFERENCES saas_skill_orchestrator_jobs(tenant_id,workspace_id,id)
);

CREATE INDEX saas_skill_orchestrator_jobs_ready
    ON saas_skill_orchestrator_jobs(tenant_id,workspace_id,environment,state,ready_at,priority DESC,created_at,id);
CREATE INDEX saas_skill_orchestrator_jobs_claim_priority
    ON saas_skill_orchestrator_jobs(tenant_id,workspace_id,environment,priority DESC,ready_at,created_at,id)
    WHERE state='queued';
CREATE INDEX saas_skill_orchestrator_jobs_expired
    ON saas_skill_orchestrator_jobs(tenant_id,state,lease_expires_at,workspace_id,id)
    WHERE state='running';
CREATE INDEX saas_skill_orchestrator_jobs_status
    ON saas_skill_orchestrator_jobs(tenant_id,workspace_id,workflow_id,created_at,id);

CREATE TABLE saas_skill_orchestrator_job_dependencies (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    job_id uuid NOT NULL,
    parent_job_id uuid NOT NULL,
    accepted_result_kinds jsonb NOT NULL CHECK (jsonb_typeof(accepted_result_kinds) = 'array' AND jsonb_array_length(accepted_result_kinds) BETWEEN 1 AND 3),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,job_id,parent_job_id),
    CHECK (job_id <> parent_job_id),
    FOREIGN KEY (tenant_id,workspace_id,job_id) REFERENCES saas_skill_orchestrator_jobs(tenant_id,workspace_id,id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id,workspace_id,parent_job_id) REFERENCES saas_skill_orchestrator_jobs(tenant_id,workspace_id,id) ON DELETE CASCADE
);

CREATE INDEX saas_skill_orchestrator_dependencies_parent
    ON saas_skill_orchestrator_job_dependencies(tenant_id,workspace_id,parent_job_id,job_id);

CREATE TABLE saas_skill_orchestrator_job_attempts (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    job_id uuid NOT NULL,
    attempt integer NOT NULL CHECK (attempt > 0),
    owner text NOT NULL CHECK (octet_length(owner) BETWEEN 1 AND 256),
    fence bigint NOT NULL CHECK (fence > 0),
    started_at timestamptz NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    ended_at timestamptz,
    result_kind text NOT NULL DEFAULT '' CHECK (result_kind IN ('','succeeded','rejected','cancelled')),
    failure_class text NOT NULL DEFAULT '' CHECK (failure_class IN ('','contention','dependency_unavailable','insufficient_evidence','policy_block','permanent_validation','safety_rejection','cancellation','unknown_internal')),
    failure_code text NOT NULL DEFAULT '' CHECK (octet_length(failure_code) <= 128),
    duration_ms bigint NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    renewal_count integer NOT NULL DEFAULT 0 CHECK (renewal_count >= 0),
    PRIMARY KEY (tenant_id,id),
    UNIQUE (tenant_id,workspace_id,job_id,attempt),
    UNIQUE (tenant_id,workspace_id,job_id,fence),
    FOREIGN KEY (tenant_id,workspace_id,job_id) REFERENCES saas_skill_orchestrator_jobs(tenant_id,workspace_id,id) ON DELETE CASCADE
);

CREATE TABLE saas_skill_orchestrator_safety_signals (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id uuid NOT NULL,
    environment text NOT NULL CHECK (octet_length(environment) BETWEEN 1 AND 64),
    skill_id text NOT NULL CHECK (octet_length(skill_id) BETWEEN 1 AND 256),
    revision_id text NOT NULL CHECK (octet_length(revision_id) BETWEEN 1 AND 256),
    source text NOT NULL CHECK (source IN ('digest_custody','capability_audit','verified_execution','materialization','critical_regression')),
    verifier_id text NOT NULL CHECK (octet_length(verifier_id) BETWEEN 1 AND 256),
    severity text NOT NULL CHECK (severity IN ('soft','hard')),
    evidence_reference text NOT NULL CHECK (octet_length(evidence_reference) BETWEEN 1 AND 256),
    deduplication_digest text NOT NULL CHECK (deduplication_digest ~ '^sha256:[a-f0-9]{64}$'),
    policy_version bigint NOT NULL CHECK (policy_version > 0),
    disposition text NOT NULL CHECK (disposition IN ('pending','accepted','rejected')),
    allocation_disabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL CHECK (updated_at >= created_at),
    accepted_at timestamptz,
    PRIMARY KEY (tenant_id,id),
    UNIQUE (tenant_id,workspace_id,id),
    UNIQUE (tenant_id,workspace_id,environment,deduplication_digest),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE
);

CREATE INDEX saas_skill_orchestrator_safety_revision
    ON saas_skill_orchestrator_safety_signals(tenant_id,workspace_id,environment,revision_id,severity,created_at DESC);

CREATE TABLE saas_skill_orchestrator_configurations (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    environment text NOT NULL CHECK (octet_length(environment) BETWEEN 1 AND 64),
    version bigint NOT NULL CHECK (version > 0),
    contract_version text NOT NULL CHECK (contract_version = 'skill-orchestrator/v1'),
    digest text NOT NULL CHECK (digest ~ '^sha256:[a-f0-9]{64}$'),
    mode text NOT NULL CHECK (mode IN ('disabled','shadow','manual','canary','automatic_low_risk')),
    configuration jsonb NOT NULL CHECK (jsonb_typeof(configuration) = 'object' AND octet_length(configuration::text) <= 32768),
    approval_reference text NOT NULL DEFAULT '' CHECK (octet_length(approval_reference) <= 256),
    release_evidence_reference text NOT NULL DEFAULT '' CHECK (octet_length(release_evidence_reference) <= 256),
    signature_reference text NOT NULL DEFAULT '' CHECK (octet_length(signature_reference) <= 256),
    created_by text NOT NULL CHECK (octet_length(created_by) BETWEEN 1 AND 256),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,workspace_id,environment,version),
    UNIQUE (tenant_id,workspace_id,environment,digest),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE
);

CREATE TABLE saas_skill_orchestrator_leader_leases (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    environment text NOT NULL CHECK (octet_length(environment) BETWEEN 1 AND 64),
    lease_kind text NOT NULL CHECK (octet_length(lease_kind) BETWEEN 1 AND 64),
    owner text NOT NULL DEFAULT '' CHECK (octet_length(owner) <= 256),
    fence bigint NOT NULL DEFAULT 0 CHECK (fence >= 0),
    lease_expires_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,workspace_id,environment,lease_kind),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE
);

CREATE TABLE saas_skill_orchestrator_reconciliation_cursors (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    environment text NOT NULL CHECK (octet_length(environment) BETWEEN 1 AND 64),
    domain text NOT NULL CHECK (octet_length(domain) BETWEEN 1 AND 64),
    cursor_value text NOT NULL DEFAULT '' CHECK (octet_length(cursor_value) <= 512),
    configuration_version bigint NOT NULL CHECK (configuration_version > 0),
    last_completed_at timestamptz,
    scanned bigint NOT NULL DEFAULT 0 CHECK (scanned >= 0),
    repaired bigint NOT NULL DEFAULT 0 CHECK (repaired >= 0),
    skipped bigint NOT NULL DEFAULT 0 CHECK (skipped >= 0),
    blocked bigint NOT NULL DEFAULT 0 CHECK (blocked >= 0),
    failed bigint NOT NULL DEFAULT 0 CHECK (failed >= 0),
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,workspace_id,environment,domain),
    FOREIGN KEY (tenant_id, workspace_id) REFERENCES saas_workspaces(tenant_id,id) ON DELETE CASCADE
);

CREATE TABLE saas_skill_orchestrator_events (
    tenant_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    id bigint GENERATED ALWAYS AS IDENTITY,
    workflow_id uuid NOT NULL,
    job_id uuid,
    event_kind text NOT NULL CHECK (octet_length(event_kind) BETWEEN 1 AND 128),
    from_state text NOT NULL DEFAULT '',
    to_state text NOT NULL DEFAULT '',
    actor_id text NOT NULL CHECK (octet_length(actor_id) BETWEEN 1 AND 256),
    fence bigint NOT NULL DEFAULT 0 CHECK (fence >= 0),
    reason_code text NOT NULL DEFAULT '' CHECK (octet_length(reason_code) <= 128),
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id,id),
    FOREIGN KEY (tenant_id,workspace_id,workflow_id) REFERENCES saas_skill_orchestrator_workflows(tenant_id,workspace_id,id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id,workspace_id,job_id) REFERENCES saas_skill_orchestrator_jobs(tenant_id,workspace_id,id) ON DELETE CASCADE
);

CREATE INDEX saas_skill_orchestrator_events_workflow
    ON saas_skill_orchestrator_events(tenant_id,workspace_id,workflow_id,id);

ALTER TABLE saas_skill_orchestrator_workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_workflows FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_workflows USING (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid);
ALTER TABLE saas_skill_orchestrator_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_jobs USING (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid);
ALTER TABLE saas_skill_orchestrator_job_dependencies ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_job_dependencies FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_job_dependencies USING (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid);
ALTER TABLE saas_skill_orchestrator_job_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_job_attempts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_job_attempts USING (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid);
ALTER TABLE saas_skill_orchestrator_safety_signals ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_safety_signals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_safety_signals USING (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid);
ALTER TABLE saas_skill_orchestrator_configurations ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_configurations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_configurations USING (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid);
ALTER TABLE saas_skill_orchestrator_leader_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_leader_leases FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_leader_leases USING (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid);
ALTER TABLE saas_skill_orchestrator_reconciliation_cursors ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_reconciliation_cursors FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_reconciliation_cursors USING (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid);
ALTER TABLE saas_skill_orchestrator_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_skill_orchestrator_events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_workspace_isolation ON saas_skill_orchestrator_events USING (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid) WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid AND workspace_id = current_setting('app.workspace_id', true)::uuid);
