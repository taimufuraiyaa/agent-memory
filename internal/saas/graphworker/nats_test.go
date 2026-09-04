package graphworker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphNATSEnvelopesAreContentFreeScopedAndStrict(t *testing.T) {
	job := JobEnvelope{Scope: core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, JobID: "job-a", ConfigurationID: "configuration-a", RevisionID: "revision-a", ProjectionRevisionID: "revision-a", Mode: contracts.GraphIndexModeFull, Attempt: 1, CreatedAt: time.Now().UTC(), Limits: DefaultWorkspaceLimits()}
	if err := validateJob(job); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(job)
	if len(body) > 1024 || containsBytes(body, []byte("content")) || containsBytes(body, []byte("projection.jsonl")) {
		t.Fatalf("graph queue envelope carried content: %s", body)
	}
	var decoded JobEnvelope
	if err := strictGraphJSON(body, &decoded); err != nil || decoded.Scope != job.Scope {
		t.Fatal("strict graph queue envelope did not round-trip")
	}
	forged := append(body[:len(body)-1], []byte(",\"unknown\":true}")...)
	if err := strictGraphJSON(forged, &decoded); err == nil {
		t.Fatal("unknown graph queue envelope field was accepted")
	}
}

func TestGraphCompletionContractRejectsMixedSuccessAndFailure(t *testing.T) {
	event := CompletionEvent{ID: "event-a", Scope: core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}, JobID: "job-a", ConfigurationID: "configuration-a", RevisionID: "revision-a", Status: "completed", ArtifactPrefix: "graph-artifacts/staging/tenant-a/workspace-a/job-a/revision-a/"}
	if err := validateCompletion(event); err != nil {
		t.Fatal(err)
	}
	event.FailureCode = "adapter_failed"
	if err := validateCompletion(event); err == nil {
		t.Fatal("mixed graph completion state was accepted")
	}
}
