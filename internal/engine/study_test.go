package engine

import (
	"context"
	"os"
	"path/filepath"
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
