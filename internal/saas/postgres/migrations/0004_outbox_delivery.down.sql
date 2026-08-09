DROP TABLE IF EXISTS saas_consumer_checkpoints;
DROP INDEX IF EXISTS saas_outbox_claimable;
ALTER TABLE saas_outbox DROP COLUMN IF EXISTS dead_lettered_at;
ALTER TABLE saas_outbox DROP COLUMN IF EXISTS claimed_until;
ALTER TABLE saas_outbox DROP COLUMN IF EXISTS claim_token;
