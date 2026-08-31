package skillreconciler

import "testing"

func TestRuntimeConfigIsDisabledByDefault(t *testing.T) {
	t.Setenv("AGENT_MEMORY_SKILL_RECONCILER_ENABLED", "")
	t.Setenv("AGENT_MEMORY_SKILL_RECONCILER_ASSIGNMENTS", "")
	configuration, err := LoadRuntimeConfig()
	if err != nil || configuration.Enabled {
		t.Fatalf("configuration=%+v err=%v", configuration, err)
	}
}

func TestRuntimeConfigRequiresBoundedUniqueAssignmentsWhenEnabled(t *testing.T) {
	t.Setenv("AGENT_MEMORY_SKILL_RECONCILER_ENABLED", "true")
	t.Setenv("AGENT_MEMORY_SKILL_RECONCILER_DATABASE_URL", "postgres://skill-reconciler@example.test/agent-memory")
	t.Setenv("AGENT_MEMORY_SKILL_RECONCILER_ASSIGNMENTS", `[{"tenant_id":"tenant-a","workspace_id":"workspace-a","environment":"production"}]`)
	t.Setenv("AGENT_MEMORY_SKILL_RECONCILER_PARTITION_LIMIT", "1")
	configuration, err := LoadRuntimeConfig()
	if err != nil || !configuration.Enabled || len(configuration.Assignments) != 1 {
		t.Fatalf("configuration=%+v err=%v", configuration, err)
	}
	t.Setenv("AGENT_MEMORY_SKILL_RECONCILER_ASSIGNMENTS", `[{"tenant_id":"tenant-a","workspace_id":"workspace-a","environment":"production"},{"tenant_id":"tenant-a","workspace_id":"workspace-a","environment":"production"}]`)
	if _, err := LoadRuntimeConfig(); err == nil {
		t.Fatal("duplicate assignment was accepted")
	}
}
