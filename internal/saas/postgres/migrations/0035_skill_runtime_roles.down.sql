REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM agent_memory_skill_worker, agent_memory_skill_reconciler;
REVOKE USAGE ON SCHEMA public FROM agent_memory_skill_worker, agent_memory_skill_reconciler;
DROP ROLE IF EXISTS agent_memory_skill_worker;
DROP ROLE IF EXISTS agent_memory_skill_reconciler;
