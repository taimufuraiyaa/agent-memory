package integration

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

func TestSQLiteSkillStandaloneRuntimeKeepsMultipleWorkspaceCyclesResponsive(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "standalone.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	worker := &integrationWorkspaceCycles{workspaces: []string{"workspace-a", "workspace-b"}}
	reconciler := &integrationReconciler{}
	runtime, err := application.NewSkillStandaloneRuntime(store, worker, reconciler, application.SkillStandaloneRuntimeConfig{
		Enabled: true, InstallationID: "installation", DatabaseID: "standalone-db", Owner: "service",
		PollInterval: 10 * time.Millisecond, ReconciliationInterval: 15 * time.Millisecond,
		LeaderLeaseDuration: time.Second, LeaderRenewalInterval: 20 * time.Millisecond, DrainTimeout: time.Second,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runtime.Run(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && worker.total() < 2 {
		time.Sleep(time.Millisecond)
	}
	if worker.total() < 2 {
		t.Fatal("multiple workspace cycles did not run")
	}
	queryCtx, queryCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer queryCancel()
	if _, err := store.ListLogicalSkills(queryCtx, "workspace-a", 1); err != nil {
		t.Fatalf("SQLite became unresponsive during runtime: %v", err)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := runtime.Shutdown(shutdown); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

type integrationWorkspaceCycles struct {
	mu         sync.Mutex
	workspaces []string
	counts     map[string]int
}

func (w *integrationWorkspaceCycles) RunOnce(context.Context) (application.SkillWorkerRunReport, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.counts == nil {
		w.counts = map[string]int{}
	}
	for _, workspace := range w.workspaces {
		w.counts[workspace]++
	}
	return application.SkillWorkerRunReport{}, nil
}

func (w *integrationWorkspaceCycles) total() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.counts)
}

type integrationReconciler struct{}

func (*integrationReconciler) RunOnce(context.Context) (application.SkillReconciliationReport, error) {
	return application.SkillReconciliationReport{}, nil
}
