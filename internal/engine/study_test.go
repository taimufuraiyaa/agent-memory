package engine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestSafeTruncateDoesNotSplitUTF8Rune(t *testing.T) {
	got := safeTruncate("aaaa€bbbb", 5)
	if !utf8.ValidString(got) {
		t.Fatalf("safeTruncate emitted invalid UTF-8: %q", got)
	}
	if got != "aaaa" {
		t.Fatalf("expected complete rune boundary, got %q", got)
	}
}

func TestStudyEngineWritesValidUTF8WhenSummaryBudgetCutsRune(t *testing.T) {
	root := t.TempDir()
	content := strings.Repeat("a", 599) + "€ trailing"
	if err := os.WriteFile(filepath.Join(root, "unicode.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write unicode source: %v", err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "unicode.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	result, err := NewStudyEngine(NewWritePipeline(store)).IngestWithOptions(context.Background(), StudyOptions{
		Workspace: "unicode-project",
		Sources:   []string{root},
		Depth:     "shallow",
		MaxFiles:  1,
	})
	if err != nil {
		t.Fatalf("study unicode source: %v", err)
	}
	if len(result.Errors) != 0 || len(result.WrittenIDs) != 1 {
		t.Fatalf("expected one valid write without errors, got %+v", result)
	}
}

func TestStudyEngineChunksOversizedTextByDefault(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "large.json")
	content := strings.Repeat("0123456789", 4)
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write oversized source: %v", err)
	}

	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "chunked-study.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	study := NewStudyEngine(NewWritePipeline(store))
	first, err := study.IngestWithOptions(context.Background(), StudyOptions{
		Workspace:   "chunked-project",
		Sources:     []string{root},
		Depth:       "medium",
		MaxFiles:    1,
		MaxFileSize: 16,
	})
	if err != nil {
		t.Fatalf("study oversized source: %v", err)
	}
	if first.ScannedFiles != 1 || first.Extracted != 1 || first.ChunkedFiles != 1 || first.ExtractedChunks != 3 {
		t.Fatalf("unexpected chunked result counts: %+v", first)
	}
	if first.Skipped != 0 || first.TruncatedFiles != 0 || len(first.Errors) != 0 || len(first.WrittenIDs) != 3 {
		t.Fatalf("expected complete chunk ingestion without file errors: %+v", first)
	}

	memories, err := store.ListMemoriesByWorkspace(context.Background(), "chunked-project")
	if err != nil {
		t.Fatalf("list chunk memories: %v", err)
	}
	if len(memories) != 3 {
		t.Fatalf("expected one memory per source chunk, got %d", len(memories))
	}
	for _, memory := range memories {
		if memory.Source.FilePath != sourcePath || len(memory.Source.LineRange) != 2 {
			t.Fatalf("chunk memory is missing source provenance: %+v", memory.Source)
		}
		if !utf8.ValidString(memory.Content) {
			t.Fatalf("chunk memory is not valid UTF-8: %q", memory.Content)
		}
	}

	second, err := study.IngestWithOptions(context.Background(), StudyOptions{
		Workspace:   "chunked-project",
		Sources:     []string{root},
		Depth:       "medium",
		MaxFiles:    1,
		MaxFileSize: 16,
	})
	if err != nil {
		t.Fatalf("repeat chunked study: %v", err)
	}
	memories, err = store.ListMemoriesByWorkspace(context.Background(), "chunked-project")
	if err != nil {
		t.Fatalf("list repeated chunk memories: %v", err)
	}
	if len(memories) != 3 || len(second.WrittenIDs) != 3 {
		t.Fatalf("expected stable per-chunk deduplication, memories=%d result=%+v", len(memories), second)
	}
}

func TestStudyEngineBoundsChunksWithoutFailingTheStudy(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "huge.txt")
	content := strings.Repeat("a", 16*(maxStudyChunksPerFile+2))
	if err := os.WriteFile(sourcePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write huge source: %v", err)
	}

	result, err := NewStudyEngine(nil).IngestWithOptions(context.Background(), StudyOptions{
		Sources:     []string{root},
		Depth:       "medium",
		DryRun:      true,
		MaxFiles:    1,
		MaxFileSize: 16,
	})
	if err != nil {
		t.Fatalf("bounded chunking must not fail the study: %v", err)
	}
	if result.ScannedFiles != 1 || result.Extracted != 1 || result.ChunkedFiles != 1 || result.ExtractedChunks != maxStudyChunksPerFile {
		t.Fatalf("unexpected bounded chunk counts: %+v", result)
	}
	if result.Skipped != 0 || result.TruncatedFiles != 1 {
		t.Fatalf("bounded prefix must not be reported as a skipped file: %+v", result)
	}
	if len(result.Errors) != 1 || result.Errors[0].Path != sourcePath || !strings.Contains(result.Errors[0].Reason, "bounded chunk limit") {
		t.Fatalf("expected one truthful bounded-prefix diagnostic, got %+v", result.Errors)
	}
}

func TestStudyEngineChunksRejectInvalidUTF8WithoutFailingTheStudy(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "invalid.txt")
	if err := os.WriteFile(sourcePath, []byte{'v', 'a', 'l', 'i', 'd', '\n', 0xff, 0xfe}, 0o644); err != nil {
		t.Fatalf("write invalid UTF-8 source: %v", err)
	}

	result, err := NewStudyEngine(nil).IngestWithOptions(context.Background(), StudyOptions{
		Sources:     []string{root},
		Depth:       "medium",
		DryRun:      true,
		MaxFiles:    1,
		MaxFileSize: 4,
	})
	if err != nil {
		t.Fatalf("invalid UTF-8 must remain an isolated file failure: %v", err)
	}
	if result.ScannedFiles != 0 || result.Extracted != 0 || result.Skipped != 1 {
		t.Fatalf("unexpected invalid UTF-8 counts: %+v", result)
	}
	if len(result.Errors) != 1 || result.Errors[0].Path != sourcePath || !strings.Contains(result.Errors[0].Reason, "invalid UTF-8") {
		t.Fatalf("expected invalid UTF-8 diagnostic, got %+v", result.Errors)
	}
}

func TestStudyEngineDeepChunkSummaryHonorsWriteAdmissionLimit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "deep.md"), []byte(strings.Repeat("a", 3000)), 0o644); err != nil {
		t.Fatalf("write deep source: %v", err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "deep-study.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = store.Close() }()

	result, err := NewStudyEngine(NewWritePipeline(store)).IngestWithOptions(context.Background(), StudyOptions{
		Workspace: "deep-project",
		Sources:   []string{root},
		Depth:     "deep",
		MaxFiles:  1,
	})
	if err != nil {
		t.Fatalf("deep study: %v", err)
	}
	if len(result.Errors) != 0 || len(result.WrittenIDs) != 1 {
		t.Fatalf("deep summary must fit the write admission limit: %+v", result)
	}
}

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

func TestStudyEnginePagesEligibleFilesWithoutRepeatingTheFirstPage(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("# "+name+"\npage content"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	engine := NewStudyEngine(nil)
	first, err := engine.IngestWithOptions(context.Background(), StudyOptions{
		Sources:  []string{root},
		Depth:    "shallow",
		DryRun:   true,
		MaxFiles: 2,
	})
	if err != nil {
		t.Fatalf("study first page: %v", err)
	}
	if first.Offset != 0 || first.PageFiles != 2 || first.NextOffset != 2 || !first.HasMore {
		t.Fatalf("unexpected first page metadata: %+v", first)
	}
	if first.ScannedFiles != 2 || first.Extracted != 2 {
		t.Fatalf("unexpected first page counts: %+v", first)
	}

	second, err := engine.IngestWithOptions(context.Background(), StudyOptions{
		Sources:  []string{root},
		Depth:    "shallow",
		DryRun:   true,
		MaxFiles: 2,
		Offset:   first.NextOffset,
	})
	if err != nil {
		t.Fatalf("study second page: %v", err)
	}
	if second.Offset != 2 || second.PageFiles != 1 || second.NextOffset != 3 || second.HasMore {
		t.Fatalf("unexpected second page metadata: %+v", second)
	}
	if second.ScannedFiles != 1 || second.Extracted != 1 {
		t.Fatalf("expected only the final file on page two, got %+v", second)
	}
}

func TestStudyEngineSkippedEligibleFileAdvancesPageOffset(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("binary\x00page item"), 0o644); err != nil {
		t.Fatalf("write binary page item: %v", err)
	}
	for _, name := range []string{"b.md", "c.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("ok"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	engine := NewStudyEngine(nil)
	first, err := engine.IngestWithOptions(context.Background(), StudyOptions{
		Sources:     []string{root},
		Depth:       "shallow",
		DryRun:      true,
		MaxFiles:    2,
		MaxFileSize: 16,
	})
	if err != nil {
		t.Fatalf("study first page: %v", err)
	}
	if first.PageFiles != 2 || first.NextOffset != 2 || first.Skipped != 1 || first.ScannedFiles != 1 || !first.HasMore {
		t.Fatalf("unexpected mixed first page: %+v", first)
	}

	second, err := engine.IngestWithOptions(context.Background(), StudyOptions{
		Sources:     []string{root},
		Depth:       "shallow",
		DryRun:      true,
		MaxFiles:    2,
		MaxFileSize: 16,
		Offset:      first.NextOffset,
	})
	if err != nil {
		t.Fatalf("study second page: %v", err)
	}
	if second.PageFiles != 1 || second.NextOffset != 3 || second.Skipped != 0 || second.ScannedFiles != 1 || second.HasMore {
		t.Fatalf("skipped file was repeated or final page was wrong: %+v", second)
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

	// README.md and large.go should be scanned and extracted.
	// large.go: .go extension (in isStudyFile) → chunked automatically.
	// binary.txt: .txt extension (in isStudyFile) → binary sniff → skipped with error.
	// token.txt: .txt extension (in isStudyFile) → gitignored → skipped silently.
	if out.ScannedFiles != 2 {
		t.Fatalf("expected README.md and large.go to be scanned, got %d", out.ScannedFiles)
	}
	if out.Extracted != 2 || out.ChunkedFiles != 1 || out.ExtractedChunks != 3 {
		t.Fatalf("expected one regular and one two-chunk extraction, got %+v", out)
	}
	if out.Skipped < 1 {
		t.Fatalf("expected binary.txt to be skipped, got %d", out.Skipped)
	}

	// Verify errors contain the expected file paths and reasons.
	foundBinary := false
	for _, e := range out.Errors {
		if strings.Contains(e.Path, "binary.txt") && strings.Contains(e.Reason, "binary") {
			foundBinary = true
		}
	}
	if !foundBinary {
		t.Fatalf("expected error for binary.txt, errors: %+v", out.Errors)
	}
}

func TestStudyEngineIgnoresGeneratedDashboardBundlesButChunksLargeSource(t *testing.T) {
	root := t.TempDir()
	assetsDir := filepath.Join(root, "internal", "api", "dashboard", "dist", "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatalf("mkdir dashboard assets: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	largeData := bytes.Repeat([]byte("a"), 300*1024)
	for _, name := range []string{"app.js", "chunk-vendor.js"} {
		if err := os.WriteFile(filepath.Join(assetsDir, name), largeData, 0o644); err != nil {
			t.Fatalf("write generated bundle %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "source.js"), []byte("export const source = true;\n"), 0o644); err != nil {
		t.Fatalf("write ordinary dashboard source: %v", err)
	}
	largeSource := filepath.Join(root, "src", "large.go")
	if err := os.WriteFile(largeSource, largeData, 0o644); err != nil {
		t.Fatalf("write large source: %v", err)
	}

	engine := NewStudyEngine(nil)
	out, err := engine.IngestWithOptions(context.Background(), StudyOptions{
		Workspace: "ws",
		Sources:   []string{root},
		Depth:     "medium",
		DryRun:    true,
		MaxFiles:  10,
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	if out.PageFiles != 2 {
		t.Fatalf("expected ordinary source.js and large.go to be eligible, got %d page files", out.PageFiles)
	}
	if out.ScannedFiles != 2 || out.Extracted != 2 || out.Skipped != 0 || out.ChunkedFiles != 1 || out.ExtractedChunks != 3 {
		t.Fatalf("unexpected result counts: %+v", out)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("expected generated bundles ignored and handwritten source chunked without errors, got %+v", out.Errors)
	}
}

func TestIsGeneratedDashboardBundle(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/repo/internal/api/dashboard/assets/app.js", want: true},
		{path: `/repo/internal/api/dashboard/assets/chunk-katex.js`, want: true},
		{path: `C:\repo\internal\api\dashboard\assets\chunk-vendor.js`, want: true},
		{path: "/repo/internal/api/dashboard/dist/assets/app.js", want: true},
		{path: `/repo/internal/api/dashboard/dist/assets/chunk-katex.js`, want: true},
		{path: `C:\repo\internal\api\dashboard\dist\assets\chunk-vendor.js`, want: true},
		{path: "/repo/internal/api/dashboard/assets/source.js", want: false},
		{path: "/repo/src/chunk-vendor.js", want: false},
		{path: "/repo/internal/api/dashboard/asset/app.js", want: false},
	}
	for _, tt := range tests {
		if got := isGeneratedDashboardBundle(tt.path); got != tt.want {
			t.Errorf("isGeneratedDashboardBundle(%q) = %t, want %t", tt.path, got, tt.want)
		}
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
