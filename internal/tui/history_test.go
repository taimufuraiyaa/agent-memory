package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTerminalHistoryPersistsRenamesDeletesAndSeparatesWorkspaces(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	base := &consoleTestBackend{response: json.RawMessage(`{"context_block":"Grounded result"}`)}
	backend, err := WithHistory(base, "selected", dir)
	if err != nil {
		t.Fatal(err)
	}
	console := backend.(ConsoleBackend)
	if _, err := console.Execute(ctx, Request{Method: "POST", Path: "/api/v1/memories/recall/preview", Values: map[string]any{"task_description": "日本語 question"}}); err != nil {
		t.Fatal(err)
	}
	backend, err = WithHistory(base, "selected", dir)
	if err != nil {
		t.Fatal(err)
	}
	console = backend.(ConsoleBackend)
	raw, err := console.Execute(ctx, Request{Method: "GET", Path: "/api/v1/tui/history"})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Items []HistoryItem `json:"items"`
	}
	_ = json.Unmarshal(raw, &result)
	if len(result.Items) != 1 || result.Items[0].Query != "日本語 question" || result.Items[0].Kind != "chat" {
		t.Fatalf("history=%s", raw)
	}
	id := result.Items[0].ID
	if _, err := console.Execute(ctx, Request{Method: "POST", Path: "/api/v1/tui/history/rename", Values: map[string]any{"id": id, "title": "Renamed"}}); err != nil {
		t.Fatal(err)
	}
	other, err := WithHistory(base, "other", dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = other.(ConsoleBackend).Execute(ctx, Request{Method: "GET", Path: "/api/v1/tui/history"})
	_ = json.Unmarshal(raw, &result)
	if len(result.Items) != 0 {
		t.Fatal("history leaked into other workspace")
	}
	if _, err := console.Execute(ctx, Request{Method: "POST", Path: "/api/v1/tui/history/delete", Values: map[string]any{"id": id}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "selected.json"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private history permissions: info=%v err=%v", info, err)
	}
}
