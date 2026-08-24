DROP TABLE IF EXISTS saas_legal_holds;
DROP TABLE IF EXISTS saas_deletion_tombstones;
DROP TABLE IF EXISTS saas_deletion_confirmations;
DROP INDEX IF EXISTS saas_deletion_idempotency;
ALTER TABLE saas_deletion_operations DROP COLUMN IF EXISTS updated_at,DROP COLUMN IF EXISTS access_revoked_at,DROP COLUMN IF EXISTS safe_error_code,DROP COLUMN IF EXISTS next_attempt_at,DROP COLUMN IF EXISTS attempts,DROP COLUMN IF EXISTS idempotency_key;
DROP TABLE IF EXISTS saas_retention_policies;
