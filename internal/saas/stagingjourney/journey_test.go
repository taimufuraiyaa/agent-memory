package stagingjourney

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCollectBindsReadyHumanAndAgentJourneysToPassedRelease(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	releasePath, releaseID, releaseDigest := writeReleaseFixture(t, directory, now.Add(-time.Hour))
	human := readyJourney(HumanWeb, releaseID, releaseDigest, now.Add(-20*time.Minute), "0123456789abcdef0123456789abcdef")
	agent := readyJourney(ScopedAgent, releaseID, releaseDigest, now.Add(-10*time.Minute), "abcdef0123456789abcdef0123456789")
	for left, right := 0, len(human.Checks)-1; left < right; left, right = left+1, right-1 {
		human.Checks[left], human.Checks[right] = human.Checks[right], human.Checks[left]
	}
	humanPath := writeJSONFixture(t, directory, "human.json", human)
	agentPath := writeJSONFixture(t, directory, "agent.json", agent)

	receipt, err := Collect(releasePath, agentPath, humanPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Ready || receipt.Schema != ReceiptSchemaV1 || receipt.Environment != "staging" || receipt.ReleaseID != releaseID || receipt.ReleaseReceiptSHA256 != releaseDigest || !receipt.CollectedAt.Equal(now) {
		t.Fatalf("receipt=%+v", receipt)
	}
	if len(receipt.Journeys) != 2 || receipt.Journeys[0].ClientKind != HumanWeb || receipt.Journeys[1].ClientKind != ScopedAgent {
		t.Fatalf("journeys=%+v", receipt.Journeys)
	}
	for _, journey := range receipt.Journeys {
		if len(journey.Checks) != 5 || len(journey.InputSHA256) != 64 || journey.TraceID == "" {
			t.Fatalf("journey=%+v", journey)
		}
		for index, check := range journey.Checks {
			if check.ID != requiredChecks[index] || check.Outcome != OutcomePassed || uuid.Validate(check.RequestID) != nil {
				t.Fatalf("check=%+v", check)
			}
		}
	}
}

func TestCollectKeepsFailedJourneyValidButUnready(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	releasePath, releaseID, releaseDigest := writeReleaseFixture(t, directory, now.Add(-time.Hour))
	human := readyJourney(HumanWeb, releaseID, releaseDigest, now.Add(-20*time.Minute), "0123456789abcdef0123456789abcdef")
	agent := readyJourney(ScopedAgent, releaseID, releaseDigest, now.Add(-10*time.Minute), "abcdef0123456789abcdef0123456789")
	agent.Ready = false
	agent.Checks[2].Outcome = OutcomeFailed

	receipt, err := Collect(
		releasePath,
		writeJSONFixture(t, directory, "human.json", human),
		writeJSONFixture(t, directory, "agent.json", agent),
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	assessment := Assess(receipt)
	if receipt.Ready || assessment.ClientCount != 2 || assessment.CheckCount != 10 || assessment.PassedCount != 9 || assessment.FailedCount != 1 {
		t.Fatalf("receipt=%+v assessment=%+v", receipt, assessment)
	}
}

func TestCollectRejectsUnsafeOrContradictoryJourneyEvidence(t *testing.T) {
	now := time.Date(2026, 8, 10, 4, 0, 0, 0, time.UTC)
	tests := map[string]func(*testing.T, string, string, string, string, Journey, Journey) (string, string){
		"local classification": func(t *testing.T, directory, _, humanPath, agentPath string, human, _ Journey) (string, string) {
			human.Classification = "local_development"
			return writeJSONFixture(t, directory, "bad-human.json", human), agentPath
		},
		"release mismatch": func(t *testing.T, directory, _, humanPath, agentPath string, human, _ Journey) (string, string) {
			human.ReleaseReceiptSHA256 = strings.Repeat("f", 64)
			return writeJSONFixture(t, directory, "bad-human.json", human), agentPath
		},
		"duplicate client kind": func(_ *testing.T, _, _, humanPath, _ string, _, _ Journey) (string, string) {
			return humanPath, humanPath
		},
		"duplicate check": func(t *testing.T, directory, _, humanPath, agentPath string, human, _ Journey) (string, string) {
			human.Checks[1].ID = human.Checks[0].ID
			return writeJSONFixture(t, directory, "bad-human.json", human), agentPath
		},
		"duplicate request id": func(t *testing.T, directory, _, humanPath, agentPath string, _, agent Journey) (string, string) {
			var human Journey
			readJSONFixture(t, humanPath, &human)
			agent.Checks[0].RequestID = human.Checks[0].RequestID
			return humanPath, writeJSONFixture(t, directory, "bad-agent.json", agent)
		},
		"duplicate trace id": func(t *testing.T, directory, _, humanPath, agentPath string, _, agent Journey) (string, string) {
			var human Journey
			readJSONFixture(t, humanPath, &human)
			agent.TraceID = human.TraceID
			return humanPath, writeJSONFixture(t, directory, "bad-agent.json", agent)
		},
		"unknown check": func(t *testing.T, directory, _, humanPath, agentPath string, human, _ Journey) (string, string) {
			human.Checks[4].ID = "unexpected_check"
			return writeJSONFixture(t, directory, "bad-human.json", human), agentPath
		},
		"noncanonical request id": func(t *testing.T, directory, _, humanPath, agentPath string, human, _ Journey) (string, string) {
			human.Checks[0].RequestID = strings.ToUpper(human.Checks[0].RequestID)
			return writeJSONFixture(t, directory, "bad-human.json", human), agentPath
		},
		"invalid trace id": func(t *testing.T, directory, _, humanPath, agentPath string, human, _ Journey) (string, string) {
			human.TraceID = strings.ToUpper(human.TraceID)
			return writeJSONFixture(t, directory, "bad-human.json", human), agentPath
		},
		"contradictory readiness": func(t *testing.T, directory, _, humanPath, agentPath string, human, _ Journey) (string, string) {
			human.Ready = false
			return writeJSONFixture(t, directory, "bad-human.json", human), agentPath
		},
		"pre-release window": func(t *testing.T, directory, _, humanPath, agentPath string, human, _ Journey) (string, string) {
			human.StartedAt = now.Add(-2 * time.Hour)
			human.CompletedAt = now.Add(-110 * time.Minute)
			return writeJSONFixture(t, directory, "bad-human.json", human), agentPath
		},
		"stale window": func(t *testing.T, directory, _, humanPath, agentPath string, human, _ Journey) (string, string) {
			human.StartedAt = now.Add(-26 * time.Hour)
			human.CompletedAt = now.Add(-25 * time.Hour)
			return writeJSONFixture(t, directory, "bad-human.json", human), agentPath
		},
		"future window": func(t *testing.T, directory, _, humanPath, agentPath string, human, _ Journey) (string, string) {
			human.StartedAt = now.Add(time.Minute)
			human.CompletedAt = now.Add(2 * time.Minute)
			return writeJSONFixture(t, directory, "bad-human.json", human), agentPath
		},
		"oversize window": func(t *testing.T, directory, _, humanPath, agentPath string, human, _ Journey) (string, string) {
			human.StartedAt = human.CompletedAt.Add(-31 * time.Minute)
			return writeJSONFixture(t, directory, "bad-human.json", human), agentPath
		},
		"unknown field": func(t *testing.T, directory, _, _, agentPath string, _, _ Journey) (string, string) {
			bad := `{"schema":"agent-memory-staging-client-journey-v1","content":"secret"}`
			path := filepath.Join(directory, "bad-human.json")
			if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
				t.Fatal(err)
			}
			return path, agentPath
		},
		"symlink input": func(t *testing.T, directory, _, humanPath, agentPath string, _, _ Journey) (string, string) {
			link := filepath.Join(directory, "human-link.json")
			if err := os.Symlink(humanPath, link); err != nil {
				t.Fatal(err)
			}
			return link, agentPath
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			releasePath, releaseID, releaseDigest := writeReleaseFixture(t, directory, now.Add(-time.Hour))
			human := readyJourney(HumanWeb, releaseID, releaseDigest, now.Add(-20*time.Minute), "0123456789abcdef0123456789abcdef")
			agent := readyJourney(ScopedAgent, releaseID, releaseDigest, now.Add(-10*time.Minute), "abcdef0123456789abcdef0123456789")
			humanPath := writeJSONFixture(t, directory, "human.json", human)
			agentPath := writeJSONFixture(t, directory, "agent.json", agent)
			humanPath, agentPath = mutate(t, directory, releasePath, humanPath, agentPath, human, agent)
			if _, err := Collect(releasePath, humanPath, agentPath, now); err == nil {
				t.Fatal("unsafe journey evidence was accepted")
			}
		})
	}
}

func TestPublishIsPrivateCreateOnlyAndNonSymlink(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "receipt.json")
	receipt := Receipt{Schema: ReceiptSchemaV1, Ready: false, Environment: "staging"}
	if err := Publish(path, receipt); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := Publish(path, receipt); err == nil {
		t.Fatal("existing receipt was overwritten")
	}
	link := filepath.Join(directory, "link.json")
	if err := os.Symlink(filepath.Join(directory, "missing"), link); err != nil {
		t.Fatal(err)
	}
	if err := Publish(link, receipt); err == nil {
		t.Fatal("symlink receipt destination was accepted")
	}
}

func readyJourney(kind ClientKind, releaseID, releaseDigest string, completed time.Time, traceID string) Journey {
	checks := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		checks = append(checks, Check{ID: id, Outcome: OutcomePassed, RequestID: uuid.NewString()})
	}
	return Journey{
		Schema: JourneySchemaV1, Classification: "staging_external", Environment: "staging",
		ReleaseID: releaseID, ReleaseReceiptSHA256: releaseDigest, ClientKind: kind,
		Ready: true, TraceID: traceID, StartedAt: completed.Add(-5 * time.Minute), CompletedAt: completed,
		Checks: checks,
	}
}

func writeReleaseFixture(t *testing.T, directory string, completed time.Time) (string, string, string) {
	t.Helper()
	releaseID := "release-20260810"
	digest := strings.Repeat("a", 64)
	receipt := map[string]any{
		"schema": "agent-memory-kubernetes-release-receipt-v1", "environment": "staging",
		"namespace": "agent-memory-staging", "kubernetes_context": "staging-context", "release_id": releaseID,
		"started_at": completed.Add(-10 * time.Minute), "completed_at": completed, "outcome": "passed",
		"images": map[string]string{
			"api": "registry.local/api@sha256:" + digest, "worker": "registry.local/worker@sha256:" + digest,
			"reconciler": "registry.local/reconciler@sha256:" + digest, "migrate": "registry.local/migrate@sha256:" + digest,
		},
		"migration": map[string]string{"outcome": "complete"}, "rollouts": map[string]string{"outcome": "healthy"},
		"deployments": []map[string]string{{"name": "agent-memory-api", "revision": "1"}, {"name": "agent-memory-worker", "revision": "1"}, {"name": "agent-memory-reconciler", "revision": "1"}},
		"rollback":    map[string]bool{"attempted": false, "succeeded": false},
	}
	path := writeJSONFixture(t, directory, "release.json", receipt)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, releaseID, sha256Hex(contents)
}

func writeJSONFixture(t *testing.T, directory, name string, value any) string {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(contents, value) != nil {
		t.Fatal("read fixture")
	}
}
