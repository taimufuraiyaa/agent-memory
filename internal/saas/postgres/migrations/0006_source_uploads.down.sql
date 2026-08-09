DROP TABLE IF EXISTS saas_upload_grants;
DROP TABLE IF EXISTS saas_tenant_entitlements;
UPDATE saas_sources SET state='failed' WHERE state IN ('uploading','validating','processing','indexing');
ALTER TABLE saas_sources DROP CONSTRAINT saas_sources_state_check;
ALTER TABLE saas_sources ADD CONSTRAINT saas_sources_state_check CHECK
    (state IN ('pending','ready','failed','disabled','deleting','deleted'));
