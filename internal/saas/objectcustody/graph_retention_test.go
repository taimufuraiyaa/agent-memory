package objectcustody

import (
	"reflect"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphBackupRestoreObjectRetentionIsBoundedAndTenantScoped(t *testing.T) {
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	plan := application.PlanGraphRetention(now, []application.GraphRetainedArtifact{
		{ID: "projection-expired", Class: application.GraphArtifactProjection, RetentionStartedAt: now.Add(-24 * time.Hour)},
		{ID: "cache-expired", Class: application.GraphArtifactCache, RetentionStartedAt: now.Add(-7 * 24 * time.Hour)},
		{ID: "native-expired", Class: application.GraphArtifactNative, RetentionStartedAt: now.Add(-30 * 24 * time.Hour)},
		{ID: "legal-hold", Class: application.GraphArtifactNative, RetentionStartedAt: now.Add(-60 * 24 * time.Hour), Hold: true},
	})
	if !reflect.DeepEqual(plan.DeleteIDs, []string{"cache-expired", "native-expired", "projection-expired"}) || !reflect.DeepEqual(plan.HeldIDs, []string{"legal-hold"}) {
		t.Fatalf("unexpected graph retention plan: %+v", plan)
	}
	a, _ := GraphArtifactStagingPrefix(core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, "job-a", "revision-a")
	b, _ := GraphArtifactStagingPrefix(core.GraphScope{TenantID: "tenant-b", WorkspaceID: "workspace-a"}, "job-a", "revision-a")
	if a == b || a == "" || b == "" {
		t.Fatalf("artifact retention prefixes are not tenant-scoped: %q %q", a, b)
	}
}
