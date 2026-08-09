package localevidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildSortsAndValidatesContentFreeReceipts(t *testing.T) {
	root := t.TempDir()
	writeReceipt(t, root, "receipts/zeta.log", "zeta passed\n")
	writeReceipt(t, root, "receipts/alpha.log", "alpha passed\n")

	started := time.Date(2026, 8, 8, 6, 0, 0, 0, time.UTC)
	manifest, err := Build(root, Metadata{
		RunID:       "20260808T060000Z-abcdef123456",
		Profile:     "floci",
		GitCommit:   strings.Repeat("a", 40),
		GitDirty:    true,
		StartedAt:   started,
		CompletedAt: started.Add(time.Minute),
		Checks: []Check{
			{Name: "zeta", Outcome: "passed", Receipt: "receipts/zeta.log"},
			{Name: "alpha", Outcome: "passed", Receipt: "receipts/alpha.log"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != SchemaV1 || manifest.Classification != ClassificationLocalDevelopment || !manifest.Passed {
		t.Fatalf("unexpected manifest identity: %+v", manifest)
	}
	if manifest.Checks[0].Name != "alpha" || manifest.Files[0].Path != "receipts/alpha.log" {
		t.Fatalf("manifest is not deterministic: %+v", manifest)
	}
	if len(manifest.Files) != 2 || len(manifest.Files[0].SHA256) != 64 || manifest.Files[0].Bytes == 0 {
		t.Fatalf("receipt metadata missing: %+v", manifest.Files)
	}
	if err := Validate(root, manifest); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestBuildRejectsFailedDuplicateOrUnsafeChecks(t *testing.T) {
	root := t.TempDir()
	writeReceipt(t, root, "receipts/health.log", "healthy\n")
	base := Metadata{
		RunID:       "20260808T060000Z-abcdef123456",
		Profile:     "floci",
		GitCommit:   strings.Repeat("b", 40),
		StartedAt:   time.Date(2026, 8, 8, 6, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, 8, 8, 6, 1, 0, 0, time.UTC),
	}

	for name, checks := range map[string][]Check{
		"failed": {{Name: "health", Outcome: "failed", Receipt: "receipts/health.log"}},
		"duplicate": {
			{Name: "health", Outcome: "passed", Receipt: "receipts/health.log"},
			{Name: "health", Outcome: "passed", Receipt: "receipts/health.log"},
		},
		"traversal": {{Name: "health", Outcome: "passed", Receipt: "../health.log"}},
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Checks = checks
			if _, err := Build(root, value); err == nil {
				t.Fatal("unsafe manifest input accepted")
			}
		})
	}
}

func TestValidateDetectsReceiptMutationAndSymlinks(t *testing.T) {
	root := t.TempDir()
	writeReceipt(t, root, "receipts/health.log", "healthy\n")
	started := time.Date(2026, 8, 8, 6, 0, 0, 0, time.UTC)
	manifest, err := Build(root, Metadata{
		RunID:       "20260808T060000Z-abcdef123456",
		Profile:     "minio",
		GitCommit:   strings.Repeat("c", 40),
		StartedAt:   started,
		CompletedAt: started.Add(time.Minute),
		Checks:      []Check{{Name: "health", Outcome: "passed", Receipt: "receipts/health.log"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeReceipt(t, root, "receipts/health.log", "changed\n")
	if err := Validate(root, manifest); err == nil {
		t.Fatal("mutated receipt passed validation")
	}

	other := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(other, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "receipts", "health.log")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(root, "receipts", "health.log")); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(root, Metadata{
		RunID:       "20260808T060000Z-abcdef123456",
		Profile:     "minio",
		GitCommit:   strings.Repeat("d", 40),
		StartedAt:   started,
		CompletedAt: started.Add(time.Minute),
		Checks:      []Check{{Name: "health", Outcome: "passed", Receipt: "receipts/health.log"}},
	}); err == nil {
		t.Fatal("symlink receipt accepted")
	}
}

func writeReceipt(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
