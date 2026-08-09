UPDATE saas_upload_grants SET state='failed' WHERE state IN ('validating','promoted');
ALTER TABLE saas_upload_grants DROP CONSTRAINT saas_upload_grants_state_check;
ALTER TABLE saas_upload_grants ADD CONSTRAINT saas_upload_grants_state_check CHECK
    (state IN ('issued','uploading','uploaded','failed','expired'));
ALTER TABLE saas_source_versions DROP COLUMN IF EXISTS vault_encryption_version;
ALTER TABLE saas_upload_grants DROP COLUMN IF EXISTS safe_error_code;
