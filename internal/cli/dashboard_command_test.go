package cli

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestBuildDashboardProcessArgsOmitsWorkspaceWhenEmpty(t *testing.T) {
	cfg := runtimeConfig{
		dbPath:   "/tmp/dashboard.db",
		modelDir: "/tmp/models",
	}
	args := buildDashboardProcessArgs(cfg, ":3210", "", "")
	for i := 0; i < len(args); i++ {
		if args[i] == "--workspace" {
			t.Fatalf("expected dashboard start args to omit --workspace when empty: %v", args)
		}
	}
}

func TestResolveDashboardRuntimeAllowsMissingWorkspace(t *testing.T) {
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

	cfg, err := resolveDashboardRuntime(commonFlags{})
	if err != nil {
		t.Fatalf("resolveDashboardRuntime: %v", err)
	}
	if cfg.workspace != "" {
		t.Fatalf("expected empty workspace fallback, got %q", cfg.workspace)
	}
	wantDir := filepath.Join(home, ".agent-memory")
	if got := filepath.Dir(cfg.dbPath); got != wantDir {
		t.Fatalf("expected dashboard base dir %q, got %q", wantDir, got)
	}
	if got := filepath.Base(cfg.dbPath); got != ".dashboard-placeholder.db" {
		t.Fatalf("expected placeholder db path, got %q", got)
	}
}

func TestDashboardPIDPathsFallbackOrder(t *testing.T) {
	cfg := runtimeConfig{
		workspace: "agent-memory",
		dbPath:    filepath.Join("/tmp", ".dashboard-placeholder.db"),
	}
	got := dashboardPIDPaths(cfg, "")
	want := []string{
		filepath.Join("/tmp", "dashboard.agent-memory.pid"),
		filepath.Join("/tmp", "dashboard.pid"),
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d pid paths, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected pid path %d to be %q, got %q", i, want[i], got[i])
		}
	}
}

func TestDashboardPIDPathsUsesExplicitPath(t *testing.T) {
	cfg := runtimeConfig{
		workspace: "agent-memory",
		dbPath:    filepath.Join("/tmp", ".dashboard-placeholder.db"),
	}
	got := dashboardPIDPaths(cfg, "/tmp/custom-dashboard.pid")
	if len(got) != 1 || got[0] != "/tmp/custom-dashboard.pid" {
		t.Fatalf("expected explicit pid path only, got %v", got)
	}
}

func TestListenerPort(t *testing.T) {
	tests := []struct {
		addr string
		want string
		ok   bool
	}{
		{addr: ":3210", want: "3210", ok: true},
		{addr: "127.0.0.1:3210", want: "3210", ok: true},
		{addr: "bad-addr", ok: false},
	}
	for _, tt := range tests {
		got, err := listenerPort(tt.addr)
		if tt.ok && err != nil {
			t.Fatalf("listenerPort(%q) unexpected error: %v", tt.addr, err)
		}
		if !tt.ok && err == nil {
			t.Fatalf("listenerPort(%q) expected error", tt.addr)
		}
		if tt.ok && got != tt.want {
			t.Fatalf("listenerPort(%q) = %q, want %q", tt.addr, got, tt.want)
		}
	}
}

func TestLooksLikeDashboardCommandLine(t *testing.T) {
	if !looksLikeDashboardCommandLine("/path/agent-memory dashboard --no-open --addr :3210") {
		t.Fatalf("expected dashboard command line to match")
	}
	if looksLikeDashboardCommandLine("/path/agent-memory serve --no-open --addr :3211") {
		t.Fatalf("expected serve command line not to match dashboard detector")
	}
}

func TestEmbeddedDashboardWrapperUsesBundledAssets(t *testing.T) {
	if !(&embeddedDashboardWrapper{}).hasAssets() {
		t.Fatal("expected embedded dashboard assets to be available")
	}
}

func TestEmbeddedDashboardPublishesBackgroundStartHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pidPath := filepath.Join(t.TempDir(), "dashboard.pid")
	cfg := runtimeConfig{workspace: "agent-memory"}
	cmd := &cobra.Command{}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	done := make(chan error, 1)
	go func() {
		done <- tryServeEmbeddedDashboard(
			cmd,
			ctx,
			cfg,
			ln,
			"http://127.0.0.1:3210",
			true,
			pidPath,
		)
	}()

	var pid dashboardPID
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		pid, err = readDashboardPID(pidPath)
		if err == nil && pid.URL != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("read handshake: %v", err)
	}
	if pid.PID != os.Getpid() {
		t.Fatalf("expected child pid %d, got %d", os.Getpid(), pid.PID)
	}
	if pid.URL != "http://127.0.0.1:3210/dashboard/" {
		t.Fatalf("expected dashboard URL in handshake, got %q", pid.URL)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serve embedded dashboard: %v", err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Fatalf("expected handshake file removal after shutdown, got %v", err)
	}
}

func TestDashboardStopWhenNotRunning(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cmd := newDashboardCommand()
	cmd.SetArgs([]string{"--stop", "--addr", ":9999"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if err.Error() != "Dashboard was closed!" {
		t.Fatalf("expected error %q, got %q", "Dashboard was closed!", err.Error())
	}
}
