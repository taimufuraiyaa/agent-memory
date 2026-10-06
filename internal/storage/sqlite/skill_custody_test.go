package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillEvidenceDeletionIsScopedTombstonedAndIdempotent(t *testing.T) {
	ctx, store, now := skillCustodyFixture(t)
	seedSkillCustodyRevision(t, ctx, store, "ws-a", "a", "memory-1", now)
	seedSkillCustodyRevision(t, ctx, store, "ws-b", "b", "memory-1", now)
	result, err := store.DeleteSkillEvidence(ctx, "ws-a", "memory", "memory-1", now.Add(time.Hour))
	if err != nil || result.CandidateReferences != 1 || result.RevisionReferences != 1 {
		t.Fatalf("deletion = %+v, %v", result, err)
	}
	revisionA, _ := store.GetSkillRevision(ctx, "ws-a", "revision-a")
	revisionB, _ := store.GetSkillRevision(ctx, "ws-b", "revision-b")
	if len(revisionA.SourceMemoryIDs) != 0 || len(revisionB.SourceMemoryIDs) != 1 {
		t.Fatalf("workspace-scoped provenance a=%v b=%v", revisionA.SourceMemoryIDs, revisionB.SourceMemoryIDs)
	}
	if tombstoned, _ := store.IsSkillEvidenceTombstoned(ctx, "ws-a", "memory", "memory-1"); !tombstoned {
		t.Fatal("deletion tombstone is missing")
	}
	replayed, err := store.DeleteSkillEvidence(ctx, "ws-a", "memory", "memory-1", now.Add(2*time.Hour))
	if err != nil || !replayed.Replayed {
		t.Fatalf("deletion replay = %+v, %v", replayed, err)
	}
	candidate := core.SkillCandidate{ID: "candidate-new", Workspace: "ws-a", Kind: core.SkillCandidateCreate, Summary: "reuse deleted evidence", ExpectedBenefit: "should be rejected", RiskTier: core.SkillRiskLow, Confidence: .9, State: core.SkillCandidateProposed, SourceMemoryIDs: []string{"memory-1"}, DeduplicationHash: "sha256:" + strings.Repeat("c", 64), CreatedBy: "agent", CreatedAt: now, UpdatedAt: now}
	if _, _, err := store.PutSkillCandidate(ctx, candidate); err == nil {
		t.Fatal("tombstoned evidence was resurrected by a candidate")
	}
}

func TestSkillEvidenceDeletionHonorsLegalHold(t *testing.T) {
	ctx, store, now := skillCustodyFixture(t)
	seedSkillCustodyRevision(t, ctx, store, "ws-a", "a", "memory-held", now)
	hold := core.SkillLegalHold{ID: "hold-1", Workspace: "ws-a", TargetKind: "memory", TargetID: "memory-held", Reason: "active legal matter", CreatedAt: now}
	if err := store.PlaceSkillLegalHold(ctx, hold); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteSkillEvidence(ctx, "ws-a", "memory", "memory-held", now.Add(time.Hour)); !errors.Is(err, ErrSkillEvidenceHeld) {
		t.Fatalf("held deletion error = %v", err)
	}
	if err := store.ReleaseSkillLegalHold(ctx, "ws-a", hold.ID, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteSkillEvidence(ctx, "ws-a", "memory", "memory-held", now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestSkillTelemetryRetentionKeepsHeldExecutions(t *testing.T) {
	ctx, store, now := skillCustodyFixture(t)
	seedSkillCustodyRevision(t, ctx, store, "ws-a", "a", "memory-1", now)
	seedSkillExecutionRows(t, ctx, store, now)
	if err := store.PlaceSkillLegalHold(ctx, core.SkillLegalHold{ID: "hold-execution", Workspace: "ws-a", TargetKind: "execution", TargetID: "execution-held", Reason: "investigation", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.PruneSkillTelemetry(ctx, "ws-a", now.Add(time.Hour))
	if err != nil || deleted != 1 {
		t.Fatalf("pruned=%d err=%v", deleted, err)
	}
	var ids []string
	rows, _ := store.db.QueryContext(ctx, `SELECT id FROM skill_executions WHERE workspace='ws-a' ORDER BY id`)
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 1 || ids[0] != "execution-held" {
		t.Fatalf("retained executions = %v", ids)
	}
}

func TestSkillLifecycleExportContainsLineageTombstoneAndNoDeletedReference(t *testing.T) {
	ctx, store, now := skillCustodyFixture(t)
	seedSkillCustodyRevision(t, ctx, store, "ws-a", "a", "memory-deleted", now)
	if _, err := store.DeleteSkillEvidence(ctx, "ws-a", "memory", "memory-deleted", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	archive, err := store.ExportSkillLifecycle(ctx, "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(archive)
	if len(archive["revision_parents"]) != 0 || len(archive["revisions"]) != 1 || len(archive["evidence_tombstones"]) != 1 {
		t.Fatalf("archive counts are incomplete: %s", encoded)
	}
	if strings.Contains(string(encoded), `"memory_ids":["memory-deleted"]`) {
		t.Fatal("deleted evidence reference leaked into export")
	}
}

func skillCustodyFixture(t *testing.T) (context.Context, *Store, time.Time) {
	t.Helper()
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "custody.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return ctx, store, time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
}

func seedSkillCustodyRevision(t *testing.T, ctx context.Context, store *Store, workspace, suffix, memoryID string, now time.Time) {
	t.Helper()
	stamp := formatSkillTime(now)
	digest := "sha256:" + strings.Repeat(suffix, 64)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skills(id,workspace,name,description,risk_tier,owner_group,status,generation,created_at,updated_at) VALUES(?,?,?,?,?,'ops','active',1,?,?)`, "skill-"+suffix, workspace, "skill-"+suffix, "description", "low", stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_candidates(id,workspace,kind,summary,expected_benefit,risk_tier,confidence,state,target_skill_ids_json,deduplication_hash,created_by,created_at,updated_at) VALUES(?,?,'create','summary','benefit','low',0.9,'proposed','[]',?,'agent',?,?)`, "candidate-"+suffix, workspace, digest, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_candidate_sources(candidate_id,source_kind,source_id) VALUES(?,'memory',?)`, "candidate-"+suffix, memoryID); err != nil {
		t.Fatal(err)
	}
	provenance, _ := json.Marshal(map[string][]string{"memory_ids": []string{memoryID}})
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_revisions(id,workspace,skill_id,revision_number,state,bundle_digest,manifest_version,compatibility_json,risk_tier,candidate_id,provenance_json,created_by,created_at) VALUES(?,?,?,1,'active',?,1,'{}','low',?,?,'agent',?)`, "revision-"+suffix, workspace, "skill-"+suffix, digest, "candidate-"+suffix, string(provenance), stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_revision_files(revision_id,path,digest,size_bytes) VALUES(?,'SKILL.md',?,10)`, "revision-"+suffix, digest); err != nil {
		t.Fatal(err)
	}
}

func seedSkillExecutionRows(t *testing.T, ctx context.Context, store *Store, now time.Time) {
	t.Helper()
	stamp := formatSkillTime(now)
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_resolutions(id,workspace,environment,principal_id,task_id,skill_id,revision_id,revision_number,digest,reason,policy_version,acknowledgement_token_hash,expires_at,resolved_at) VALUES('resolution-a','ws-a','local','agent','episode','skill-a','revision-a',1,?,'active',1,'hash',?,?)`, digest, formatSkillTime(now.Add(time.Hour)), stamp); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"execution-held", "execution-expired"} {
		if _, err := store.db.ExecContext(ctx, `INSERT INTO skill_executions(id,workspace,environment,episode_id,skill_id,revision_id,revision_digest,resolution_id,acknowledged,acknowledged_at,outcome,independently_verified,started_at,completed_at) VALUES(?,'ws-a','local','episode','skill-a','revision-a',?,'resolution-a',1,?,'success',1,?,?)`, id, digest, stamp, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
}
