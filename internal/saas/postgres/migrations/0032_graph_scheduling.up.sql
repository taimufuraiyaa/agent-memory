ALTER TABLE saas_graph_change_journal ADD COLUMN configuration_id uuid;

UPDATE saas_graph_change_journal journal
SET configuration_id = configuration.id
FROM saas_graph_configurations configuration
WHERE configuration.tenant_id = journal.tenant_id
  AND configuration.workspace_id = journal.workspace_id
  AND configuration.projection_version = journal.projection_version
  AND configuration.version::text = journal.configuration_version;

DELETE FROM saas_graph_change_journal WHERE configuration_id IS NULL;
ALTER TABLE saas_graph_change_journal ALTER COLUMN configuration_id SET NOT NULL;
ALTER TABLE saas_graph_change_journal
  ADD CONSTRAINT saas_graph_change_journal_configuration_fk
  FOREIGN KEY (tenant_id,workspace_id,configuration_id)
  REFERENCES saas_graph_configurations(tenant_id,workspace_id,id) ON DELETE CASCADE;

CREATE INDEX saas_graph_change_journal_pending
  ON saas_graph_change_journal(tenant_id,workspace_id,configuration_id,occurred_at,id)
  WHERE processed_revision_id IS NULL;
