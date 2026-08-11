package postgresrestore

import (
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
)

func TestCollectBindsReadyRestoreToReadyPlatformChain(t *testing.T) {
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	paths, drill := readyFixture(t, now)
	receipt, err := Collect(paths.inventory, paths.plan, paths.change, writeDrill(t, drill), now)
	if err != nil {
		t.Fatal(err)
	}
	assessment := Assess(receipt)
	if !assessment.Ready || assessment.RPOSeconds != 120 || assessment.RTOSeconds != 2400 || assessment.PassedCount != 10 || assessment.FailedCount != 0 {
		t.Fatalf("unexpected assessment: %+v", assessment)
	}
	if receipt.InventoryID != drill.InventoryID || receipt.ChangeID != drill.ChangeID || receipt.Backup.ID != drill.Backup.ID || !digestPattern.MatchString(receipt.InputSHA256) {
		t.Fatalf("receipt did not retain content-free chain binding: %+v", receipt)
	}
}

func TestCollectKeepsFailedCheckOrTargetBreachValidButUnready(t *testing.T) {
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*Drill){
		"failed check": func(drill *Drill) {
			drill.Checks[8].Outcome = OutcomeFailed
			drill.Ready = false
		},
		"RPO breach": func(drill *Drill) {
			drill.Timeline.RecoveryPointAt = drill.Timeline.ImpairmentStartedAt.Add(-301 * time.Second)
			drill.Ready = false
		},
		"fractional RPO breach": func(drill *Drill) {
			drill.Timeline.RecoveryPointAt = drill.Timeline.ImpairmentStartedAt.Add(-300*time.Second - time.Nanosecond)
			drill.Ready = false
		},
		"RTO breach": func(drill *Drill) {
			drill.Timeline.ServiceReadyAt = drill.Timeline.ImpairmentStartedAt.Add(3601 * time.Second)
			drill.Timeline.RestoreTargetDisposedAt = drill.Timeline.ServiceReadyAt.Add(time.Minute)
			drill.GeneratedAt = drill.Timeline.RestoreTargetDisposedAt.Add(time.Minute)
			drill.Ready = false
		},
	} {
		t.Run(name, func(t *testing.T) {
			paths, drill := readyFixture(t, now)
			mutate(&drill)
			receipt, err := Collect(paths.inventory, paths.plan, paths.change, writeDrill(t, drill), now.Add(2*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if Assess(receipt).Ready {
				t.Fatal("failed or out-of-target drill was ready")
			}
		})
	}
}

func TestCollectRejectsUnsafeMalformedContradictoryAndMismatchedEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*Drill){
		"local classification":      func(drill *Drill) { drill.Classification = "local_mock" },
		"inventory digest mismatch": func(drill *Drill) { drill.InventoryReceiptSHA256 = strings.Repeat("a", 64) },
		"change digest mismatch":    func(drill *Drill) { drill.ChangeReceiptSHA256 = strings.Repeat("b", 64) },
		"contradictory readiness":   func(drill *Drill) { drill.Ready = false },
		"missing check":             func(drill *Drill) { drill.Checks = drill.Checks[:9] },
		"duplicate check":           func(drill *Drill) { drill.Checks[9].ID = drill.Checks[0].ID },
		"unknown check":             func(drill *Drill) { drill.Checks[0].ID = "database_content" },
		"missing evidence hash":     func(drill *Drill) { drill.Checks[0].EvidenceSHA256 = "" },
		"recovery after impairment": func(drill *Drill) {
			drill.Timeline.RecoveryPointAt = drill.Timeline.ImpairmentStartedAt.Add(time.Second)
		},
		"restore before impairment": func(drill *Drill) {
			drill.Timeline.RestoreStartedAt = drill.Timeline.ImpairmentStartedAt.Add(-time.Second)
		},
		"reconcile before restore": func(drill *Drill) {
			drill.Timeline.ReconciliationCompletedAt = drill.Timeline.RestoreCompletedAt.Add(-time.Second)
		},
		"not disposed": func(drill *Drill) {
			drill.Timeline.RestoreTargetDisposedAt = drill.Timeline.ServiceReadyAt.Add(-time.Second)
		},
		"future evidence": func(drill *Drill) { drill.GeneratedAt = now.Add(time.Second) },
		"stale evidence":  func(drill *Drill) { drill.GeneratedAt = now.Add(-25 * time.Hour) },
	} {
		t.Run(name, func(t *testing.T) {
			paths, drill := readyFixture(t, now)
			mutate(&drill)
			if _, err := Collect(paths.inventory, paths.plan, paths.change, writeDrill(t, drill), now); err == nil {
				t.Fatal("unsafe or contradictory restore evidence was accepted")
			}
		})
	}

	paths, drill := readyFixture(t, now)
	validPath := writeDrill(t, drill)
	link := filepath.Join(t.TempDir(), "restore.json")
	if err := os.Symlink(validPath, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(paths.inventory, paths.plan, paths.change, link, now); err == nil {
		t.Fatal("symlink drill was accepted")
	}

	contents, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(string(contents), `"schema":`, `"database_name":"private","schema":`, 1)
	unknownPath := filepath.Join(t.TempDir(), "unknown.json")
	if err := os.WriteFile(unknownPath, []byte(unknown), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Collect(paths.inventory, paths.plan, paths.change, unknownPath, now); err == nil {
		t.Fatal("unknown content-bearing field was accepted")
	}
}

func TestPublishIsCreateOnlyAndMode0600(t *testing.T) {
	now := time.Date(2026, 8, 10, 6, 0, 0, 0, time.UTC)
	paths, drill := readyFixture(t, now)
	receipt, err := Collect(paths.inventory, paths.plan, paths.change, writeDrill(t, drill), now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipt.json")
	if err := Publish(path, receipt); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%#o", info.Mode().Perm())
	}
	if err := Publish(path, receipt); err == nil {
		t.Fatal("existing receipt was overwritten")
	}
}

type fixturePaths struct{ inventory, plan, change string }

func readyFixture(t *testing.T, now time.Time) (fixturePaths, Drill) {
	t.Helper()
	root := repositoryRoot(t)
	paths := fixturePaths{
		inventory: filepath.Join(root, "docs", "saas", "self-managed-platform-inventory.example.json"),
		plan:      filepath.Join(root, "docs", "saas", "self-managed-infrastructure-plan.example.json"),
		change:    filepath.Join(root, "docs", "saas", "self-managed-infrastructure-change.example.json"),
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
	checks := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, EvidenceSHA256: strings.Repeat("e", 64)})
	}
	return paths, Drill{
		Schema: DrillSchemaV1, Classification: "self_managed_external", Environment: string(inventory.Environment),
		DrillID: "restore-drill-20260810", InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256,
		ChangeID: change.ChangeID, ChangeReceiptSHA256: change.ReceiptSHA256, Ready: true, GeneratedAt: now.Add(-time.Minute),
		Backup:   Backup{ID: "backup-20260810-0430", CreatedAt: impairment.Add(-30 * time.Minute), VerifiedAt: impairment.Add(-25 * time.Minute), ManifestSHA256: strings.Repeat("c", 64), VerificationOutputSHA256: strings.Repeat("d", 64)},
		Timeline: Timeline{ImpairmentStartedAt: impairment, RecoveryPointAt: impairment.Add(-120 * time.Second), RestoreStartedAt: impairment.Add(5 * time.Minute), RestoreCompletedAt: impairment.Add(20 * time.Minute), ReconciliationCompletedAt: impairment.Add(30 * time.Minute), ServiceReadyAt: impairment.Add(40 * time.Minute), RestoreTargetDisposedAt: impairment.Add(45 * time.Minute)},
		Checks:   checks,
	}
}

func writeDrill(t *testing.T, drill Drill) string {
	t.Helper()
	contents, err := json.Marshal(drill)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "restore.json")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
