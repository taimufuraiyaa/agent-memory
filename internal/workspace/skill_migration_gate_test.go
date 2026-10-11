package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestSkillMigrationReleaseGateFreshAndUpgradedDatabaseMatrix(t *testing.T) {
	for _, mode := range []string{"fresh", "upgraded"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			allowBundleCleanup(t, root)
			seedMigrationSkill(t, root, "safe-release")
			database := filepath.Join(t.TempDir(), "memory.db")
			if mode == "upgraded" {
				old, err := sqlite.Open(ctx, database)
				if err != nil {
					t.Fatal(err)
				}
				old.Close()
			}
			store, err := sqlite.Open(ctx, database)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			skillID := deterministicSkillImportID("skill", "ws", "safe-release")
			report, err := RunSkillMigrationReleaseGate(ctx, store, "ws", root, []SkillShadowSelection{{TaskID: "release-task", LegacySkillName: "safe-release", LifecycleSkillID: skillID}}, func() time.Time { return time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC) })
			if err != nil || !report.Ready || report.VerifiedSkills != 1 || report.ShadowComparisons != 1 || report.RollbackReadySkills != 1 {
				t.Fatalf("%s gate = %+v, %v", mode, report, err)
			}
		})
	}
}

func TestSkillMigrationReleaseGateBlocksShadowDigestAndMaterializationDrift(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	allowBundleCleanup(t, root)
	seedMigrationSkill(t, root, "safe-release")
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	skillID := deterministicSkillImportID("skill", "ws", "safe-release")
	wrong, err := RunSkillMigrationReleaseGate(ctx, store, "ws", root, []SkillShadowSelection{{TaskID: "task", LegacySkillName: "different", LifecycleSkillID: skillID}}, time.Now)
	if err != nil || wrong.Ready || len(wrong.Discrepancies) == 0 {
		t.Fatalf("shadow mismatch did not block: %+v, %v", wrong, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".agents", "skills", "safe-release", "SKILL.md"), []byte("---\nname: safe-release\ndescription: tampered\n---\n# Changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tampered, err := RunSkillMigrationReleaseGate(ctx, store, "ws", root, []SkillShadowSelection{{TaskID: "task", LegacySkillName: "safe-release", LifecycleSkillID: skillID}}, time.Now)
	if err != nil || tampered.Ready {
		t.Fatalf("digest drift did not block: %+v, %v", tampered, err)
	}
	found := false
	for _, discrepancy := range tampered.Discrepancies {
		if discrepancy.Kind == "digest" || discrepancy.Kind == "import" || discrepancy.Kind == "materialization" {
			found = true
		}
	}
	if !found {
		t.Fatalf("digest discrepancy missing: %+v", tampered)
	}
}

func seedMigrationSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, ".agents", "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: Representative migration fixture\n---\n# " + strings.ReplaceAll(name, "-", " ") + "\n\nRun verified steps.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
