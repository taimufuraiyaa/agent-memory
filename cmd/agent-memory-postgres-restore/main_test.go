package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformchange"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformplan"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/postgresrestore"
)

func TestRunPublishesContentFreeReadyReceipt(t *testing.T) {
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	paths, secrets := writeFixtures(t, now, true)
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(arguments(paths), &stdout, &stderr, dependencies{now: func() time.Time { return now }})
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var result report
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Ready || !result.ReceiptWritten || result.CheckCount != 10 || result.PassedCount != 10 || result.RPOSeconds != 120 || result.RTOSeconds != 2400 {
		t.Fatalf("unexpected report: %+v", result)
	}
	output := stdout.String()
	for _, secret := range secrets {
		if strings.Contains(output, secret) {
			t.Fatalf("aggregate output disclosed %q", secret)
		}
	}
	info, err := os.Stat(paths.receipt)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%#o", info.Mode().Perm())
	}
}

func TestRunReturnsThreeForValidUnreadyDrill(t *testing.T) {
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	paths, _ := writeFixtures(t, now, false)
	var stdout, stderr bytes.Buffer
	code := runWithDependencies(arguments(paths), &stdout, &stderr, dependencies{now: func() time.Time { return now }})
	if code != 3 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(paths.receipt); err != nil {
		t.Fatal(err)
	}
}

func TestRunSeparatesUsageAndUnsafeEvidenceFailures(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("usage code=%d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--inventory", "missing", "--plan", "missing", "--change", "missing", "--drill", "missing", "--receipt", filepath.Join(t.TempDir(), "receipt.json")}, &stdout, &stderr); code != 1 {
		t.Fatalf("unsafe evidence code=%d", code)
	}
}

type cliPaths struct{ inventory, plan, change, drill, receipt string }

func arguments(paths cliPaths) []string {
	return []string{"--inventory", paths.inventory, "--plan", paths.plan, "--change", paths.change, "--drill", paths.drill, "--receipt", paths.receipt}
}

func writeFixtures(t *testing.T, now time.Time, ready bool) (cliPaths, []string) {
	t.Helper()
	root := repositoryRoot(t)
	directory := t.TempDir()
	paths := cliPaths{
		inventory: filepath.Join(root, "docs", "saas", "self-managed-platform-inventory.example.json"),
		plan:      filepath.Join(root, "docs", "saas", "self-managed-infrastructure-plan.example.json"),
		change:    filepath.Join(root, "docs", "saas", "self-managed-infrastructure-change.example.json"),
		drill:     filepath.Join(directory, "drill.json"), receipt: filepath.Join(directory, "receipt.json"),
	}
	inventory, err := platforminventory.Load(paths.inventory)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := platformplan.Load(paths.plan, inventory)
	if err != nil {
		t.Fatal(err)
	}
	change, err := platformchange.Load(paths.change, inventory, plan)
	if err != nil {
		t.Fatal(err)
	}
	impairment := time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)
	checks := []postgresrestore.Check{}
	ids := []postgresrestore.CheckID{
		postgresrestore.CheckBackupIntegrity, postgresrestore.CheckRestoreCompleted,
		postgresrestore.CheckSchemaMigrationsMatch, postgresrestore.CheckTenantCountsMatch,
		postgresrestore.CheckAuthoritativeRowsMatch, postgresrestore.CheckOutboxReconciled,
		postgresrestore.CheckAuditChainVerified, postgresrestore.CheckDeletionTombstonesReplay,
		postgresrestore.CheckDeletedDataAbsent, postgresrestore.CheckRestoreTargetDisposed,
	}
	for _, id := range ids {
		checks = append(checks, postgresrestore.Check{ID: id, Outcome: postgresrestore.OutcomePassed, EvidenceSHA256: strings.Repeat("e", 64)})
	}
	if !ready {
		checks[8].Outcome = postgresrestore.OutcomeFailed
	}
	drill := postgresrestore.Drill{
		Schema: postgresrestore.DrillSchemaV1, Classification: "self_managed_external", Environment: string(inventory.Environment),
		DrillID: "restore-drill-secret", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256,
		ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, Ready: ready, GeneratedAt: now.Add(-time.Minute),
		Backup:   postgresrestore.Backup{ID: "backup-secret", CreatedAt: impairment.Add(-30 * time.Minute), VerifiedAt: impairment.Add(-25 * time.Minute), ManifestSHA256: strings.Repeat("c", 64), VerificationOutputSHA256: strings.Repeat("d", 64)},
		Timeline: postgresrestore.Timeline{ImpairmentStartedAt: impairment, RecoveryPointAt: impairment.Add(-120 * time.Second), RestoreStartedAt: impairment.Add(5 * time.Minute), RestoreCompletedAt: impairment.Add(20 * time.Minute), ReconciliationCompletedAt: impairment.Add(30 * time.Minute), ServiceReadyAt: impairment.Add(40 * time.Minute), RestoreTargetDisposedAt: impairment.Add(45 * time.Minute)},
		Checks:   checks,
	}
	contents, err := json.Marshal(drill)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.drill, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return paths, []string{drill.DrillID, drill.Backup.ID, inventory.InventoryID, change.ChangeID}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
