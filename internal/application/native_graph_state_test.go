package application

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestNativeGraphStateVerifiesCopyAndPrunesOnlyExpiredUnprotectedState(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultGraphConfig(root)
	worker := NewNativeGraphWorker(cfg)
	scope := core.GraphScope{WorkspaceID: "state-workspace"}
	now := time.Now().UTC()
	worker.now = func() time.Time { return now.Add(-25 * time.Hour) }
	if err := os.MkdirAll(cfg.JobRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	directories := map[string]string{}
	for _, revision := range []string{"active", "previous", "expired"} {
		dir, err := os.MkdirTemp(cfg.JobRoot, ".graph-job-")
		if err != nil {
			t.Fatal(err)
		}
		directories[revision] = dir
		output := filepath.Join(dir, "output", "normalized")
		if err = os.MkdirAll(output, 0o700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "output", "entities.parquet"), []byte("raw tables "+revision), 0o600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(output, "artifact-manifest.json"), []byte(`{"revision_id":"`+revision+`","status":"completed"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err = worker.saveState(scope, revision, dir, "sha256:build"); err != nil {
			t.Fatal(err)
		}
	}
	base, err := worker.loadState(scope, "active", "sha256:build")
	if err != nil {
		t.Fatal(err)
	}
	copyDir, err := os.MkdirTemp(cfg.JobRoot, ".graph-job-")
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"native_base_job_dir": base}
	if err = prepareNativeGraphBase(copyDir, base, payload, cfg.MaxDiskBytes); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(copyDir, "output", "entities.parquet"))
	if err != nil || string(data) != "raw tables active" {
		t.Fatalf("base tables missing: %s %v", data, err)
	}
	if payload["base_manifest_sha256"] == "" || payload["native_base_job_dir"] != nil {
		t.Fatalf("base binding not prepared: %+v", payload)
	}
	worker.now = func() time.Time { return now }
	if err = worker.pruneStates(scope, map[string]bool{"active": true, "previous": true}); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []string{"active", "previous"} {
		if _, err = os.Stat(directories[revision]); err != nil {
			t.Fatalf("protected state removed: %s %v", revision, err)
		}
	}
	if _, err = os.Stat(directories["expired"]); !os.IsNotExist(err) {
		t.Fatalf("expired state retained: %v", err)
	}
	if err = os.WriteFile(filepath.Join(base, "output", "entities.parquet"), []byte("modified raw tables"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = worker.loadState(scope, "active", "sha256:build"); err == nil {
		t.Fatal("modified base accepted")
	}
}
