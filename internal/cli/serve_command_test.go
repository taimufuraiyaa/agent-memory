package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestBuildServeProcessArgsOmitsWorkspaceWhenEmpty(t *testing.T) {
	cfg := runtimeConfig{
		dbPath:   "/tmp/serve.db",
		modelDir: "/tmp/models",
	}
	args := buildServeProcessArgs(cfg, ":3210", "")
	for i := 0; i < len(args); i++ {
		if args[i] == "--workspace" {
			t.Fatalf("expected serve start args to omit --workspace when empty: %v", args)
		}
	}
}

func TestResolveServeRuntimeAllowsMissingWorkspace(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Setenv("HOME", home)
	t.Setenv("MEMORY_WORKSPACE", "")

	cfg, err := resolveServeRuntime(commonFlags{})
	if err != nil {
		t.Fatalf("resolveServeRuntime: %v", err)
	}
	if cfg.workspace != "" {
		t.Fatalf("expected empty workspace fallback, got %q", cfg.workspace)
	}
	wantDir := filepath.Join(home, ".agent-memory")
	if got := filepath.Dir(cfg.dbPath); got != wantDir {
		t.Fatalf("expected serve base dir %q, got %q", wantDir, got)
	}
}

func TestResolveServeRuntimeDoesNotBindDaemonToCurrentWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg, err := resolveServeRuntime(commonFlags{workspace: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.workspace != "" {
		t.Fatalf("daemon bound to workspace %q", cfg.workspace)
	}
	if got, want := filepath.Dir(cfg.dbPath), filepath.Join(home, ".agent-memory"); got != want {
		t.Fatalf("base dir=%q want %q", got, want)
	}
}

func TestServePIDPathIsGlobalEvenWhenStartedFromWorkspace(t *testing.T) {
	cfg := runtimeConfig{workspace: "alpha", dbPath: filepath.Join(t.TempDir(), "alpha.db")}
	if got, want := filepath.Base(servePIDPath(cfg)), "serve.pid"; got != want {
		t.Fatalf("serve PID must be daemon-global: got %q want %q", got, want)
	}
}

func TestServeAndDoctorUseSameCanonicalPort(t *testing.T) {
	if defaultServeAddr != "127.0.0.1:3211" {
		t.Fatalf("serve addr=%q", defaultServeAddr)
	}
	if defaultServiceURL != "http://127.0.0.1:3211" {
		t.Fatalf("doctor URL=%q", defaultServiceURL)
	}
}

func TestValidateLocalListenAddrRejectsNonLoopback(t *testing.T) {
	for _, addr := range []string{":3211", "0.0.0.0:3211", "[::]:3211", "192.0.2.10:3211"} {
		if err := validateLocalListenAddr(addr); err == nil {
			t.Fatalf("expected non-loopback address %q to fail", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:3211", "localhost:3211", "[::1]:3211"} {
		if err := validateLocalListenAddr(addr); err != nil {
			t.Fatalf("expected loopback address %q to pass: %v", addr, err)
		}
	}
}

func TestLegacyServePIDPathsExcludeGlobalDaemonPID(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"serve.pid", "serve.alpha.pid", "serve.beta.pid", "other.pid"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte("1"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := legacyServePIDPaths(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || filepath.Base(paths[0]) != "serve.alpha.pid" || filepath.Base(paths[1]) != "serve.beta.pid" {
		t.Fatalf("legacy paths=%v", paths)
	}
}

func TestWallClockAnchorNextAfter(t *testing.T) {
	loc := time.FixedZone("UTC+2", 2*60*60)
	anchor := wallClockAnchor{
		hour:       9,
		minute:     30,
		second:     0,
		nanosecond: 0,
		location:   loc,
	}

	now := time.Date(2026, 6, 8, 8, 0, 0, 0, loc)
	next := anchor.NextAfter(now)
	if !next.Equal(time.Date(2026, 6, 8, 9, 30, 0, 0, loc)) {
		t.Fatalf("expected same-day next tick, got %s", next)
	}

	now = time.Date(2026, 6, 8, 10, 0, 0, 0, loc)
	next = anchor.NextAfter(now)
	if !next.Equal(time.Date(2026, 6, 9, 9, 30, 0, 0, loc)) {
		t.Fatalf("expected next-day wall clock tick, got %s", next)
	}
}

func TestEvaluateSchedulerDecision(t *testing.T) {
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	recentSummary := &sqlite.WorkspaceActivitySummary{
		Workspace:      "ws",
		MemoryCount:    1,
		LastUpdatedAt:  now.Add(-2 * time.Hour),
		LastAccessedAt: now.Add(-time.Hour),
	}
	quietSummary := &sqlite.WorkspaceActivitySummary{
		Workspace:      "ws",
		MemoryCount:    1,
		LastUpdatedAt:  now.Add(-48 * time.Hour),
		LastAccessedAt: now.Add(-48 * time.Hour),
	}
	recentState := &sqlite.SchedulerWorkspaceState{
		Workspace:       "ws",
		LastCompletedAt: now.Add(-48 * time.Hour),
	}
	staleState := &sqlite.SchedulerWorkspaceState{
		Workspace:       "ws",
		LastCompletedAt: now.Add(-8 * 24 * time.Hour),
	}

	if got := evaluateSchedulerDecision(schedulerTriggerDaily, recentSummary, recentState, now, false); !got.eligible {
		t.Fatalf("expected daily tick to run for recently active workspace, got %+v", got)
	}
	if got := evaluateSchedulerDecision(schedulerTriggerDaily, quietSummary, recentState, now, false); got.eligible || got.skipReason != schedulerSkipInactiveToday {
		t.Fatalf("expected quiet workspace daily skip, got %+v", got)
	}
	if got := evaluateSchedulerDecision(schedulerTriggerDaily, quietSummary, staleState, now, false); !got.eligible || !got.hygieneOverdue {
		t.Fatalf("expected stale workspace hygiene run, got %+v", got)
	}
	if got := evaluateSchedulerDecision(schedulerTriggerStartup, quietSummary, recentState, now, false); got.eligible {
		t.Fatalf("expected startup catch-up to skip non-overdue workspace, got %+v", got)
	}
	if got := evaluateSchedulerDecision(schedulerTriggerStartup, quietSummary, staleState, now, false); !got.eligible {
		t.Fatalf("expected startup catch-up to run overdue workspace, got %+v", got)
	}
	if got := evaluateSchedulerDecision(schedulerTriggerDaily, &sqlite.WorkspaceActivitySummary{Workspace: "ws"}, nil, now, false); got.eligible || got.skipReason != schedulerSkipNoMemories {
		t.Fatalf("expected no-memories skip, got %+v", got)
	}
}

func TestServeSchedulerAcquireWorkspacePreventsOverlap(t *testing.T) {
	scheduler := &serveScheduler{
		runningByWS: map[string]time.Time{},
	}
	startedAt := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	if ok := scheduler.acquireWorkspace("ws", startedAt); !ok {
		t.Fatalf("expected first acquire to succeed")
	}
	if ok := scheduler.acquireWorkspace("ws", startedAt.Add(time.Second)); ok {
		t.Fatalf("expected overlapping acquire to fail")
	}
	scheduler.releaseWorkspace("ws")
	if ok := scheduler.acquireWorkspace("ws", startedAt.Add(2*time.Second)); !ok {
		t.Fatalf("expected acquire after release to succeed")
	}
}

func TestServeSchedulerWorkspaceDBPathUsesWorkspaceDBForPlaceholder(t *testing.T) {
	scheduler := &serveScheduler{
		cfg: runtimeConfig{
			dbPath: filepath.Join("/tmp", ".dashboard-placeholder.db"),
		},
	}

	got := scheduler.workspaceDBPath(workspace.ListItem{
		Name:   "agent-memory",
		DBPath: filepath.Join("/tmp", ".dashboard-placeholder.db"),
	})
	want := filepath.Join("/tmp", "agent-memory.db")
	if got != want {
		t.Fatalf("expected placeholder db path to resolve to workspace db %q, got %q", want, got)
	}

	custom := scheduler.workspaceDBPath(workspace.ListItem{
		Name:   "agent-memory",
		DBPath: filepath.Join("/tmp", "custom.db"),
	})
	if custom != filepath.Join("/tmp", "custom.db") {
		t.Fatalf("expected explicit custom db path to remain unchanged, got %q", custom)
	}
}
