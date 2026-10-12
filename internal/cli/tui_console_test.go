package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/api"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	memorytui "github.com/taimufuraiyaa/agent-memory/internal/tui"
)

func TestTUIConsoleLocalHandlerUsesExactDatabaseAndRevisionContract(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service := &api.Service{Workspace: "selected", BaseDir: dir, DBPath: filepath.Join(dir, "custom.db"), EmbeddingProvider: naturalTestProvider{}}
	defer service.Close()
	client := newTUIHandlerClient(api.NewMux(service))
	backend, err := memorytui.NewHTTPBackend("http://localhost", "selected", client)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := backend.Execute(ctx, memorytui.Request{Method: "POST", Path: "/api/v1/notes/create", Values: map[string]any{"workspace": "wrong", "path": "notes/test.md", "title": "Title", "body": "Unicode 日本語"}})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Note struct {
			ID        string `json:"id"`
			Workspace string `json:"workspace"`
			Revision  int    `json:"revision"`
		} `json:"note"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Note.Workspace != "selected" || result.Note.Revision != 1 {
		t.Fatalf("note=%+v", result.Note)
	}
	request := memorytui.Request{Method: "POST", Path: "/api/v1/notes/update", Values: map[string]any{"note_id": result.Note.ID, "expected_revision": 1, "path": "notes/test.md", "title": "Updated", "body": "Second"}}
	if _, err := backend.Execute(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Execute(ctx, request); err == nil {
		t.Fatal("stale note revision accepted")
	}
	overview, err := backend.LoadOverview(ctx)
	if err != nil || overview.Workspace != "selected" {
		t.Fatalf("overview=%+v err=%v", overview, err)
	}
}

type tuiGraphController struct {
	request contracts.GraphOperationRequest
}

func (c *tuiGraphController) Readiness(context.Context, core.GraphScope, string) (contracts.GraphIndexReadiness, error) {
	return contracts.GraphIndexReadiness{}, nil
}
func (c *tuiGraphController) Status(context.Context, core.GraphScope, string) (contracts.GraphIndexStatus, error) {
	return contracts.GraphIndexStatus{}, nil
}
func (c *tuiGraphController) Operate(_ context.Context, r contracts.GraphOperationRequest) (contracts.GraphOperationResult, error) {
	c.request = r
	return contracts.GraphOperationResult{}, nil
}

func TestTUIConsoleGraphStrictContractBindsSelectedWorkspace(t *testing.T) {
	controller := &tuiGraphController{}
	backend, _ := memorytui.NewHTTPBackend("http://localhost", "selected", newTUIHandlerClient(api.NewMux(&api.Service{GraphOperations: controller})))
	_, err := backend.Execute(context.Background(), memorytui.Request{Method: "POST", Path: "/api/v1/graph-index/operations", Values: map[string]any{"action": "update", "configuration_id": "default", "scope": map[string]any{"workspace_id": "wrong", "tenant_id": "wrong"}}})
	if err != nil {
		t.Fatal(err)
	}
	if controller.request.Scope.WorkspaceID != "selected" || controller.request.Scope.TenantID != "" || controller.request.Action != "update" {
		t.Fatalf("request=%+v", controller.request)
	}
}

func TestTUIConsoleMemoryMutationAndExportImportJourney(t *testing.T) {
	t.Setenv("AGENT_MEMORY_ENABLED", "1")
	ctx := context.Background()
	dir := t.TempDir()
	service := &api.Service{Workspace: "selected", BaseDir: dir, DBPath: filepath.Join(dir, "custom.db"), EmbeddingProvider: naturalTestProvider{}}
	defer service.Close()
	backend, _ := memorytui.NewHTTPBackend("http://localhost", "selected", newTUIHandlerClient(api.NewMux(service)))
	_, err := backend.Execute(ctx, memorytui.Request{Method: "POST", Path: "/api/v1/memories/write", Values: map[string]any{"type": "semantic", "content": "Terminal migration preserves workspace isolation"}})
	if err != nil {
		t.Fatal(err)
	}
	items, err := backend.ListRecent(ctx, 100)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	id := items[0].ID
	if _, err := backend.Execute(ctx, memorytui.Request{Method: "POST", Path: "/api/v1/memories/pin", Values: map[string]any{"memory_id": id, "pinned": true}}); err != nil {
		t.Fatal(err)
	}
	overview, err := backend.LoadOverview(ctx)
	if err != nil || overview.Pinned != 1 || overview.TypeCounts["semantic"] != 1 {
		t.Fatalf("overview=%+v err=%v", overview, err)
	}
	path := filepath.Join(dir, "memories.json")
	if _, err := backend.Execute(ctx, memorytui.Request{Method: "GET", Path: "/api/v1/memories/export", OutputPath: path}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Execute(ctx, memorytui.Request{Method: "POST", Path: "/api/v1/memories/delete", Values: map[string]any{"memory_ids": []string{id}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Execute(ctx, memorytui.Request{Method: "POST", Path: "/api/v1/memories/import", InputPath: path}); err != nil {
		t.Fatal(err)
	}
	items, err = backend.ListRecent(ctx, 100)
	if err != nil || len(items) != 1 || !items[0].Pinned {
		t.Fatalf("restored items=%+v err=%v", items, err)
	}
	search, err := backend.Search(ctx, "Terminal migration", 20)
	if err != nil || len(search) == 0 {
		t.Fatalf("search=%+v err=%v", search, err)
	}
}

func TestTUIConsoleClientProfilesPreserveStrictBodyAndRevision(t *testing.T) {
	ctx := context.Background()
	service := &api.Service{BaseDir: t.TempDir()}
	defer service.Close()
	if err := api.ConfigureLocalClientProfiles(service); err != nil {
		t.Fatal(err)
	}
	backend, _ := memorytui.NewHTTPBackend("http://localhost", "selected", newTUIHandlerClient(api.NewMux(service)))
	_, err := backend.Execute(ctx, memorytui.Request{Method: "POST", Path: "/api/v1/client-profiles", Values: map[string]any{"id": "terminal-test", "display_name": "Terminal", "client_kind": "codex", "tool_profile": "default"}})
	if err != nil {
		t.Fatal(err)
	}
	request := memorytui.Request{Method: "PUT", Path: "/api/v1/client-profiles/terminal-test", Values: map[string]any{"id": "terminal-test", "display_name": "Updated", "client_kind": "codex", "tool_profile": "expanded", "expected_revision": 1}}
	if _, err := backend.Execute(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Execute(ctx, request); err == nil {
		t.Fatal("stale profile revision accepted")
	}
}

func TestTUIConsoleImportAppearsInSourcesAndHonorsRights(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service := &api.Service{Workspace: "selected", BaseDir: dir, DBPath: filepath.Join(dir, "custom.db"), EmbeddingProvider: naturalTestProvider{}}
	defer service.Close()
	if err := api.ConfigureLocalRightsAttestation(ctx, service); err != nil {
		t.Fatal(err)
	}
	backend, _ := memorytui.NewHTTPBackend("http://localhost", "selected", newTUIHandlerClient(api.NewMux(service)))
	path := filepath.Join(dir, "book.md")
	if err := os.WriteFile(path, []byte("# Test Book\n\nA useful chapter about testing."), 0600); err != nil {
		t.Fatal(err)
	}
	request := memorytui.Request{Method: "POST", Path: "/api/v1/library/imports", UploadPath: path, ProjectSource: true, Values: map[string]any{"title": "Test Book", "format": "markdown", "rights_basis": "author_owned", "principal_id": "local-user", "library_kind": "personal", "language": "en", "edition_label": "original"}}
	if _, err := backend.Execute(ctx, request); err == nil {
		t.Fatal("import bypassed rights attestation")
	}
	policy := attestation.CurrentPolicy()
	ids := []string{}
	for _, statement := range policy.Statements {
		ids = append(ids, statement.ID)
	}
	if _, err := backend.Execute(ctx, memorytui.Request{Method: "POST", Path: "/api/v1/rights-attestation/accept", Values: map[string]any{"policy_version": policy.Version, "accepted_statement_ids": ids}}); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Execute(ctx, request); err != nil {
		t.Fatal(err)
	}
	raw, err := backend.Execute(ctx, memorytui.Request{Method: "GET", Path: "/api/v1/notes"})
	if err != nil {
		t.Fatal(err)
	}
	var notes struct {
		Notes []struct {
			Title      string         `json:"title"`
			Properties map[string]any `json:"properties"`
		} `json:"notes"`
	}
	_ = json.Unmarshal(raw, &notes)
	if len(notes.Notes) != 1 || notes.Notes[0].Properties["library_edition_id"] == nil {
		t.Fatalf("import missing source projection: %s", raw)
	}
	if _, err := backend.Execute(ctx, request); err != nil {
		t.Fatal(err)
	}
	raw, _ = backend.Execute(ctx, memorytui.Request{Method: "GET", Path: "/api/v1/notes"})
	_ = json.Unmarshal(raw, &notes)
	if len(notes.Notes) != 1 {
		t.Fatal("reimport duplicated source note")
	}
}
