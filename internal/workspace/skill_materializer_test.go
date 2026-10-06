package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillMaterializerReplacesLegacyBundleWithAllAssets(t *testing.T) {
	projectRoot := t.TempDir()
	allowBundleCleanup(t, projectRoot)
	active := filepath.Join(projectRoot, ".agents", "skills", "example")
	if err := os.MkdirAll(active, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "SKILL.md"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{"SKILL.md": []byte("new"), "references/guide.md": []byte("guide")}
	revision := testBundleRevision(t, contents)
	store, err := NewRevisionBundleStore(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(context.Background(), revision, contents); err != nil {
		t.Fatal(err)
	}
	materializer, err := NewSkillMaterializer(projectRoot, store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := materializer.Materialize(context.Background(), testMaterializationRequest(revision))
	if err != nil {
		t.Fatal(err)
	}
	if result.Digest != revision.BundleDigest || result.Recovered {
		t.Fatalf("result = %+v", result)
	}
	for path, want := range contents {
		got, err := os.ReadFile(filepath.Join(active, filepath.FromSlash(path)))
		if err != nil || string(got) != string(want) {
			t.Fatalf("active %s = %q, err = %v", path, got, err)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(active))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "example" {
		t.Fatalf("materialization leftovers = %v", entries)
	}
}

func TestSkillMaterializerPreservesPriorBundleWhenSwapFails(t *testing.T) {
	projectRoot := t.TempDir()
	allowBundleCleanup(t, projectRoot)
	active := filepath.Join(projectRoot, ".agents", "skills", "example")
	if err := os.MkdirAll(active, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "SKILL.md"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{"SKILL.md": []byte("new")}
	revision := testBundleRevision(t, contents)
	store, err := NewRevisionBundleStore(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(context.Background(), revision, contents); err != nil {
		t.Fatal(err)
	}
	materializer, err := NewSkillMaterializer(projectRoot, store)
	if err != nil {
		t.Fatal(err)
	}
	materializer.afterPriorMoved = func() error { return errors.New("simulated swap failure") }
	if _, err := materializer.Materialize(context.Background(), testMaterializationRequest(revision)); err == nil {
		t.Fatal("materialization unexpectedly succeeded")
	}
	got, err := os.ReadFile(filepath.Join(active, "SKILL.md"))
	if err != nil || string(got) != "old" {
		t.Fatalf("prior active bundle was not restored: %q, %v", got, err)
	}
}

func TestSkillMaterializerRejectsUnverifiedSourceBeforeTouchingActive(t *testing.T) {
	projectRoot := t.TempDir()
	active := filepath.Join(projectRoot, ".agents", "skills", "example")
	if err := os.MkdirAll(active, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "SKILL.md"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{"SKILL.md": []byte("new"), "references/missing.md": []byte("missing")}
	revision := testBundleRevision(t, contents)
	store, err := NewRevisionBundleStore(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	materializer, err := NewSkillMaterializer(projectRoot, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := materializer.Materialize(context.Background(), testMaterializationRequest(revision)); err == nil {
		t.Fatal("missing immutable source was accepted")
	}
	got, err := os.ReadFile(filepath.Join(active, "SKILL.md"))
	if err != nil || string(got) != "old" {
		t.Fatal("active bundle changed after source verification failure")
	}
}

func TestSkillMaterializerRecoversInterruptedSwap(t *testing.T) {
	projectRoot := t.TempDir()
	allowBundleCleanup(t, projectRoot)
	skillsRoot := filepath.Join(projectRoot, ".agents", "skills")
	backup := filepath.Join(skillsRoot, ".example.backup-operation-1")
	if err := os.MkdirAll(backup, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "SKILL.md"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{"SKILL.md": []byte("new")}
	revision := testBundleRevision(t, contents)
	store, err := NewRevisionBundleStore(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(context.Background(), revision, contents); err != nil {
		t.Fatal(err)
	}
	materializer, err := NewSkillMaterializer(projectRoot, store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := materializer.Materialize(context.Background(), testMaterializationRequest(revision))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Recovered {
		t.Fatal("interrupted swap recovery was not reported")
	}
	got, err := os.ReadFile(filepath.Join(skillsRoot, "example", "SKILL.md"))
	if err != nil || string(got) != "new" {
		t.Fatalf("recovered active bundle = %q, %v", got, err)
	}
}

func TestSkillMaterializerFailsClosedOnReadOnlySkillsRoot(t *testing.T) {
	projectRoot := t.TempDir()
	skillsRoot := filepath.Join(projectRoot, ".agents", "skills")
	if err := os.Mkdir(filepath.Join(projectRoot, ".agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(skillsRoot, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(skillsRoot, 0o700) })
	contents := map[string][]byte{"SKILL.md": []byte("new")}
	revision := testBundleRevision(t, contents)
	store, err := NewRevisionBundleStore(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	allowBundleCleanup(t, projectRoot)
	if _, err := store.Publish(context.Background(), revision, contents); err != nil {
		t.Fatal(err)
	}
	materializer, err := NewSkillMaterializer(projectRoot, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := materializer.Materialize(context.Background(), testMaterializationRequest(revision)); err == nil {
		t.Fatal("read-only skills root was accepted")
	}
}

func testMaterializationRequest(revision core.SkillRevision) SkillMaterializationRequest {
	return SkillMaterializationRequest{
		OperationID: "operation-1",
		Skill: core.LogicalSkill{
			ID: revision.SkillID, Workspace: revision.Workspace, Name: "example", Description: "Example",
			RiskTier: core.SkillRiskMedium, OwnerGroup: "test", Status: core.SkillStatusActive,
			Generation: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		},
		Revision: revision,
	}
}
