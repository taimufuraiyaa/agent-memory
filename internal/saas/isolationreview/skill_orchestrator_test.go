package isolationreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillOrchestratorReleaseBoundaryIsForcedRLSAndLeastPrivilege(t *testing.T) {
	root := repositoryRoot(t)
	migration := readSecurityFixture(t, filepath.Join(root, "internal", "saas", "postgres", "migrations", "0033_skill_background_orchestrator.up.sql"))
	roles := readSecurityFixture(t, filepath.Join(root, "internal", "saas", "postgres", "migrations", "0035_skill_runtime_roles.up.sql"))
	deployment := readSecurityFixture(t, filepath.Join(root, "deploy", "saas", "kubernetes", "base", "deployments.yaml"))
	accounts := readSecurityFixture(t, filepath.Join(root, "deploy", "saas", "kubernetes", "base", "accounts.yaml"))

	for _, table := range []string{
		"workflows", "jobs", "job_dependencies", "job_attempts", "safety_signals",
		"configurations", "leader_leases", "reconciliation_cursors", "events",
	} {
		name := "saas_skill_orchestrator_" + table
		if !strings.Contains(migration, "ALTER TABLE "+name+" FORCE ROW LEVEL SECURITY") ||
			!strings.Contains(migration, "CREATE POLICY tenant_workspace_isolation ON "+name) {
			t.Fatalf("%s lacks forced tenant/workspace RLS", name)
		}
	}
	for _, requirement := range []string{"NOINHERIT", "NOBYPASSRLS", "GRANT SELECT, INSERT, UPDATE ON", "agent_memory_skill_worker"} {
		if !strings.Contains(roles, requirement) {
			t.Fatalf("skill worker database role lacks %q", requirement)
		}
	}
	for _, forbidden := range []string{"GRANT DELETE", "GRANT TRUNCATE", "GRANT REFERENCES", "GRANT TRIGGER"} {
		if strings.Contains(roles, forbidden) {
			t.Fatalf("skill worker role gained forbidden capability %q", forbidden)
		}
	}
	skillWorkerStart := strings.Index(deployment, "name: agent-memory-skill-worker")
	if skillWorkerStart < 0 {
		t.Fatal("skill worker deployment is missing")
	}
	skillWorker := deployment[skillWorkerStart:]
	for _, requirement := range []string{"runAsNonRoot: true", "automountServiceAccountToken: false", "allowPrivilegeEscalation: false", "drop: [\"ALL\"]", "readOnlyRootFilesystem: true"} {
		if !strings.Contains(skillWorker, requirement) {
			t.Fatalf("skill worker deployment lacks %q", requirement)
		}
	}
	if !strings.Contains(accounts, "name: agent-memory-skill-worker\nautomountServiceAccountToken: false") {
		t.Fatal("skill worker service account token is not disabled")
	}
}

func readSecurityFixture(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
