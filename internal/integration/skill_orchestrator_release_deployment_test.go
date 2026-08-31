package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillOrchestratorProductionDeploymentIsDefaultOffAndRunbookBound(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	deployment := readReleaseArtifact(t, filepath.Join(repositoryRoot, "deploy", "saas", "kubernetes", "base", "deployments.yaml"))
	skillWorker := deployment[strings.Index(deployment, "name: agent-memory-skill-worker"):]
	for _, required := range []string{
		"replicas: 0",
		"terminationGracePeriodSeconds: 40",
		"name: AGENT_MEMORY_SKILL_WORKER_ENABLED\n              value: \"false\"",
		"name: AGENT_MEMORY_SKILL_WORKER_DATABASE_ROLE\n              value: agent_memory_skill_worker",
	} {
		if !strings.Contains(skillWorker, required) {
			t.Fatalf("default-off skill-worker deployment is missing %q", required)
		}
	}

	alerts := readReleaseArtifact(t, filepath.Join(repositoryRoot, "deploy", "saas", "observability", "skill-lifecycle-alerts.yaml"))
	for _, alert := range []string{
		"AgentMemorySkillOrchestratorStuckReadyWork", "AgentMemorySkillOrchestratorLeaseChurn",
		"AgentMemorySkillOrchestratorDeadLetters", "AgentMemorySkillOrchestratorEvaluatorOutage",
		"AgentMemorySkillOrchestratorStaleCanary", "AgentMemorySkillOrchestratorRollbackFailure",
		"AgentMemorySkillOrchestratorReconciliationDrift", "AgentMemorySkillOrchestratorUnexpectedActivation",
	} {
		if !strings.Contains(alerts, "alert: "+alert) {
			t.Fatalf("alert routing is missing %s", alert)
		}
	}

	runbook := readReleaseArtifact(t, filepath.Join(repositoryRoot, "docs", "runbooks", "skill-orchestrator-production-release.md"))
	for _, heading := range []string{
		"## Preconditions", "## Disabled drill", "## Shadow drill", "## Manual drill", "## Canary drill",
		"## Automatic low-risk approval", "## Pause and drain drill", "## Restore drill", "## Complete shutdown drill",
		"## Evidence signing and verification", "## Abort conditions",
	} {
		if !strings.Contains(runbook, heading) {
			t.Fatalf("production runbook is missing %q", heading)
		}
	}
	for _, invariant := range []string{
		"active skill digest", "audit record count", "rollback slo", "configuration digest",
		"build digest", "migration digest", "accountable product",
	} {
		if !strings.Contains(strings.ToLower(runbook), invariant) {
			t.Fatalf("production runbook is missing invariant %q", invariant)
		}
	}
}

func readReleaseArtifact(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
