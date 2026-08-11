ALTER TABLE saas_retention_policies
    DROP CONSTRAINT IF EXISTS saas_retention_policy_purpose,
    DROP COLUMN IF EXISTS purpose;
