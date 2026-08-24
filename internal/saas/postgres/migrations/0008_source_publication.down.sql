DROP TABLE saas_source_citations;
DROP TABLE saas_source_passages;
DROP TABLE saas_source_nodes;
DROP INDEX saas_source_versions_content_lookup;
ALTER TABLE saas_source_versions ADD CONSTRAINT saas_source_versions_tenant_id_content_sha256_key UNIQUE(tenant_id,content_sha256);
ALTER TABLE saas_jobs DROP COLUMN lease_expires_at;
