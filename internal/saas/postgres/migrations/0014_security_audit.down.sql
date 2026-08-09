DROP TRIGGER IF EXISTS saas_audit_enqueue_after_insert ON saas_audit_events;
DROP FUNCTION IF EXISTS saas_audit_enqueue_archive();
DROP TABLE IF EXISTS saas_audit_archive_queue;
DROP INDEX IF EXISTS saas_audit_events_target_idx;
DROP INDEX IF EXISTS saas_audit_events_request_idx;
DROP INDEX IF EXISTS saas_audit_events_actor_idx;
DROP INDEX IF EXISTS saas_audit_events_hot_search_idx;
DROP TRIGGER IF EXISTS saas_audit_no_update_or_delete ON saas_audit_events;
DROP FUNCTION IF EXISTS saas_audit_immutable();
DROP TRIGGER IF EXISTS saas_audit_prepare_before_insert ON saas_audit_events;
DROP FUNCTION IF EXISTS saas_audit_prepare();
DROP FUNCTION IF EXISTS saas_audit_safe_json(jsonb);
ALTER TABLE saas_audit_events
    DROP COLUMN IF EXISTS event_hash,
    DROP COLUMN IF EXISTS previous_hash,
    DROP COLUMN IF EXISTS risk_signals,
    DROP COLUMN IF EXISTS reason_code,
    DROP COLUMN IF EXISTS policy_version,
    DROP COLUMN IF EXISTS trace_id,
    DROP COLUMN IF EXISTS service,
    DROP COLUMN IF EXISTS session_ref,
    DROP COLUMN IF EXISTS credential_ref,
    DROP COLUMN IF EXISTS received_at,
    DROP COLUMN IF EXISTS schema_version;
