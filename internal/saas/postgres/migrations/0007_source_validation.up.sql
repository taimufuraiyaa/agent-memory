ALTER TABLE saas_upload_grants DROP CONSTRAINT saas_upload_grants_state_check;
ALTER TABLE saas_upload_grants ADD CONSTRAINT saas_upload_grants_state_check CHECK
    (state IN ('issued','uploading','uploaded','validating','promoted','failed','expired'));
ALTER TABLE saas_source_versions ADD COLUMN vault_encryption_version text NOT NULL DEFAULT '';
ALTER TABLE saas_upload_grants ADD COLUMN safe_error_code text NOT NULL DEFAULT '';
