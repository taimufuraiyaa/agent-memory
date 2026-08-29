package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillActivationOperationLedgerIsIdempotentAndTransitionChecked(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "activation-operation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	insertSkillOperationFixture(t, store, now)
	operation := core.SkillActivationOperation{
		ID: "operation-1", Workspace: "ws", Environment: "local", SkillID: "skill-1",
		FromRevisionID: "revision-1", ToRevisionID: "revision-2", ExpectedGeneration: 1,
		State: core.SkillActivationOperationReserved, IdempotencyKey: "promote-revision-2",
		CreatedAt: now, UpdatedAt: now,
	}
	duplicate, err := store.CreateSkillActivationOperation(ctx, operation)
	if err != nil || duplicate {
		t.Fatalf("create = duplicate %v, err %v", duplicate, err)
	}
	duplicate, err = store.CreateSkillActivationOperation(ctx, operation)
	if err != nil || !duplicate {
		t.Fatalf("replay = duplicate %v, err %v", duplicate, err)
	}
	updated, err := store.TransitionSkillActivationOperation(ctx, "ws", "operation-1", core.SkillActivationOperationReserved, core.SkillActivationOperationMaterializing, "", now.Add(time.Second))
	if err != nil || updated.State != core.SkillActivationOperationMaterializing {
		t.Fatalf("transition = %+v, err %v", updated, err)
	}
	if _, err := store.TransitionSkillActivationOperation(ctx, "ws", "operation-1", core.SkillActivationOperationReserved, core.SkillActivationOperationCompleted, "", now.Add(2*time.Second)); err == nil {
		t.Fatal("stale or illegal transition was accepted")
	}
	failed, err := store.TransitionSkillActivationOperation(ctx, "ws", "operation-1", core.SkillActivationOperationMaterializing, core.SkillActivationOperationFailed, "disk unavailable", now.Add(3*time.Second))
	if err != nil || failed.Error != "disk unavailable" {
		t.Fatalf("failed transition = %+v, err %v", failed, err)
	}
}

func TestSkillRevisionStateTransitionIsOptimistic(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "revision-transition.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 29, 15, 30, 0, 0, time.UTC)
	insertSkillOperationFixture(t, store, now)
	if _, err := store.db.Exec(`UPDATE skill_revisions SET state='draft' WHERE id='revision-2'`); err != nil {
		t.Fatal(err)
	}
	for _, transition := range []struct {
		from core.SkillRevisionState
		to   core.SkillRevisionState
	}{{core.SkillRevisionDraft, core.SkillRevisionTesting}, {core.SkillRevisionTesting, core.SkillRevisionCanary}} {
		if _, err := store.TransitionSkillRevisionState(ctx, "ws", "revision-2", transition.from, transition.to); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.TransitionSkillRevisionState(ctx, "ws", "revision-2", core.SkillRevisionTesting, core.SkillRevisionActive); err == nil {
		t.Fatal("stale or illegal revision transition was accepted")
	}
}

func insertSkillOperationFixture(t *testing.T, store *Store, now time.Time) {
	t.Helper()
	digestOne := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestTwo := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	formatted := now.Format(time.RFC3339Nano)
	if _, err := store.db.Exec(`INSERT INTO skills(id,workspace,name,description,risk_tier,owner_group,status,generation,created_at,updated_at) VALUES('skill-1','ws','example','desc','low','platform','active',1,?,?)`, formatted, formatted); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []struct{ id, digest string }{{"revision-1", digestOne}, {"revision-2", digestTwo}} {
		if _, err := store.db.Exec(`INSERT INTO skill_revisions(id,workspace,skill_id,revision_number,state,bundle_digest,manifest_version,compatibility_json,risk_tier,candidate_id,created_by,created_at) VALUES(?,?,?,?,?, ?,1,'{}','low','','agent',?)`, revision.id, "ws", "skill-1", map[string]int{"revision-1": 1, "revision-2": 2}[revision.id], map[string]string{"revision-1": "active", "revision-2": "canary"}[revision.id], revision.digest, formatted); err != nil {
			t.Fatal(err)
		}
	}
}
