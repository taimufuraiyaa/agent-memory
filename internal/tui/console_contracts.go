package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type WorkspaceSelector interface {
	SelectWorkspace(context.Context, string) (Backend, error)
}
type workspaceSelectedMsg struct {
	generation int
	workspace  string
	backend    Backend
	err        error
}

func (b *HTTPBackend) SelectWorkspace(ctx context.Context, workspace string) (Backend, error) {
	data, err := b.Execute(ctx, Request{Method: "GET", Path: "/api/v1/projects/list"})
	if err != nil {
		return nil, err
	}
	var projects struct {
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &projects); err != nil {
		return nil, err
	}
	for _, project := range projects.Projects {
		if project.Name == workspace {
			return NewHTTPBackend(b.baseURL, workspace, b.client)
		}
	}
	return nil, errors.New("workspace is no longer registered; refresh the list")
}

// These account-level and graph handlers deliberately use strict JSON schemas.
func bindConsoleValues(path string, values map[string]any, workspace string) {
	switch {
	case path == "/api/v1/graph-index/operations" || path == "/api/v1/graph-index/review" || path == "/api/v1/graph-index/feedback":
		values["scope"] = map[string]any{"workspace_id": workspace}
		delete(values, "workspace")
	case strings.HasPrefix(path, "/api/v1/client-profiles"):
		delete(values, "workspace")
		if path != "/api/v1/client-profiles" {
			delete(values, "id")
		}
	case path == "/api/v1/library/local-llm" || path == "/api/v1/library/local-llm/test" || strings.HasPrefix(path, "/api/v1/rights-attestation/") || strings.HasPrefix(path, "/api/v1/library/seminars/"):
		delete(values, "workspace")
	case path == "/api/v1/projects/rename":
		values["from"] = workspace
	case path == "/api/v1/projects/delete":
		values["project_name"] = workspace
		values["yes"] = true
	}
}

func (b *HTTPBackend) projectImportedSource(ctx context.Context, request Request, result json.RawMessage) (json.RawMessage, error) {
	var job struct {
		ID     string `json:"id"`
		Result struct {
			WorkID    string `json:"work_id"`
			EditionID string `json:"edition_id"`
			AssetID   string `json:"asset_id"`
			Format    string `json:"format"`
		} `json:"result"`
	}
	if err := json.Unmarshal(result, &job); err != nil || job.Result.EditionID == "" {
		return nil, errors.New("import returned no edition; inspect the import job")
	}
	existing, err := b.Execute(ctx, Request{Method: "GET", Path: "/api/v1/notes"})
	if err != nil {
		return nil, fmt.Errorf("source imported as edition %s, but note listing failed; retry import to complete source registration", job.Result.EditionID)
	}
	var notes struct {
		Notes []struct {
			ID         string         `json:"id"`
			Properties map[string]any `json:"properties"`
		} `json:"notes"`
	}
	if err := json.Unmarshal(existing, &notes); err != nil {
		return nil, errors.New("source imported but source note listing could not be decoded")
	}
	for _, note := range notes.Notes {
		if note.Properties["library_edition_id"] == job.Result.EditionID {
			return b.Execute(ctx, Request{Method: "GET", Path: "/api/v1/notes/get", Values: map[string]any{"note_id": note.ID}})
		}
	}
	title := fmt.Sprint(request.Values["title"])
	note, err := b.Execute(ctx, Request{Method: "POST", Path: "/api/v1/notes/create", Values: map[string]any{
		"title": title, "path": "library/" + job.Result.EditionID + ".md", "body": "# " + title + "\n\nImported " + strings.ToUpper(job.Result.Format) + " source.",
		"properties": map[string]any{"source": "library import", "library_work_id": job.Result.WorkID, "library_edition_id": job.Result.EditionID, "library_asset_id": job.Result.AssetID, "library_format": job.Result.Format, "library_import_job_id": job.ID, "library_principal_id": request.Values["principal_id"]},
	}})
	if err != nil {
		return nil, fmt.Errorf("source imported as edition %s, but note registration failed; retry import to complete registration", job.Result.EditionID)
	}
	return note, nil
}
