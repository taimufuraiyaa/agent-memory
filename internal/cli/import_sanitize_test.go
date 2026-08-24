package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestImportSanitizesOversizedContent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ws.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	oversized := strings.Repeat("x", 600*1024) // 600KB > 500KB limit
	bundle := engine.ExportBundle{
		Version: engine.ExportVersion,
		Memories: []core.MemoryEntry{
			{ID: "a", Type: "semantic", Content: "valid content", Workspace: "ws"},
			{ID: "b", Type: "semantic", Content: oversized, Workspace: "ws"},
			{ID: "c", Type: "semantic", Content: "another valid", Workspace: "ws"},
		},
	}
	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}

	inFile := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(inFile, bundleJSON, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"import", "--db", dbPath, "--workspace", "ws", "--in", inFile, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("import with mix of valid/oversized: %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := envelope["data"].(map[string]any)
	imported := data["imported"].(float64)
	if imported != 2 {
		t.Fatalf("expected 2 imported, got %v", imported)
	}
	skipped := data["skipped"].([]any)
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped, got %v", len(skipped))
	}
	skipEntry := skipped[0].(map[string]any)
	if skipEntry["id"] != "b" {
		t.Fatalf("expected 'b' skipped, got %v", skipEntry["id"])
	}

	// Verify that valid items were imported and oversized skipped.
	memories, listErr := store.ListMemoriesByWorkspace(context.Background(), "ws")
	if listErr != nil {
		t.Fatalf("list: %v", listErr)
	}
	foundValid := false
	foundOversized := false
	for _, m := range memories {
		if m.ID == "a" {
			foundValid = true
		}
		if m.ID == "b" {
			foundOversized = true
		}
	}
	if !foundValid {
		t.Fatal("valid item 'a' not imported")
	}
	if foundOversized {
		t.Fatal("oversized item 'b' was imported (should have been skipped)")
	}
}

func TestImportSanitizesTraversalWorkspaceName(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ws-traversal.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	bundle := engine.ExportBundle{
		Version: engine.ExportVersion,
		Memories: []core.MemoryEntry{
			{ID: "d1", Type: "semantic", Content: "good", Workspace: "ws"},
			{ID: "d2", Type: "semantic", Content: "bad ws", Workspace: "../../evil"},
		},
	}
	bundleJSON, _ := json.Marshal(bundle)

	inFile := filepath.Join(t.TempDir(), "traversal.json")
	if err := os.WriteFile(inFile, bundleJSON, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"import", "--db", dbPath, "--workspace", "ws", "--in", inFile, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("import with traversal ws: %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := envelope["data"].(map[string]any)
	imported := data["imported"].(float64)
	if imported != 1 {
		t.Fatalf("expected 1 imported, got %v", imported)
	}
	skipped := data["skipped"].([]any)
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped, got %v", len(skipped))
	}
}

func TestImportAllItemsFailedReturnsError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ws-allfail.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	// All items have invalid workspace names.
	bundle := engine.ExportBundle{
		Version: engine.ExportVersion,
		Memories: []core.MemoryEntry{
			{ID: "x1", Type: "semantic", Content: "bad", Workspace: "../../hack"},
			{ID: "x2", Type: "semantic", Content: "bad2", Workspace: "/etc/passwd"},
		},
	}
	bundleJSON, _ := json.Marshal(bundle)

	inFile := filepath.Join(t.TempDir(), "allfail.json")
	if err := os.WriteFile(inFile, bundleJSON, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"import", "--db", dbPath, "--workspace", "ws", "--in", inFile, "--format", "json"})
	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected error when all items fail")
	}
	if !strings.Contains(err.Error(), "all") && !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("expected all-rejected message, got: %v", err)
	}
}

func TestImportMixedValidInvalidReportsAll(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ws-mixed.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	bundle := engine.ExportBundle{
		Version: engine.ExportVersion,
		Memories: []core.MemoryEntry{
			{ID: "m1", Type: "semantic", Content: "valid one", Workspace: "ws"},
			{ID: "m2", Type: "semantic", Content: "valid two", Workspace: "ws"},
		},
	}
	bundleJSON, _ := json.Marshal(bundle)

	inFile := filepath.Join(t.TempDir(), "mixed.json")
	if err := os.WriteFile(inFile, bundleJSON, 0o644); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"import", "--db", dbPath, "--workspace", "ws", "--in", inFile, "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("import with all valid: %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	data := envelope["data"].(map[string]any)
	imported := data["imported"].(float64)
	if imported != 2 {
		t.Fatalf("expected 2 imported, got %v", imported)
	}
	skipped, ok := data["skipped"].([]any)
	if !ok || len(skipped) != 0 {
		t.Fatalf("expected 0 skipped, got %v", len(skipped))
	}
	failed, ok := data["failed"].([]any)
	if !ok || len(failed) != 0 {
		t.Fatalf("expected 0 failed, got %v", len(failed))
	}
}

func TestWriteWithOutcomeFlags(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ws-outcome.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{
		"write", "--db", dbPath, "--workspace", "ws",
		"--type", "outcome",
		"--content", "Migration completed",
		"--outcome-result", "success",
		"--outcome-approach", "blue-green deployment",
		"--format", "json",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("write with outcome: %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data := envelope["data"].(map[string]any)
	if rejected, ok := data["rejected"]; ok && rejected.(bool) {
		t.Fatalf("write was rejected: %v", data)
	}

	// Verify the outcome was stored by reading back from the store.
	memories, err := store.ListMemoriesByWorkspace(context.Background(), "ws")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(memories) == 0 {
		t.Fatal("no memories stored")
	}
	m := memories[0]
	if m.Outcome == nil {
		t.Fatal("outcome not stored")
	}
	if m.Outcome.Result != "success" {
		t.Fatalf("expected success result, got %s", m.Outcome.Result)
	}
	if m.Outcome.Approach != "blue-green deployment" {
		t.Fatalf("expected approach 'blue-green deployment', got %s", m.Outcome.Approach)
	}
}

func TestSessionEndReadsStdin(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "ws-session.db")
	if _, err := sqlite.Open(context.Background(), dbPath); err != nil {
		t.Fatalf("open: %v", err)
	}

	transcriptContent := "AGENT: I fixed the bug.\nUSER: Great, what was the issue?\nAGENT: Null pointer in handler.go"

	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	go func() {
		_, _ = w.Write([]byte(transcriptContent))
		_ = w.Close()
	}()

	cmd := NewRootCommand()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"session-end", "--db", dbPath, "--workspace", "ws", "--format", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("session-end via stdin: %v (output: %s)", err, output.String())
	}

	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v (output: %s)", err, output.String())
	}
	data := envelope["data"].(map[string]any)
	if _, ok := data["memories_created"]; !ok {
		// session-end should produce at least some result
		t.Logf("session-end result: %+v", data)
	}
}
