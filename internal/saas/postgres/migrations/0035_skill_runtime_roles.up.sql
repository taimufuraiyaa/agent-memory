DO $roles$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'agent_memory_skill_worker') THEN
        CREATE ROLE agent_memory_skill_worker LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'agent_memory_skill_reconciler') THEN
        CREATE ROLE agent_memory_skill_reconciler LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
    END IF;
END
$roles$;

GRANT USAGE ON SCHEMA public TO agent_memory_skill_worker, agent_memory_skill_reconciler;

GRANT SELECT, INSERT, UPDATE ON
    saas_skill_orchestrator_workflows,
    saas_skill_orchestrator_jobs,
    saas_skill_orchestrator_job_dependencies,
    saas_skill_orchestrator_job_attempts,
    saas_skill_orchestrator_safety_signals,
    saas_skill_orchestrator_events
TO agent_memory_skill_worker;
GRANT SELECT ON saas_skill_orchestrator_configurations TO agent_memory_skill_worker;

GRANT SELECT, INSERT, UPDATE ON
    saas_skill_orchestrator_workflows,
    saas_skill_orchestrator_jobs,
    saas_skill_orchestrator_job_dependencies,
    saas_skill_orchestrator_job_attempts,
    saas_skill_orchestrator_safety_signals,
    saas_skill_orchestrator_events,
    saas_skill_orchestrator_reconciliation_cursors,
    saas_skill_orchestrator_reconciliation_partitions
TO agent_memory_skill_reconciler;
GRANT SELECT ON saas_skill_orchestrator_configurations TO agent_memory_skill_reconciler;
