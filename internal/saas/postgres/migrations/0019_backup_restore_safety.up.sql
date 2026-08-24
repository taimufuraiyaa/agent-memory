CREATE TABLE saas_backup_restore_drills (
    tenant_id uuid NOT NULL REFERENCES saas_tenants(id),
    id uuid NOT NULL,
    backup_created_at timestamptz NOT NULL,
    restored_at timestamptz NOT NULL,
    tombstones_replayed integer NOT NULL,
    exposed_deleted_count integer NOT NULL,
    outcome text NOT NULL CHECK(outcome IN ('passed','failed')),
    evidence_sha256 text NOT NULL,
    PRIMARY KEY(tenant_id,id)
);
ALTER TABLE saas_backup_restore_drills ENABLE ROW LEVEL SECURITY;
ALTER TABLE saas_backup_restore_drills FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saas_backup_restore_drills USING(tenant_id=current_setting('app.tenant_id',true)::uuid) WITH CHECK(tenant_id=current_setting('app.tenant_id',true)::uuid);
