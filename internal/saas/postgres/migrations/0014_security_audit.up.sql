CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE saas_audit_events
    ADD COLUMN schema_version text NOT NULL DEFAULT 'audit.v1',
    ADD COLUMN received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD COLUMN credential_ref text NOT NULL DEFAULT '',
    ADD COLUMN session_ref text NOT NULL DEFAULT '',
    ADD COLUMN service text NOT NULL DEFAULT '',
    ADD COLUMN trace_id text NOT NULL DEFAULT '',
    ADD COLUMN policy_version text NOT NULL DEFAULT 'baseline-v1',
    ADD COLUMN reason_code text NOT NULL DEFAULT 'none',
    ADD COLUMN risk_signals jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN previous_hash text NOT NULL DEFAULT '',
    ADD COLUMN event_hash text NOT NULL DEFAULT '';

CREATE OR REPLACE FUNCTION saas_audit_safe_json(value jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    item record;
    child jsonb;
    scalar text;
BEGIN
    IF octet_length(value::text) > 4096 THEN RETURN false; END IF;
    IF jsonb_typeof(value) = 'object' THEN
        FOR item IN SELECT key,val FROM jsonb_each(value) AS pairs(key,val) LOOP
            IF item.key ~* '^(content|prompt|response|raw_text|full_text|source_bytes|password|secret|access_token|refresh_token|authorization)$'
               OR NOT saas_audit_safe_json(item.val) THEN RETURN false; END IF;
        END LOOP;
    ELSIF jsonb_typeof(value) = 'array' THEN
        FOR child IN SELECT element FROM jsonb_array_elements(value) AS elements(element) LOOP
            IF NOT saas_audit_safe_json(child) THEN RETURN false; END IF;
        END LOOP;
    ELSIF jsonb_typeof(value) = 'string' THEN
        scalar := value #>> '{}';
        IF length(scalar) > 256 OR scalar ~* '^(Bearer[ ]|AKIA[0-9A-Z]{16}|-----BEGIN[ ])' THEN RETURN false; END IF;
    END IF;
    RETURN true;
END;
$$;

CREATE OR REPLACE FUNCTION saas_audit_prepare() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    prior_hash text;
    canonical text;
BEGIN
    IF jsonb_typeof(NEW.safe_metadata) <> 'object' OR NOT saas_audit_safe_json(NEW.safe_metadata) THEN
        RAISE EXCEPTION 'unsafe audit metadata' USING ERRCODE = '22023';
    END IF;
    IF jsonb_typeof(NEW.risk_signals) <> 'array' OR octet_length(NEW.risk_signals::text) > 2048 THEN
        RAISE EXCEPTION 'invalid audit risk signals' USING ERRCODE = '22023';
    END IF;

    NEW.schema_version := COALESCE(NULLIF(NEW.schema_version, ''), 'audit.v1');
    NEW.received_at := COALESCE(NEW.received_at, clock_timestamp());
    NEW.trace_id := COALESCE(NULLIF(NEW.trace_id, ''), NEW.correlation_id);
    NEW.service := COALESCE(NULLIF(NEW.service, ''), CASE
        WHEN NEW.operation LIKE 'account.%' OR NEW.operation LIKE 'session.%' OR NEW.operation LIKE 'credential.%' THEN 'control'
        WHEN NEW.operation LIKE 'memory.%' OR NEW.operation LIKE 'proposal.%' THEN 'memory'
        WHEN NEW.operation LIKE 'source.%' OR NEW.operation LIKE 'upload.%' THEN 'source'
        WHEN NEW.operation LIKE 'retrieval.%' OR NEW.operation LIKE 'model.%' THEN 'retrieval'
        WHEN NEW.operation LIKE 'deletion.%' THEN 'deletion'
        WHEN NEW.operation LIKE 'billing.%' THEN 'billing'
        WHEN NEW.operation LIKE 'operator.%' OR NEW.operation LIKE 'notice.%' THEN 'operator'
        ELSE 'api'
    END);

    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.tenant_id::text, 17));
    SELECT event_hash INTO prior_hash
      FROM saas_audit_events
     WHERE tenant_id = NEW.tenant_id AND event_hash <> ''
     ORDER BY received_at DESC, id DESC LIMIT 1;
    NEW.previous_hash := COALESCE(prior_hash, '');
    canonical := concat_ws('|', NEW.schema_version, NEW.id::text, NEW.tenant_id::text,
        NEW.occurred_at::text, NEW.received_at::text, NEW.actor_type, NEW.actor_id,
        NEW.credential_ref, NEW.session_ref, NEW.service, NEW.operation, NEW.outcome,
        NEW.request_id, NEW.trace_id, NEW.target_type, NEW.target_id,
        NEW.policy_version, NEW.reason_code, NEW.risk_signals::text,
        NEW.safe_metadata::text, NEW.previous_hash);
    NEW.event_hash := encode(digest(canonical, 'sha256'), 'hex');
    RETURN NEW;
END;
$$;

CREATE TRIGGER saas_audit_prepare_before_insert
BEFORE INSERT ON saas_audit_events
FOR EACH ROW EXECUTE FUNCTION saas_audit_prepare();

CREATE OR REPLACE FUNCTION saas_audit_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit events are immutable' USING ERRCODE = '55000';
END;
$$;

CREATE INDEX saas_audit_events_hot_search_idx
    ON saas_audit_events (tenant_id, occurred_at DESC, operation, outcome);
CREATE INDEX saas_audit_events_actor_idx
    ON saas_audit_events (tenant_id, actor_id, occurred_at DESC);
CREATE INDEX saas_audit_events_request_idx
    ON saas_audit_events (tenant_id, request_id, trace_id);
CREATE INDEX saas_audit_events_target_idx
    ON saas_audit_events (tenant_id, target_type, target_id, occurred_at DESC);

CREATE TABLE saas_audit_archive_queue (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    event_id uuid NOT NULL,
    claim_token uuid,
    claimed_until timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    archived_at timestamptz,
    archive_key text NOT NULL DEFAULT '',
    archive_sha256 text NOT NULL DEFAULT '',
    last_error_code text NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, event_id),
    FOREIGN KEY (tenant_id, event_id) REFERENCES saas_audit_events(tenant_id, id)
);

ALTER TABLE saas_audit_archive_queue ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_audit_archive_queue FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_audit_archive_queue
USING (tenant_id = current_setting('app.tenant_id', true)::uuid)
WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

CREATE OR REPLACE FUNCTION saas_audit_enqueue_archive() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO saas_audit_archive_queue(tenant_id,event_id,next_attempt_at)
    VALUES(NEW.tenant_id,NEW.id,NEW.received_at);
    RETURN NEW;
END;
$$;

CREATE TRIGGER saas_audit_enqueue_after_insert
AFTER INSERT ON saas_audit_events
FOR EACH ROW EXECUTE FUNCTION saas_audit_enqueue_archive();

-- Existing rows predate the chained contract. Give them independently
-- verifiable hashes and enqueue them; the next event anchors the live chain.
UPDATE saas_audit_events SET event_hash = encode(digest(concat_ws('|', id::text,
    tenant_id::text, occurred_at::text, operation, outcome, safe_metadata::text), 'sha256'), 'hex');
INSERT INTO saas_audit_archive_queue(tenant_id,event_id,next_attempt_at)
SELECT tenant_id,id,received_at FROM saas_audit_events ON CONFLICT DO NOTHING;

CREATE TRIGGER saas_audit_no_update_or_delete
BEFORE UPDATE OR DELETE ON saas_audit_events
FOR EACH ROW EXECUTE FUNCTION saas_audit_immutable();
