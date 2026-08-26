DROP TABLE IF EXISTS saas_graph_change_journal;
DROP TABLE IF EXISTS saas_graph_completion_events;
ALTER TABLE saas_graph_reports DROP COLUMN IF EXISTS review_version, DROP COLUMN IF EXISTS evidence_fingerprint, DROP COLUMN IF EXISTS membership_fingerprint, DROP COLUMN IF EXISTS prompt_fingerprint, DROP COLUMN IF EXISTS model_fingerprint, DROP COLUMN IF EXISTS model_route, DROP COLUMN IF EXISTS admission_state;
ALTER TABLE saas_graph_communities DROP COLUMN IF EXISTS evidence_fingerprint, DROP COLUMN IF EXISTS membership_fingerprint, DROP COLUMN IF EXISTS configuration_id;
ALTER TABLE saas_graph_edge_versions DROP COLUMN IF EXISTS provenance_approved, DROP COLUMN IF EXISTS origin;
