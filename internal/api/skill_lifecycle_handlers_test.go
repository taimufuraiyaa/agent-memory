package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestStandaloneSkillLifecycleListInspectProposeReplayAndStale(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err == nil {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	})
	authorizer := &skillMutationTestAuthorizer{}
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir(), DBPath: filepath.Join(root, "lifecycle.db"), ProjectRoot: root, SkillMutationAuthorizer: authorizer}
	assets, err := svc.resolve(context.Background(), "ws")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	candidate := core.SkillCandidate{ID: "candidate-api", Workspace: "ws", Kind: core.SkillCandidateCreate, Summary: "API proposed skill", ExpectedBenefit: "Reuse verified work", RiskTier: core.SkillRiskLow, Confidence: .9, State: core.SkillCandidateProposed, SourceMemoryIDs: []string{"memory-1"}, DeduplicationHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedBy: "agent", CreatedAt: now, UpdatedAt: now}
	if _, _, err := assets.Store.PutSkillCandidate(context.Background(), candidate); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewMux(svc))
	t.Cleanup(func() { server.Close(); _ = svc.Close() })
	payload := map[string]any{"candidate_id": candidate.ID, "skill_name": "api-skill", "description": "API lifecycle skill", "files": map[string]string{"SKILL.md": "---\nname: api-skill\ndescription: API lifecycle skill.\n---\n\n# API skill\n\nRun safely.\n"}}
	first := postJSON(t, server.URL+"/api/v1/skills/lifecycle", map[string]any{"operation": "propose", "workspace": "ws", "actor": "agent", "payload": payload})["result"].(map[string]any)
	revision := first["revision"].(map[string]any)
	if revision["state"] != "draft" {
		t.Fatalf("unexpected proposal: %#v", first)
	}
	replayed := postJSON(t, server.URL+"/api/v1/skills/lifecycle", map[string]any{"operation": "propose", "workspace": "ws", "actor": "agent", "payload": payload})["result"].(map[string]any)
	if replayed["replayed"] != true || replayed["revision"].(map[string]any)["id"] != revision["id"] {
		t.Fatalf("proposal did not replay: %#v", replayed)
	}
	listed := getJSON(t, server.URL+"/api/v1/skills/lifecycle/list?workspace=ws")
	if len(listed["skills"].([]any)) != 1 {
		t.Fatalf("missing skill: %#v", listed)
	}
	inspected := getJSON(t, server.URL+"/api/v1/skills/inspect?workspace=ws&skill_id="+first["skill"].(map[string]any)["id"].(string))
	if len(inspected["revisions"].([]any)) != 1 {
		t.Fatalf("missing revision: %#v", inspected)
	}

	status := postSkillLifecycleStatus(t, server.URL, map[string]any{"operation": "disable", "workspace": "ws", "actor": "agent", "payload": map[string]any{"revision_id": revision["id"], "expected_state": "testing"}})
	if status >= 200 && status < 300 {
		t.Fatalf("stale transition unexpectedly succeeded: %d", status)
	}
	authorizer.err = errors.New("not authorized")
	status = postSkillLifecycleStatus(t, server.URL, map[string]any{"operation": "disable", "workspace": "ws", "actor": "agent", "payload": map[string]any{"revision_id": revision["id"], "expected_state": "draft"}})
	if status != http.StatusForbidden {
		t.Fatalf("unauthorized mutation status=%d", status)
	}
}

func TestStandaloneSkillLifecycleEvaluationFailsClosedWithoutRunner(t *testing.T) {
	svc := &Service{Workspace: "ws", BaseDir: t.TempDir(), SkillMutationAuthorizer: &skillMutationTestAuthorizer{}}
	server := httptest.NewServer(NewMux(svc))
	t.Cleanup(func() { server.Close(); _ = svc.Close() })
	status := postSkillLifecycleStatus(t, server.URL, map[string]any{"operation": "evaluate", "workspace": "ws", "actor": "agent", "payload": map[string]any{}})
	if status >= 200 && status < 300 {
		t.Fatalf("evaluation without restricted runner succeeded: %d", status)
	}
}

type skillMutationTestAuthorizer struct{ err error }

func (a *skillMutationTestAuthorizer) AuthorizeSkillMutation(context.Context, string, string, string, string) error {
	return a.err
}

func postSkillLifecycleStatus(t *testing.T, base string, body any) int {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Post(base+"/api/v1/skills/lifecycle", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}
