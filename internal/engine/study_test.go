package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestStudyEngineDryRunAndWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Project\nThis service handles orders.\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n// handles payments\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "study.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	engine := NewStudyEngine(NewWritePipeline(store))
	dry, err := engine.Ingest(context.Background(), "ws", root, true)
	if err != nil {
		t.Fatalf("dry run ingest: %v", err)
	}
	if !dry.DryRun || dry.ScannedFiles == 0 || dry.Extracted == 0 || len(dry.WrittenIDs) != 0 {
		t.Fatalf("unexpected dry result: %+v", dry)
	}

	wet, err := engine.Ingest(context.Background(), "ws", root, false)
	if err != nil {
		t.Fatalf("write ingest: %v", err)
	}
	if wet.DryRun || len(wet.WrittenIDs) == 0 {
		t.Fatalf("expected written IDs in non-dry run")
	}

	again, err := engine.Ingest(context.Background(), "ws", root, false)
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	memories, err := store.ListMemoriesByWorkspace(context.Background(), "ws")
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}
	if len(memories) != len(wet.WrittenIDs) {
		t.Fatalf("expected idempotent rerun (memory count unchanged), count=%d first_written=%d second_written=%d", len(memories), len(wet.WrittenIDs), len(again.WrittenIDs))
	}
}

func TestStudyEngineOptionsIgnoreAndMaxFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep.md"), []byte("# Keep\nhello"), 0o644); err != nil {
		t.Fatalf("write keep.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "skip.md"), []byte("# Skip\nworld"), 0o644); err != nil {
		t.Fatalf("write skip.md: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "study-options.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	engine := NewStudyEngine(NewWritePipeline(store))
	out, err := engine.IngestWithOptions(context.Background(), StudyOptions{
		Workspace: "ws",
		Sources:   []string{root},
		Depth:     "shallow",
		DryRun:    true,
		MaxFiles:  1,
		Ignore:    []string{"skip.md"},
	})
	if err != nil {
		t.Fatalf("ingest with options: %v", err)
	}
	if out.SourcesScanned != 1 {
		t.Fatalf("expected 1 source scanned, got %d", out.SourcesScanned)
	}
	if out.ScannedFiles != 1 {
		t.Fatalf("expected max-files to cap scanned files at 1, got %d", out.ScannedFiles)
	}
}

func TestStudyEngineDoesNotMutateExistingMemoryMarkdownSource(t *testing.T) {
	root := t.TempDir()
	memoryPath := filepath.Join(root, "MEMORY.md")
	original := "# Agent Memory\n\nPinned facts must remain stable.\n"
	if err := os.WriteFile(memoryPath, []byte(original), 0o644); err != nil {
		t.Fatalf("write MEMORY.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Readme\nservice notes"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "study-markdown.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	engine := NewStudyEngine(NewWritePipeline(store))
	_, err = engine.IngestWithOptions(context.Background(), StudyOptions{
		Workspace: "ws",
		Sources:   []string{root},
		Depth:     "medium",
		DryRun:    false,
	})
	if err != nil {
		t.Fatalf("ingest with memory markdown source: %v", err)
	}

	after, err := os.ReadFile(memoryPath)
	if err != nil {
		t.Fatalf("read MEMORY.md: %v", err)
	}
	if string(after) != original {
		t.Fatalf("expected MEMORY.md to remain unchanged by study")
	}
}

func TestStudyEngineBoundedIngestion_GitignoreBinaryOversizeAndErrors(t *testing.T) {
	root := t.TempDir()

	// Valid source file.
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Project\nThis service handles orders.\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	// .gitignore file that ignores the secrets/ directory.
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("secrets/\n*.log\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	// Subdirectory with a gitignored file.
	secretsDir := filepath.Join(root, "secrets")
	if err := os.MkdirAll(secretsDir, 0o755); err != nil {
		t.Fatalf("mkdir secrets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secretsDir, "token.txt"), []byte("secret: abc123def456"), 0o644); err != nil {
		t.Fatalf("write token.txt: %v", err)
	}

	// A >256KB file that should be skipped.
	largePath := filepath.Join(root, "large.go")
	largeData := make([]byte, 300*1024) // 300 KB
	for i := range largeData {
		largeData[i] = 'a'
	}
	if err := os.WriteFile(largePath, largeData, 0o644); err != nil {
		t.Fatalf("write large.go: %v", err)
	}

	// A binary file (null bytes) that should be skipped.
	// Use a .txt extension so isStudyFile accepts it (binary check happens after).
	binaryPath := filepath.Join(root, "binary.txt")
	binaryData := []byte("PK\x00\x01\x02some binary data")
	if err := os.WriteFile(binaryPath, binaryData, 0o644); err != nil {
		t.Fatalf("write binary.txt: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "study-bounded.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	engine := NewStudyEngine(NewWritePipeline(store))
	out, err := engine.IngestWithOptions(context.Background(), StudyOptions{
		Workspace: "ws",
		Sources:   []string{root},
		Depth:     "medium",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// Only README.md should be scanned and extracted.
	// large.go: .go extension (in isStudyFile) → too large → skipped with error.
	// binary.txt: .txt extension (in isStudyFile) → binary sniff → skipped with error.
	// token.txt: .txt extension (in isStudyFile) → gitignored → skipped silently.
	if out.ScannedFiles != 1 {
		t.Fatalf("expected 1 scanned file (README.md), got %d", out.ScannedFiles)
	}
	if out.Extracted != 1 {
		t.Fatalf("expected 1 extracted, got %d", out.Extracted)
	}
	if out.Skipped < 2 {
		t.Fatalf("expected at least 2 skipped (large.go + binary.txt), got %d", out.Skipped)
	}

	// Verify errors contain the expected file paths and reasons.
	foundLarge := false
	foundBinary := false
	for _, e := range out.Errors {
		if strings.Contains(e.Path, "large.go") && strings.Contains(e.Reason, "too large") {
			foundLarge = true
		}
		if strings.Contains(e.Path, "binary.txt") && strings.Contains(e.Reason, "binary") {
			foundBinary = true
		}
	}
	if !foundLarge {
		t.Fatalf("expected error for large.go too large, errors: %+v", out.Errors)
	}
	if !foundBinary {
		t.Fatalf("expected error for binary.txt, errors: %+v", out.Errors)
	}
}

func TestStudyEngineStructureAwareTruncation(t *testing.T) {
	root := t.TempDir()

	// A JSON file with nested structure.
	jsonContent := "{\n  \"name\": \"config\",\n  \"settings\": {\n    \"debug\": true,\n    \"port\": 8080\n  },\n  \"nested\": {\n    \"deep\": {\n      \"key\": \"value\"\n    }\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(jsonContent), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	// A markdown file with a fenced code block.
	mdContent := "# Title\n\nSome text here.\n\n```go\npackage main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\n\nMore text after fence.\n"
	if err := os.WriteFile(filepath.Join(root, "code.md"), []byte(mdContent), 0o644); err != nil {
		t.Fatalf("write code.md: %v", err)
	}

	dbPath := filepath.Join(t.TempDir(), "study-truncate.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	engine := NewStudyEngine(NewWritePipeline(store))
	out, err := engine.IngestWithOptions(context.Background(), StudyOptions{
		Workspace: "ws",
		Sources:   []string{root},
		Depth:     "shallow",
		DryRun:    true,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	if out.ScannedFiles != 2 {
		t.Fatalf("expected 2 scanned files, got %d", out.ScannedFiles)
	}
	if out.Extracted != 2 {
		t.Fatalf("expected 2 extracted, got %d", out.Extracted)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("expected no errors, got %+v", out.Errors)
	}
}
