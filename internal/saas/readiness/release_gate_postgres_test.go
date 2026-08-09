package readiness

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func TestEvaluateReleaseRequiresMetricsApprovalsAndRecentDrills(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_POSTGRES_URL"))
	if url == "" {
		t.Skip("AGENT_MEMORY_TEST_POSTGRES_URL is not configured")
	}
	ctx := context.Background()
	pool, err := saaspostgres.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := saaspostgres.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE saas_release_evidence,saas_game_day_drills`); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	window := 28 * 24 * time.Hour
	service := NewService(pool, func() time.Time { return now })
	for _, metric := range GateMetrics["private_beta"] {
		if err := service.RecordEvidence(ctx, Evidence{Gate: "private_beta", Metric: metric, Value: 1, Threshold: 1, Comparator: "gte", Owner: "test-owner", SourceRef: "report:test", ObservedAt: now, WindowStart: now.Add(-window), WindowEnd: now}); err != nil {
			t.Fatal(err)
		}
	}
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	controls := GateApprovals["private_beta"]
	bundle := TrustBundle{Schema: ApprovalTrustSchema, Keys: []TrustedApprover{{KeyID: "release-2026", Owner: "release-review", PublicKey: base64.StdEncoding.EncodeToString(public), Gates: []string{"private_beta"}, Controls: controls}}}
	approvals := make([]SignedApproval, 0, len(controls))
	for index, control := range controls {
		approval := approvalFixture(now.Add(time.Duration(index)*time.Second), control, "approved")
		approval.Owner, approval.KeyID = "release-review", "release-2026"
		approval.ExpiresAt = now.Add(7 * 24 * time.Hour).Format(time.RFC3339Nano)
		approvals = append(approvals, signApproval(t, private, approval))
	}

	report, err := service.EvaluateRelease(ctx, "private_beta", window, bundle, approvals, now)
	if err != nil || report.Ready || len(report.MissingGameDays) != len(GateGameDays["private_beta"]) {
		t.Fatalf("release without drills=%+v err=%v", report, err)
	}
	for _, scenario := range GateGameDays["private_beta"] {
		if _, err := pool.Exec(ctx, `INSERT INTO saas_game_day_drills(id,scenario,owner,outcome,started_at,completed_at,evidence_sha256,safe_summary) VALUES($1,$2,'test-oncall','passed',$3,$4,$5,'{}')`, uuid.NewString(), scenario, now.Add(-time.Hour), now.Add(-30*time.Minute), strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
	}
	report, err = service.EvaluateRelease(ctx, "private_beta", window, bundle, approvals, now)
	if err != nil || !report.Ready || !report.Metrics.Ready || !report.Approvals.Ready || len(report.MissingGameDays) != 0 {
		t.Fatalf("complete release report=%+v err=%v", report, err)
	}
	stale, err := service.EvaluateRelease(ctx, "private_beta", window, bundle, approvals, now.Add(48*time.Hour))
	if err != nil || stale.Ready || stale.MetricsCurrent {
		t.Fatalf("stale metric window must block release: report=%+v err=%v", stale, err)
	}
}
