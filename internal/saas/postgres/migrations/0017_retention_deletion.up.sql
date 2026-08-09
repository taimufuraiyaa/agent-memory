CREATE TABLE saas_retention_policies (
    data_class text NOT NULL,
    version text NOT NULL,
    owner text NOT NULL,
    retention_trigger text NOT NULL,
    duration_seconds bigint NOT NULL CHECK (duration_seconds >= 0),
    deletion_method text NOT NULL,
    hold_behavior text NOT NULL,
    migration_plan text NOT NULL,
    customer_impact text NOT NULL,
    effective_at timestamptz NOT NULL,
    retired_at timestamptz,
    PRIMARY KEY(data_class,version)
);
CREATE UNIQUE INDEX saas_retention_policies_active ON saas_retention_policies(data_class) WHERE retired_at IS NULL;

INSERT INTO saas_retention_policies(data_class,version,owner,retention_trigger,duration_seconds,deletion_method,hold_behavior,migration_plan,customer_impact,effective_at) VALUES
('account_identity','retention-v1','control','account_deleted',2592000,'pseudonymize_then_delete','scoped_hold','forward-only; existing deletions keep prior receipt','identity becomes inaccessible immediately; backups age out later',clock_timestamp()),
('sessions_credentials','retention-v1','control','revoked_or_expired',2592000,'hard_delete','never_hold_credentials','expire existing rows by new cutoff','active access is revoked immediately',clock_timestamp()),
('memory_content','retention-v1','memory','user_delete',0,'hard_delete','scoped_hold','enqueue existing deleted rows','content is unavailable before purge completes',clock_timestamp()),
('source_originals','retention-v1','source','source_delete',0,'object_delete','scoped_hold','enqueue active deleting sources','original bytes are revoked immediately',clock_timestamp()),
('source_derived','retention-v1','source','source_delete',0,'database_and_index_delete','scoped_hold','rebuild projections excluding tombstones','passages and indexes purge asynchronously',clock_timestamp()),
('exports','retention-v1','control','export_ready',86400,'object_delete','no_hold','shorten new exports only','download expires after one day',clock_timestamp()),
('model_usage','retention-v1','billing','usage_recorded',31536000,'aggregate_then_delete','scoped_hold','retain existing aggregates','content-free usage remains for reconciliation',clock_timestamp()),
('audit_events','retention-v1','security','event_received',220752000,'compliance_archive_expiry','legal_hold_extends','new objects use current retention; old WORM objects cannot shorten','content-free security records remain for seven years',clock_timestamp()),
('security_cases','retention-v1','trust','case_closed',220752000,'pseudonymize_then_delete','legal_hold_extends','closed cases adopt new cutoff','case accountability remains after content purge',clock_timestamp()),
('billing_records','retention-v1','billing','subscription_closed',220752000,'provider_and_local_delete','legal_hold_extends','reconcile provider before local expiry','financial records follow statutory retention',clock_timestamp()),
('backups','retention-v1','operations','backup_created',2592000,'cryptographic_expiry','holds_do_not_restore_access','apply tombstones during every restore','deleted content can persist encrypted for up to thirty days',clock_timestamp()),
('analytics','retention-v1','operations','event_received',2592000,'aggregate_then_delete','no_content_hold','drop raw partitions at cutoff','only content-free aggregates remain',clock_timestamp());

ALTER TABLE saas_deletion_operations
    ADD COLUMN idempotency_key text NOT NULL DEFAULT '',
    ADD COLUMN attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN safe_error_code text NOT NULL DEFAULT '',
    ADD COLUMN access_revoked_at timestamptz,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT clock_timestamp();
CREATE UNIQUE INDEX saas_deletion_idempotency ON saas_deletion_operations(tenant_id,idempotency_key) WHERE idempotency_key<>'';

CREATE TABLE saas_deletion_confirmations (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    operation_id uuid NOT NULL,
    subsystem text NOT NULL CHECK(subsystem IN ('object','database','index','cache','queue')),
    state text NOT NULL CHECK(state IN ('pending','confirmed','failed')),
    evidence_code text NOT NULL DEFAULT '',
    confirmed_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,operation_id,subsystem),
    FOREIGN KEY(tenant_id,operation_id) REFERENCES saas_deletion_operations(tenant_id,id)
);

CREATE TABLE saas_deletion_tombstones (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    target_type text NOT NULL,
    target_id uuid NOT NULL,
    operation_id uuid NOT NULL,
    deleted_at timestamptz NOT NULL,
    receipt_sha256 text NOT NULL DEFAULT '',
    backup_expires_at timestamptz NOT NULL,
    PRIMARY KEY(tenant_id,target_type,target_id),
    FOREIGN KEY(tenant_id,operation_id) REFERENCES saas_deletion_operations(tenant_id,id)
);

CREATE TABLE saas_legal_holds (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    target_type text NOT NULL,
    target_id uuid NOT NULL,
    reason_code text NOT NULL,
    state text NOT NULL CHECK(state IN ('active','released')),
    approved_by text NOT NULL,
    created_at timestamptz NOT NULL,
    released_at timestamptz,
    PRIMARY KEY(tenant_id,id)
);
CREATE UNIQUE INDEX saas_legal_holds_active ON saas_legal_holds(tenant_id,target_type,target_id) WHERE state='active';

DO $$ DECLARE table_name text; BEGIN
    FOREACH table_name IN ARRAY ARRAY['saas_deletion_confirmations','saas_deletion_tombstones','saas_legal_holds'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',table_name);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',table_name);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id=current_setting(''app.tenant_id'',true)::uuid) WITH CHECK (tenant_id=current_setting(''app.tenant_id'',true)::uuid)',table_name);
    END LOOP;
END $$;

