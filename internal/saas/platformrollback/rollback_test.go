package platformrollback

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadPairAndEvaluateRestoredStagingRollback(t *testing.T) {
	baselinePath := writeJSON(t, "baseline.json", validBaselineMap())
	attemptPath := writeJSON(t, "attempt.json", validAttemptMap())

	pair, err := LoadPair(baselinePath, attemptPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pair.BaselineReceiptSHA256) != 64 || len(pair.AttemptReceiptSHA256) != 64 || pair.BaselineReceiptSHA256 == pair.AttemptReceiptSHA256 {
		t.Fatalf("unexpected exact receipt digests: %+v", pair)
	}
	receipt, err := Evaluate(pair, validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	assessment := Assess(receipt)
	if !assessment.Ready || assessment.RestoredCount != 3 || assessment.DeploymentCount != 3 {
		t.Fatalf("unexpected assessment: %+v", assessment)
	}
}

func TestLoadPassedReleaseForEnvironmentAcceptsOnlyExactTarget(t *testing.T) {
	production := validBaselineMap()
	production["environment"] = "production"
	production["namespace"] = "agent-memory-production"
	production["kubernetes_context"] = "production-context"
	path := writeJSON(t, "production.json", production)
	receipt, digest, err := LoadPassedReleaseForEnvironment(path, "production")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Environment != "production" || receipt.Namespace != "agent-memory-production" || len(digest) != 64 {
		t.Fatalf("unexpected production receipt: %+v digest=%q", receipt, digest)
	}
	if _, _, err := LoadPassedReleaseForEnvironment(path, "staging"); err == nil {
		t.Fatal("production receipt accepted for staging")
	}
	if _, _, err := LoadPassedReleaseForEnvironment(path, "preview"); err == nil {
		t.Fatal("unsupported environment accepted")
	}
}

func TestEvaluatePreservesFixedUnreadyOutcomes(t *testing.T) {
	pair := validPair(t)
	tests := map[string]struct {
		mutate func(*Snapshot)
		want   Outcome
	}{
		"image mismatch": {func(value *Snapshot) {
			workload := value.Deployments[DeploymentAPI]
			workload.Image = attemptedImage("api")
			value.Deployments[DeploymentAPI] = workload
		}, OutcomeImageMismatch},
		"not ready": {func(value *Snapshot) {
			workload := value.Deployments[DeploymentWorker]
			workload.ReadyReplicas = 1
			value.Deployments[DeploymentWorker] = workload
		}, OutcomeNotReady},
		"unavailable": {func(value *Snapshot) {
			delete(value.Deployments, DeploymentReconciler)
		}, OutcomeUnavailable},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot := validSnapshot()
			test.mutate(&snapshot)
			receipt, err := Evaluate(pair, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.Ready || outcomeFor(receipt, test.want) != 1 {
				t.Fatalf("unexpected receipt: %+v", receipt)
			}
		})
	}
}

func TestLoadPairRejectsInvalidOrMeaninglessDrillEvidence(t *testing.T) {
	baseline := validBaselineMap()
	attempt := validAttemptMap()
	tests := map[string]func(map[string]any, map[string]any){
		"baseline not passed": func(base, _ map[string]any) { base["outcome"] = "failed" },
		"attempt not failed":  func(_, attempt map[string]any) { attempt["outcome"] = "passed" },
		"rollback not attempted": func(_, attempt map[string]any) {
			attempt["rollback"] = map[string]any{"attempted": false, "succeeded": false}
		},
		"production": func(base, attempt map[string]any) {
			base["environment"], attempt["environment"] = "production", "production"
			base["namespace"], attempt["namespace"] = "agent-memory-production", "agent-memory-production"
		},
		"context mismatch": func(_, attempt map[string]any) { attempt["kubernetes_context"] = "other-context" },
		"stale attempt":    func(_, attempt map[string]any) { attempt["started_at"] = "2026-08-10T00:30:00Z" },
		"no changed workload image": func(base, attempt map[string]any) {
			attemptImages := attempt["images"].(map[string]any)
			baselineImages := base["images"].(map[string]any)
			for _, name := range []string{"api", "worker", "reconciler"} {
				attemptImages[name] = baselineImages[name]
			}
		},
		"unknown field": func(_, attempt map[string]any) { attempt["reviewed"] = true },
		"missing deployment": func(_, attempt map[string]any) {
			attempt["deployments"] = attempt["deployments"].([]any)[:2]
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			baseCopy := cloneMap(baseline)
			attemptCopy := cloneMap(attempt)
			mutate(baseCopy, attemptCopy)
			if _, err := LoadPair(writeJSON(t, "baseline.json", baseCopy), writeJSON(t, "attempt.json", attemptCopy)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}

	target := writeJSON(t, "target.json", baseline)
	link := filepath.Join(t.TempDir(), "baseline-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPair(link, writeJSON(t, "attempt.json", attempt)); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestEvaluateRejectsUnsafeOrStaleLiveSnapshot(t *testing.T) {
	pair := validPair(t)
	for name, mutate := range map[string]func(*Snapshot){
		"context mismatch": func(value *Snapshot) { value.KubernetesContext = "other-context" },
		"stale collection": func(value *Snapshot) { value.CollectedAt = time.Date(2026, 8, 10, 1, 20, 0, 0, time.UTC) },
		"unsafe context":   func(value *Snapshot) { value.KubernetesContext = "context with spaces" },
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := validSnapshot()
			mutate(&snapshot)
			if _, err := Evaluate(pair, snapshot); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPublishIsPrivateCreateOnlyAndRejectsSymlink(t *testing.T) {
	receipt, err := Evaluate(validPair(t), validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rollback.json")
	if err := Publish(path, receipt); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o, want 600", info.Mode().Perm())
	}
	if err := Publish(path, receipt); err == nil {
		t.Fatal("existing receipt was overwritten")
	}
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Publish(link, receipt); err == nil {
		t.Fatal("symlink destination was accepted")
	}
}

func TestLoadReceiptStrictlyReloadsReadyRollback(t *testing.T) {
	receipt, err := Evaluate(validPair(t), validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	path := writeJSON(t, "rollback.json", receipt)
	loaded, digest, err := LoadReceipt(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Ready || len(digest) != 64 || Assess(loaded).RestoredCount != 3 {
		t.Fatalf("unexpected loaded rollback receipt: receipt=%+v digest=%q", loaded, digest)
	}
}

func TestLoadReceiptRejectsContradictoryIncompleteAndUnsafeFiles(t *testing.T) {
	receipt, err := Evaluate(validPair(t), validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var base map[string]any
	if err := json.Unmarshal(contents, &base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"contradictory ready": func(value map[string]any) {
			deployments := value["deployments"].([]any)
			deployments[0].(map[string]any)["outcome"] = "not_ready"
		},
		"missing deployment": func(value map[string]any) { value["deployments"] = value["deployments"].([]any)[:2] },
		"duplicate deployment": func(value map[string]any) {
			deployments := value["deployments"].([]any)
			deployments[1].(map[string]any)["name"] = "agent-memory-api"
		},
		"unknown field":  func(value map[string]any) { value["operator"] = "hidden" },
		"invalid digest": func(value map[string]any) { value["baseline_receipt_sha256"] = "invalid" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := cloneMap(base)
			mutate(copy)
			if _, _, err := LoadReceipt(writeJSON(t, "rollback.json", copy)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
	target := writeJSON(t, "target.json", receipt)
	link := filepath.Join(t.TempDir(), "rollback-link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadReceipt(link); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func validPair(t *testing.T) Pair {
	t.Helper()
	pair, err := LoadPair(writeJSON(t, "baseline.json", validBaselineMap()), writeJSON(t, "attempt.json", validAttemptMap()))
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

func validSnapshot() Snapshot {
	return Snapshot{
		KubernetesContext: "staging-context",
		CollectedAt:       time.Date(2026, 8, 10, 2, 0, 0, 0, time.UTC),
		Deployments: map[DeploymentName]LiveDeployment{
			DeploymentAPI:        {Image: baselineImage("api"), Revision: "8", DesiredReplicas: 2, ReadyReplicas: 2},
			DeploymentWorker:     {Image: baselineImage("worker"), Revision: "8", DesiredReplicas: 2, ReadyReplicas: 2},
			DeploymentReconciler: {Image: baselineImage("reconciler"), Revision: "8", DesiredReplicas: 1, ReadyReplicas: 1},
		},
	}
}

func validBaselineMap() map[string]any {
	return releaseMap("baseline-release", "2026-08-10T01:00:00Z", "2026-08-10T01:10:00Z", "passed", false, false, false)
}

func validAttemptMap() map[string]any {
	return releaseMap("failed-release", "2026-08-10T01:15:00Z", "2026-08-10T01:30:00Z", "failed", true, true, true)
}

func releaseMap(releaseID, startedAt, completedAt, outcome string, attempted, succeeded, changedImages bool) map[string]any {
	images := map[string]any{
		"api": baselineImage("api"), "worker": baselineImage("worker"),
		"reconciler": baselineImage("reconciler"), "migrate": baselineImage("migrate"),
	}
	if changedImages {
		images["api"], images["worker"], images["reconciler"] = attemptedImage("api"), attemptedImage("worker"), attemptedImage("reconciler")
	}
	migration, rollouts := "complete", "healthy"
	if outcome == "failed" {
		rollouts = "failed"
	}
	return map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging",
		"namespace": "agent-memory-staging", "kubernetes_context": "staging-context",
		"release_id": releaseID, "started_at": startedAt, "completed_at": completedAt,
		"outcome": outcome, "images": images,
		"migration": map[string]any{"outcome": migration}, "rollouts": map[string]any{"outcome": rollouts},
		"deployments": []map[string]any{
			{"name": "agent-memory-api", "revision": "7"},
			{"name": "agent-memory-worker", "revision": "7"},
			{"name": "agent-memory-reconciler", "revision": "7"},
		},
		"rollback": map[string]any{"attempted": attempted, "succeeded": succeeded},
	}
}

func baselineImage(name string) string {
	return "registry.example/agent-memory-" + name + "@sha256:" + strings.Repeat("a", 64)
}

func attemptedImage(name string) string {
	return "registry.example/agent-memory-" + name + "@sha256:" + strings.Repeat("b", 64)
}

func writeJSON(t *testing.T, name string, value any) string {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func cloneMap(value map[string]any) map[string]any {
	contents, _ := json.Marshal(value)
	var clone map[string]any
	_ = json.Unmarshal(contents, &clone)
	return clone
}

func outcomeFor(receipt Receipt, outcome Outcome) int {
	count := 0
	for _, deployment := range receipt.Deployments {
		if deployment.Outcome == outcome {
			count++
		}
	}
	return count
}
