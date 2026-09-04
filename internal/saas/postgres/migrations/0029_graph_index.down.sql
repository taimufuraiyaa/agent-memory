ALTER TABLE saas_graph_configurations DROP CONSTRAINT IF EXISTS saas_graph_configurations_active_revision_fk;
ALTER TABLE saas_graph_configurations DROP CONSTRAINT IF EXISTS saas_graph_configurations_previous_revision_fk;

DROP TABLE IF EXISTS saas_graph_feedback;
DROP TABLE IF EXISTS saas_graph_reviews;
DROP TABLE IF EXISTS saas_graph_reports;
DROP TABLE IF EXISTS saas_graph_community_members;
DROP TABLE IF EXISTS saas_graph_communities;
DROP TABLE IF EXISTS saas_graph_edge_evidence;
DROP TABLE IF EXISTS saas_graph_edge_versions;
DROP TABLE IF EXISTS saas_graph_edges;
DROP TABLE IF EXISTS saas_graph_entity_evidence;
DROP TABLE IF EXISTS saas_graph_entity_versions;
DROP TABLE IF EXISTS saas_graph_entities;
DROP TABLE IF EXISTS saas_graph_jobs;
DROP TABLE IF EXISTS saas_graph_revisions;
DROP TABLE IF EXISTS saas_graph_configurations;
