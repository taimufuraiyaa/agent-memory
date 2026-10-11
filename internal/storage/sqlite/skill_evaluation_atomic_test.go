package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestCreateSkillEvaluationRunsRollsBackWholePair(t *testing.T) {
	store := openSkillOrchestratorStore(t)
	now := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	seedSQLiteEvaluationForeignKeys(t, store, now)
	baseline := sqliteEvaluationRun("baseline", "revision-1", now)
	if err := store.CreateSkillEvaluationRun(context.Background(), baseline); err != nil {
		t.Fatal(err)
	}
	candidate := sqliteEvaluationRun("candidate", "revision-2", now)
	candidate.BaselineRevisionID = baseline.RevisionID
	candidate.BaselineDigest = baseline.RevisionDigest
	if err := store.CreateSkillEvaluationRuns(context.Background(), candidate, baseline); err == nil {
		t.Fatal("duplicate baseline unexpectedly committed evaluation pair")
	}
	if _, err := store.GetSkillEvaluationRun(context.Background(), "ws", candidate.ID); err == nil {
		t.Fatal("candidate half of failed pair was persisted")
	}
}

func seedSQLiteEvaluationForeignKeys(t *testing.T, store *Store, now time.Time) {
	t.Helper()
	timestamp := formatSkillTime(now)
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO skills(id,workspace,name,description,risk_tier,owner_group,status,generation,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, []any{"skill-1", "ws", "Skill", "Description", "low", "owner", "active", 1, timestamp, timestamp}},
		{`INSERT INTO skill_revisions(id,workspace,skill_id,revision_number,state,bundle_digest,manifest_version,risk_tier,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, []any{"revision-1", "ws", "skill-1", 1, "active", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1, "low", "test", timestamp}},
		{`INSERT INTO skill_revisions(id,workspace,skill_id,revision_number,state,bundle_digest,manifest_version,risk_tier,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, []any{"revision-2", "ws", "skill-1", 2, "testing", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", 1, "low", "test", timestamp}},
		{`INSERT INTO skill_evaluation_suites(id,workspace,skill_id,version,digest,created_by,created_at) VALUES(?,?,?,?,?,?,?)`, []any{"suite-1", "ws", "skill-1", 1, "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "test", timestamp}},
	}
	for _, statement := range statements {
		if _, err := store.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func sqliteEvaluationRun(id, revisionID string, now time.Time) core.SkillEvaluationRun {
	return core.SkillEvaluationRun{ID: id, Workspace: "ws", SkillID: "skill-1", RevisionID: revisionID,
		RevisionDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SuiteID:        "suite-1", SuiteVersion: 1, SuiteDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Evaluator: "runner", EvaluatorVersion: "v1", EnvironmentFingerprint: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Verdict: core.SkillEvaluationPass, CaseResults: []core.SkillEvaluationCaseResult{{CaseID: "case-1", Passed: true, IndependentlyVerified: true}}, StartedAt: now, CompletedAt: now}
}
