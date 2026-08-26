package backup

import (
	"bytes"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphBackupRestoreReplaysDeletionAndRebuildsWithoutNativeArtifacts(t *testing.T) {
	backupAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	now := backupAt.Add(48 * time.Hour)
	scope := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}
	request := application.GraphProjectionRequest{
		Scope: scope, ConfigurationID: "configuration-a", JobID: "job-restore", RevisionID: "revision-restore",
		Mode: contracts.GraphIndexModeFull, ProjectionPolicyVersion: "projection-v1",
		Cutoff:            core.GraphWatermark{Sequence: 20, EventTime: now, Digest: "sha256:restore"},
		PromptFingerprint: "sha256:prompt", ModelRoutes: []string{"index-model"}, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour), ProducerIdentity: "restore-controller",
		Records: []application.GraphProjectionRecord{
			{ID: "deleted-memory", Kind: application.GraphProjectionMemory, Content: "must never return", Fingerprint: core.FingerprintText("must never return"), EventTime: backupAt, Authorized: true, Exportable: true},
			{ID: "surviving-memory", Kind: application.GraphProjectionMemory, Content: "book a remains canonical", Fingerprint: core.FingerprintText("book a remains canonical"), EventTime: backupAt, Authorized: true, Exportable: true},
		},
	}
	plan, err := PlanGraphRestore(GraphRestoreRequest{Scope: scope, BackupCreatedAt: backupAt, Projection: request, Tombstones: []contracts.GraphDeletionTombstone{
		{Scope: scope, CanonicalKind: string(application.GraphProjectionMemory), CanonicalID: "deleted-memory", DeletedAt: backupAt.Add(time.Hour)},
		{Scope: core.GraphScope{TenantID: "tenant-b", WorkspaceID: "workspace-b"}, CanonicalKind: string(application.GraphProjectionMemory), CanonicalID: "surviving-memory", DeletedAt: backupAt.Add(time.Hour)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.RebuildRequired || plan.NativeArtifacts != 0 || len(plan.AppliedTombstones) != 1 {
		t.Fatalf("unsafe graph restore plan: %+v", plan)
	}
	if bytes.Contains(plan.Projection.DocumentsJSONL, []byte("must never return")) || !bytes.Contains(plan.Projection.DocumentsJSONL, []byte("book a remains canonical")) {
		t.Fatalf("restore resurrected deleted data or lost canonical data: %s", plan.Projection.DocumentsJSONL)
	}
}
