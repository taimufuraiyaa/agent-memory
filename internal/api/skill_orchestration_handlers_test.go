package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillOrchestrationStatusPauseResumeRetryCancelReconcileReplayAndDrain(t *testing.T) {
	authorizer := &skillMutationTestAuthorizer{}
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir(), DBPath: filepath.Join(t.TempDir(), "orchestration.db"), SkillMutationAuthorizer: authorizer}
	assets, err := svc.resolve(context.Background(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	scope := core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "local"}
	now := time.Now().UTC().Add(-time.Minute)
	digest := "sha256:" + strings.Repeat("a", 64)
	workflow := core.SkillWorkflow{ID: "workflow-control", Scope: scope, SkillID: "skill-control", OriginKind: core.SkillWorkflowOriginToolLesson, OriginID: "lesson-control",
		Kind: core.SkillWorkflowAutomaticRevision, ContractVersion: core.SkillOrchestratorContractVersion, InputDigest: digest,
		State: core.SkillWorkflowOpen, CurrentStage: core.SkillStageDetect, Generation: 1, ConfigurationVersion: 1,
		PolicyDigest: digest, CreatedAt: now, UpdatedAt: now}
	if _, _, err := assets.Store.CreateSkillWorkflow(context.Background(), workflow); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewMux(svc))
	t.Cleanup(func() { server.Close(); _ = svc.Close() })

	statusURL := server.URL + "/api/v1/skills/orchestration/status?workspace=ws&environment=local&actor=actor&workflow_id=" + url.QueryEscape(workflow.ID) + "&limit=1"
	if response, body := getSkillOrchestrationResponse(t, statusURL); response != http.StatusOK || body["workflow"].(map[string]any)["id"] != workflow.ID {
		t.Fatalf("status=%d body=%#v", response, body)
	}
	statusBySkillURL := server.URL + "/api/v1/skills/orchestration/status?workspace=ws&environment=local&actor=actor&skill_id=" + workflow.SkillID + "&limit=1"
	if response, body := getSkillOrchestrationResponse(t, statusBySkillURL); response != http.StatusOK || body["workflow"].(map[string]any)["id"] != workflow.ID {
		t.Fatalf("skill status=%d body=%#v", response, body)
	}
	paused := postSkillOrchestration(t, server.URL, map[string]any{"action": "pause", "workspace": "ws", "environment": "local", "actor": "actor", "workflow_id": workflow.ID, "expected_generation": 1})
	if paused.status != http.StatusOK || paused.body["result"].(map[string]any)["generation"] != float64(2) {
		t.Fatalf("pause=%+v", paused)
	}
	stale := postSkillOrchestration(t, server.URL, map[string]any{"action": "resume", "workspace": "ws", "environment": "local", "actor": "actor", "workflow_id": workflow.ID, "expected_generation": 1})
	if stale.status != http.StatusConflict {
		t.Fatalf("stale resume status=%d", stale.status)
	}
	resumed := postSkillOrchestration(t, server.URL, map[string]any{"action": "resume", "workspace": "ws", "environment": "local", "actor": "actor", "workflow_id": workflow.ID, "expected_generation": 2})
	if resumed.status != http.StatusOK || resumed.body["result"].(map[string]any)["generation"] != float64(3) {
		t.Fatalf("resume=%+v", resumed)
	}

	retryJob := orchestrationTestJob(now, scope, workflow.ID, "job-retry", digest)
	if _, _, err := assets.Store.EnqueueSkillJob(context.Background(), retryJob, nil); err != nil {
		t.Fatal(err)
	}
	claimed, err := assets.Store.ClaimSkillJobs(context.Background(), scope, "worker", 1, time.Minute, 30*time.Second, time.Now().UTC())
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := assets.Store.BlockSkillJob(context.Background(), contracts.SkillJobBlock{Scope: scope, JobID: retryJob.ID, Owner: "worker", Fence: 1, ExpectedWorkflowGeneration: 3, FailureClass: core.SkillFailurePolicyBlock, ReasonCode: "waiting_policy", RecheckAt: time.Now().UTC().Add(time.Hour), Now: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	retried := postSkillOrchestration(t, server.URL, map[string]any{"action": "retry", "workspace": "ws", "environment": "local", "actor": "actor", "job_id": retryJob.ID, "expected_generation": 3})
	if retried.status != http.StatusOK || retried.body["result"].(map[string]any)["state"] != "queued" {
		t.Fatalf("retry=%+v", retried)
	}
	claimed, err = assets.Store.ClaimSkillJobs(context.Background(), scope, "worker-replay", 1, time.Minute, 30*time.Second, time.Now().UTC().Add(time.Second))
	if err != nil || len(claimed) != 1 || claimed[0].ID != retryJob.ID {
		t.Fatalf("replay claim=%+v err=%v", claimed, err)
	}
	if err := assets.Store.FinalizeSkillJob(context.Background(), contracts.SkillJobFinalization{Scope: scope, JobID: retryJob.ID, Owner: claimed[0].LeaseOwner, Fence: claimed[0].Fence, ExpectedWorkflowGeneration: 3, ResultKind: core.SkillJobResultRejected, FailureClass: core.SkillFailurePermanentValidation, FailureCode: "operator_fixture", DeadLetter: true, Now: time.Now().UTC().Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	replayed := postSkillOrchestration(t, server.URL, map[string]any{"action": "replay", "workspace": "ws", "environment": "local", "actor": "actor", "job_id": retryJob.ID, "reason_code": "operator_replay", "idempotency_key": "replay-key"})
	if replayed.status != http.StatusOK || replayed.body["result"].(map[string]any)["created"] != true {
		t.Fatalf("replay=%+v", replayed)
	}
	replayedAgain := postSkillOrchestration(t, server.URL, map[string]any{"action": "replay", "workspace": "ws", "environment": "local", "actor": "actor", "job_id": retryJob.ID, "reason_code": "operator_replay", "idempotency_key": "replay-key"})
	if replayedAgain.status != http.StatusOK || replayedAgain.body["result"].(map[string]any)["created"] != false {
		t.Fatalf("replay idempotency=%+v", replayedAgain)
	}

	cancelJob := orchestrationTestJob(time.Now().UTC(), scope, workflow.ID, "job-cancel", digest)
	cancelJob.Stage = core.SkillStageBuild
	if _, _, err := assets.Store.EnqueueSkillJob(context.Background(), cancelJob, nil); err != nil {
		t.Fatal(err)
	}
	cancelled := postSkillOrchestration(t, server.URL, map[string]any{"action": "cancel", "workspace": "ws", "environment": "local", "actor": "actor", "job_id": cancelJob.ID, "expected_generation": 3})
	if cancelled.status != http.StatusOK {
		t.Fatalf("cancel=%+v", cancelled)
	}

	reconcile := postSkillOrchestration(t, server.URL, map[string]any{"action": "reconcile", "workspace": "ws", "environment": "local", "actor": "actor", "workflow_id": workflow.ID, "expected_generation": 3, "limit": 10})
	if reconcile.status != http.StatusOK {
		t.Fatalf("reconcile=%+v", reconcile)
	}
	svc.SkillOrchestrationDrainer = orchestrationDrainerFunc(func(context.Context) error { return context.DeadlineExceeded })
	timedOutDrain := postSkillOrchestration(t, server.URL, map[string]any{"action": "drain", "workspace": "ws", "environment": "local", "actor": "actor"})
	if timedOutDrain.status != http.StatusGatewayTimeout {
		t.Fatalf("timed out drain=%+v", timedOutDrain)
	}
	svc.SkillOrchestrationDrainer = nil
	drain := postSkillOrchestration(t, server.URL, map[string]any{"action": "drain", "workspace": "ws", "environment": "local", "actor": "actor"})
	if drain.status != http.StatusOK || drain.body["result"].(map[string]any)["drained"] != true {
		t.Fatalf("drain=%+v", drain)
	}

	runningWorkflow := workflow
	runningWorkflow.ID, runningWorkflow.OriginID, runningWorkflow.Generation = "workflow-running", "lesson-running", 1
	runningWorkflow.CreatedAt, runningWorkflow.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	if _, _, err := assets.Store.CreateSkillWorkflow(context.Background(), runningWorkflow); err != nil {
		t.Fatal(err)
	}
	runningJob := orchestrationTestJob(time.Now().UTC(), scope, runningWorkflow.ID, "job-running", digest)
	runningJob.Priority = 1_000_000
	if _, _, err := assets.Store.EnqueueSkillJob(context.Background(), runningJob, nil); err != nil {
		t.Fatal(err)
	}
	runningClaim, err := assets.Store.ClaimSkillJobs(context.Background(), scope, "worker-running", 1, time.Minute, 30*time.Second, time.Now().UTC().Add(time.Second))
	if err != nil || len(runningClaim) != 1 || runningClaim[0].ID != runningJob.ID {
		t.Fatalf("running claim=%+v err=%v", runningClaim, err)
	}
	runningCancel := postSkillOrchestration(t, server.URL, map[string]any{"action": "cancel", "workspace": "ws", "environment": "local", "actor": "actor", "job_id": runningJob.ID, "expected_generation": 1})
	storedRunning, loadErr := assets.Store.GetSkillJob(context.Background(), scope, runningJob.ID)
	if runningCancel.status != http.StatusOK || loadErr != nil || storedRunning.CancelRequestedAt.IsZero() || storedRunning.State != core.SkillJobRunning {
		t.Fatalf("running cancel=%+v job=%+v err=%v", runningCancel, storedRunning, loadErr)
	}

	if response, body := getSkillOrchestrationResponse(t, statusURL); response != http.StatusOK || body["next_event_cursor"] == nil || body["next_job_cursor"] == nil {
		t.Fatalf("paginated status=%d body=%#v", response, body)
	} else if events := body["events"].([]any); len(events) == 0 || events[0].(map[string]any)["content"] != nil || events[0].(map[string]any)["payload"] != nil {
		t.Fatalf("history exposed content-bearing fields: %#v", body)
	}
	invalidRetry := postSkillOrchestration(t, server.URL, map[string]any{"action": "retry", "workspace": "ws", "environment": "local", "actor": "actor", "job_id": retryJob.ID, "expected_generation": 3})
	if invalidRetry.status != http.StatusConflict {
		t.Fatalf("dead-letter retry status=%d", invalidRetry.status)
	}
	unknownURL := server.URL + "/api/v1/skills/orchestration/status?workspace=unknown&environment=local&actor=actor&workflow_id=" + workflow.ID
	if response, _ := getSkillOrchestrationResponse(t, unknownURL); response >= 200 && response < 300 {
		t.Fatalf("unknown workspace status=%d", response)
	}

	authorizer.err = context.Canceled
	if response, _ := getSkillOrchestrationResponse(t, statusURL); response != http.StatusForbidden {
		t.Fatalf("unauthorized status=%d", response)
	}
}

type orchestrationDrainerFunc func(context.Context) error

func (f orchestrationDrainerFunc) Drain(ctx context.Context) error { return f(ctx) }

func orchestrationTestJob(now time.Time, scope core.SkillOrchestratorScope, workflowID, id, digest string) core.SkillJob {
	return core.SkillJob{ID: id, WorkflowID: workflowID, Scope: scope, Stage: core.SkillStageDetect, ContractVersion: core.SkillOrchestratorContractVersion,
		InputDigest: digest, PolicyVersion: 1, State: core.SkillJobQueued, Priority: 100, ReadyAt: now,
		MaxAttempts: 3, CreatedAt: now, UpdatedAt: now}
}

type orchestrationHTTPResult struct {
	status int
	body   map[string]any
}

func postSkillOrchestration(t *testing.T, base string, input any) orchestrationHTTPResult {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(base+"/api/v1/skills/orchestration/control", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(response.Body).Decode(&body)
	if data, ok := body["data"].(map[string]any); ok {
		body = data
	}
	return orchestrationHTTPResult{status: response.StatusCode, body: body}
}

func getSkillOrchestrationResponse(t *testing.T, target string) (int, map[string]any) {
	t.Helper()
	response, err := http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(response.Body).Decode(&body)
	if data, ok := body["data"].(map[string]any); ok {
		body = data
	}
	return response.StatusCode, body
}
