package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadRegularJSONRejectsPathReplacedBySymlinkToOpenedFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "input.json")
	originalPath := filepath.Join(directory, "opened.json")
	if err := os.WriteFile(path, []byte(`{"value":"expected"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var destination struct {
		Value string `json:"value"`
	}
	err := readRegularJSONWithHook(path, &destination, func() {
		if err := os.Rename(path, originalPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(originalPath, path); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("JSON input whose path became a symlink was accepted")
	}
}

func TestRunBuildsAndVerifiesLocalEvidenceManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "receipts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "receipts", "health.log"), []byte("healthy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := `{
  "run_id":"20260808T060000Z-abcdef123456",
  "profile":"floci",
  "git_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "git_dirty":true,
  "started_at":"2026-08-08T06:00:00Z",
  "completed_at":"2026-08-08T06:01:00Z",
  "checks":[{"name":"health","outcome":"passed","receipt":"receipts/health.log"}]
}`
	metadataPath := filepath.Join(root, "metadata.json")
	if err := os.WriteFile(metadataPath, []byte(metadata), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if exit := run([]string{"--root", root, "--metadata", metadataPath}, &stdout, &stderr); exit != 0 {
		t.Fatalf("build exit=%d stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"schema":"agent-memory-local-alpha-evidence-v1"`) || !strings.Contains(stdout.String(), `"passed":true`) {
		t.Fatalf("unexpected manifest: %s", stdout.String())
	}
	manifestPath := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifestPath, stdout.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if exit := run([]string{"--root", root, "--verify", manifestPath}, &stdout, &stderr); exit != 0 || strings.TrimSpace(stdout.String()) != "local evidence manifest verified" {
		t.Fatalf("verify exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	if err := os.WriteFile(filepath.Join(root, "receipts", "health.log"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if exit := run([]string{"--root", root, "--verify", manifestPath}, &stdout, &stderr); exit == 0 {
		t.Fatal("modified evidence verified")
	}
}

func TestRunRejectsAmbiguousModeAndOversizedMetadata(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exit := run(nil, &stdout, &stderr); exit != 2 {
		t.Fatalf("missing mode exit=%d", exit)
	}
	root := t.TempDir()
	metadataPath := filepath.Join(root, "metadata.json")
	if err := os.WriteFile(metadataPath, bytes.Repeat([]byte("x"), (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if exit := run([]string{"--root", root, "--metadata", metadataPath}, &stdout, &stderr); exit == 0 {
		t.Fatal("oversized metadata accepted")
	}
}
