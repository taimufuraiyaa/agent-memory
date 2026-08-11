package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/api"
	dashboardassets "github.com/taimufuraiyaa/agent-memory/internal/api/dashboard"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
)

var localHostedDashboardBaseURL = "http://localhost:58081"

const dashboardRuntimeSchema = "agent-memory-dashboard-runtime-v1"

func openInBrowser(url string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("url is required")
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Run()
	default:
		return exec.Command("xdg-open", url).Run()
	}
}

func apiURLForListenerAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://localhost:3210"
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

func waitForHTTP(url string, timeout time.Duration) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("url is required")
	}
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", url, nil)
		res, err := client.Do(req)
		if err == nil {
			_ = res.Body.Close()
			return nil
		}
		time.Sleep(125 * time.Millisecond)
	}
	return errors.New("timeout waiting for server")
}

func discoverHostedDashboard(ctx context.Context, client *http.Client, baseURL string) (string, bool) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || client == nil {
		return "", false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/dashboard/runtime.json", nil)
	if err != nil {
		return "", false
	}
	res, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4097))
	if err != nil || len(body) > 4096 {
		return "", false
	}
	var manifest struct {
		Schema string `json:"schema"`
		Mode   string `json:"mode"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil || manifest.Schema != dashboardRuntimeSchema || manifest.Mode != "hosted" {
		return "", false
	}
	return baseURL + "/dashboard/", true
}

type dashboardPID struct {
	PID       int       `json:"pid"`
	VitePID   int       `json:"vite_pid,omitempty"`
	Workspace string    `json:"workspace"`
	Addr      string    `json:"addr"`
	URL       string    `json:"url"`
	StartedAt time.Time `json:"started_at"`
}

func dashboardPIDPath(cfg runtimeConfig) string {
	base := filepath.Dir(cfg.dbPath)
	name := "dashboard.pid"
	if ws := strings.TrimSpace(cfg.workspace); ws != "" {
		name = fmt.Sprintf("dashboard.%s.pid", ws)
	}
	return filepath.Join(base, name)
}

func dashboardPIDPaths(cfg runtimeConfig, pidFile string) []string {
	if explicit := strings.TrimSpace(pidFile); explicit != "" {
		return []string{explicit}
	}
	paths := make([]string, 0, 2)
	seen := map[string]struct{}{}
	add := func(path string) {
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	add(dashboardPIDPath(cfg))
	if strings.TrimSpace(cfg.workspace) != "" {
		add(filepath.Join(filepath.Dir(cfg.dbPath), "dashboard.pid"))
	}
	return paths
}

func inferDashboardPIDByAddr(addr string) (dashboardPID, error) {
	pid, err := listenerPIDForAddr(addr)
	if err != nil {
		return dashboardPID{}, err
	}
	cmdline, err := processCommandLine(pid)
	if err != nil {
		return dashboardPID{}, err
	}
	if !looksLikeDashboardCommandLine(cmdline) {
		return dashboardPID{}, fmt.Errorf("listener on %s is not an agent-memory dashboard process", addr)
	}
	return dashboardPID{
		PID:  pid,
		Addr: addr,
	}, nil
}

func listenerPIDForAddr(addr string) (int, error) {
	port, err := listenerPort(addr)
	if err != nil {
		return 0, err
	}
	if runtime.GOOS == "windows" {
		return 0, errors.New("listener pid fallback is unsupported on windows")
	}
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err == nil && pid > 0 {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("no listening pid found for %s", addr)
}

func listenerPort(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		_ = host
		if strings.TrimSpace(port) == "" || port == "0" {
			return "", fmt.Errorf("invalid addr: %s", addr)
		}
		return port, nil
	}
	if strings.HasPrefix(addr, ":") && len(addr) > 1 {
		return strings.TrimPrefix(addr, ":"), nil
	}
	return "", fmt.Errorf("invalid addr: %s", addr)
}

func processCommandLine(pid int) (string, error) {
	if pid <= 0 {
		return "", errors.New("pid is required")
	}
	if runtime.GOOS == "windows" {
		return "", errors.New("process command lookup is unsupported on windows")
	}
	out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func looksLikeDashboardCommandLine(cmdline string) bool {
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return false
	}
	return strings.Contains(cmdline, "agent-memory") && strings.Contains(cmdline, " dashboard ")
}

func readDashboardPID(path string) (dashboardPID, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return dashboardPID{}, err
	}
	var out dashboardPID
	if err := json.Unmarshal(b, &out); err == nil && out.PID > 0 {
		return out, nil
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid); err == nil && pid > 0 {
		return dashboardPID{PID: pid}, nil
	}
	return dashboardPID{}, errors.New("invalid dashboard pid file")
}

func writeDashboardPID(path string, v dashboardPID) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := p.Signal(syscall.Signal(0)); err == nil {
		return true
	}
	return false
}

func stopProcess(pid int) error {
	if pid <= 0 {
		return errors.New("pid is required")
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = p.Signal(syscall.SIGTERM)
		time.Sleep(250 * time.Millisecond)
		if isProcessAlive(pid) {
			_ = p.Kill()
		}
		return nil
	}
	if err := p.Kill(); err != nil {
		return err
	}
	return nil
}

func validateDashboardStartAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") && len(addr) > 1 && addr != ":0" {
			return nil
		}
		_ = host
		return fmt.Errorf("invalid addr: %s", addr)
	}
	if port == "0" {
		return errors.New("addr cannot use port 0 with --start (pick a fixed port)")
	}
	return nil
}

func buildDashboardProcessArgs(cfg runtimeConfig, addr string, dashDirFlag string, pidFile string) []string {
	args := []string{
		"dashboard",
		"--no-open",
		"--addr", addr,
		"--db", cfg.dbPath,
		"--model-dir", cfg.modelDir,
	}
	if ws := strings.TrimSpace(cfg.workspace); ws != "" {
		args = append(args, "--workspace", ws)
	}
	if strings.TrimSpace(dashDirFlag) != "" {
		args = append(args, "--dashboard-dir", dashDirFlag)
	}
	if strings.TrimSpace(pidFile) != "" {
		args = append(args, "--pid-file", pidFile)
	}
	return args
}

func startDashboardProcess(cfg runtimeConfig, addr string, dashDirFlag string, pidFile string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	args := buildDashboardProcessArgs(cfg, addr, dashDirFlag, pidFile)
	c := exec.Command(exe, args...)
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	configureBackgroundProcess(c)
	if err := c.Start(); err != nil {
		return 0, err
	}
	pid := c.Process.Pid
	if err := c.Process.Release(); err != nil {
		_ = c.Process.Kill()
		return 0, err
	}
	return pid, nil
}

func pickFreeLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, err
	}
	if port <= 0 {
		return 0, errors.New("failed to allocate a free port")
	}
	return port, nil
}

func resolveDashboardRuntime(flags commonFlags) (runtimeConfig, error) {
	modelDir := strings.TrimSpace(flags.modelDir)
	if modelDir == "" {
		home, _ := os.UserHomeDir()
		modelDir = embeddings.DefaultModelDir(home)
	}
	apiURL := resolveAPIURL(flags.apiURL)
	dbPath := strings.TrimSpace(flags.dbPath)
	workspace, err := resolveWorkspace(flags.workspace)
	if err == nil {
		if dbPath == "" {
			dbPath, err = defaultDBPath(workspace)
			if err != nil {
				return runtimeConfig{}, err
			}
		}
		return runtimeConfig{
			workspace: workspace,
			dbPath:    dbPath,
			modelDir:  modelDir,
			apiURL:    apiURL,
		}, nil
	}
	if dbPath == "" {
		baseDir, baseErr := defaultDBBaseDir()
		if baseErr != nil {
			return runtimeConfig{}, baseErr
		}
		dbPath = filepath.Join(baseDir, ".dashboard-placeholder.db")
	}
	return runtimeConfig{
		workspace: "",
		dbPath:    dbPath,
		modelDir:  modelDir,
		apiURL:    apiURL,
	}, nil
}

func dashboardSourceDir(override string) (string, error) {
	if v := strings.TrimSpace(override); v != "" {
		dir := v
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return "", fmt.Errorf("dashboard dir not found: %s", dir)
		}
		if !fileExists(filepath.Join(dir, "package.json")) {
			return "", errors.New("dashboard package.json is missing")
		}
		return dir, nil
	}
	if v := strings.TrimSpace(os.Getenv("AGENT_MEMORY_DASHBOARD_DIR")); v != "" {
		return dashboardSourceDir(v)
	}
	cwd, err := os.Getwd()
	var root string
	if err == nil {
		root = findSourceRoot(cwd)
	}
	if strings.TrimSpace(root) == "" {
		if exe, err := os.Executable(); err == nil {
			root = findSourceRoot(filepath.Dir(exe))
		}
	}
	if strings.TrimSpace(root) == "" {
		return "", errors.New("standalone dashboard sources not found (run from the repository)")
	}
	dir := filepath.Join(root, "tools", "agent-memory", "dashboard")
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", errors.New("standalone dashboard directory is missing (tools/agent-memory/dashboard)")
	}
	if !fileExists(filepath.Join(dir, "package.json")) {
		return "", errors.New("standalone dashboard package.json is missing")
	}
	return dir, nil
}

func tryServeEmbeddedDashboard(cmd *cobra.Command, ctx context.Context, cfg runtimeConfig, ln net.Listener, apiURL string, noOpen bool, pidFile string) error {
	// Import the dashboard package to access embedded assets
	embeddedDashboard := &embeddedDashboardWrapper{}

	if !embeddedDashboard.hasAssets() {
		return errors.New("embedded assets not available")
	}

	// Log that we're using embedded assets
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "using embedded dashboard assets (npm not required)\n")

	// Get the dashboard URL - embedded assets are served from the API server
	dashURL := strings.TrimRight(apiURL, "/") + "/dashboard/"

	if strings.TrimSpace(pidFile) != "" {
		if err := writeDashboardPID(pidFile, dashboardPID{
			PID:       os.Getpid(),
			Workspace: cfg.workspace,
			Addr:      ln.Addr().String(),
			URL:       dashURL,
			StartedAt: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("write dashboard start handshake: %w", err)
		}
		defer func() { _ = os.Remove(pidFile) }()
	}

	if noOpen {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", dashURL)
	} else {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "opening %s\n", dashURL)
		_ = openInBrowser(dashURL)
	}

	// Wait for cancellation
	<-ctx.Done()
	return nil
}

type embeddedDashboardWrapper struct{}

func (w *embeddedDashboardWrapper) hasAssets() bool {
	return dashboardassets.HasEmbeddedAssets()
}

func newDashboardCommand() *cobra.Command {
	var flags commonFlags
	var addr string
	var noOpen bool
	var start bool
	var stop bool
	var dashDirFlag string
	var pidFile string
	var status bool
	cmd := &cobra.Command{
		Use:     "dashboard",
		Short:   "Open the Agent Memory webapp (reuses Floci or starts a local fallback)",
		Aliases: []string{"ui"},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			cfg, err := resolveDashboardRuntime(flags)
			if err != nil {
				return err
			}
			pidPaths := dashboardPIDPaths(cfg, pidFile)
			pidPath := pidPaths[0]
			if status {
				for _, candidate := range pidPaths {
					v, err := readDashboardPID(candidate)
					if err != nil || !isProcessAlive(v.PID) {
						continue
					}
					url := strings.TrimSpace(v.URL)
					return writeSuccessEnvelope(cmd.OutOrStdout(), "dashboard-status", map[string]any{
						"running":   true,
						"healthy":   url != "",
						"pid":       v.PID,
						"vite_pid":  v.VitePID,
						"workspace": v.Workspace,
						"addr":      v.Addr,
						"url":       url,
					})
				}
				if v, err := inferDashboardPIDByAddr(addr); err == nil && isProcessAlive(v.PID) {
					return writeSuccessEnvelope(cmd.OutOrStdout(), "dashboard-status", map[string]any{
						"running": true,
						"healthy": true,
						"pid":     v.PID,
						"addr":    v.Addr,
					})
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "dashboard-status", map[string]any{
					"running": false,
					"healthy": false,
				})
			}
			if cfg.apiURL != "" {
				url := strings.TrimRight(cfg.apiURL, "/") + "/dashboard/"
				if noOpen {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", url)
					return nil
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "opening %s\n", url)
				return openInBrowser(url)
			}
			if start && stop {
				return errors.New("only one of --start or --stop can be set")
			}
			if start {
				client := &http.Client{
					Timeout: 750 * time.Millisecond,
					CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
						return http.ErrUseLastResponse
					},
				}
				if url, ok := discoverHostedDashboard(ctx, client, localHostedDashboardBaseURL); ok {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "reusing running Agent Memory webapp")
					if noOpen {
						_, _ = fmt.Fprintln(cmd.OutOrStdout(), url)
						return nil
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "opening %s\n", url)
					return openInBrowser(url)
				}
			}
			if stop {
				var (
					v      dashboardPID
					stopOK bool
				)
				for _, candidate := range pidPaths {
					var readErr error
					v, readErr = readDashboardPID(candidate)
					if readErr != nil {
						continue
					}
					pidPath = candidate
					stopOK = true
					break
				}
				if !stopOK {
					inferred, inferErr := inferDashboardPIDByAddr(addr)
					if inferErr != nil {
						return errors.New("Dashboard was closed!")
					}
					v = inferred
				}
				if v.VitePID > 0 {
					_ = stopProcess(v.VitePID)
				}
				if v.PID > 0 {
					_ = stopProcess(v.PID)
				}
				_ = os.Remove(pidPath)
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "stopped dashboard (pid=%d)\n", v.PID)
				return nil
			}
			if start {
				if err := validateDashboardStartAddr(addr); err != nil {
					return err
				}
				for _, candidate := range pidPaths {
					if v, err := readDashboardPID(candidate); err == nil && isProcessAlive(v.PID) {
						url := strings.TrimSpace(v.URL)
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dashboard already running (pid=%d)\n", v.PID)
						if noOpen {
							if url != "" {
								_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", url)
							}
							return nil
						}
						if url != "" {
							_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "opening %s\n", url)
							return openInBrowser(url)
						}
						return nil
					}
				}
				if v, err := inferDashboardPIDByAddr(addr); err == nil && isProcessAlive(v.PID) {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dashboard already running (pid=%d)\n", v.PID)
					return nil
				}
				for _, candidate := range pidPaths {
					_ = os.Remove(candidate)
				}

				if ln, err := net.Listen("tcp", addr); err == nil {
					_ = ln.Close()
				} else {
					return fmt.Errorf("cannot start dashboard: address %s is already in use by another process", addr)
				}

				pid, err := startDashboardProcess(cfg, addr, dashDirFlag, pidPath)
				if err != nil {
					return err
				}

				url := ""
				for i := 0; i < 120; i++ {
					time.Sleep(125 * time.Millisecond)
					v, err := readDashboardPID(pidPath)
					if err != nil {
						continue
					}
					if !isProcessAlive(v.PID) {
						break
					}
					if strings.TrimSpace(v.URL) != "" {
						url = strings.TrimSpace(v.URL)
						break
					}
				}

				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "started dashboard (pid=%d)\n", pid)
				if url == "" {
					return errors.New("dashboard failed to start (no URL reported)")
				}
				if noOpen {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", url)
					return nil
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "opening %s\n", url)
				_ = openInBrowser(url)
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(cfg.dbPath), 0o755); err != nil {
				return err
			}
			if err := os.MkdirAll(cfg.modelDir, 0o755); err != nil {
				return err
			}
			provider, err := embeddings.NewProvider(cfg.modelDir)
			if err != nil {
				return err
			}

			svc := &api.Service{
				Workspace:         cfg.workspace,
				BaseDir:           filepath.Dir(cfg.dbPath),
				EmbeddingProvider: provider,
			}
			if err := api.ConfigureLocalRightsAttestation(ctx, svc); err != nil {
				return err
			}
			if err := api.ConfigureLocalClientProfiles(svc); err != nil {
				return err
			}
			if err := api.ConfigureLocalDeploymentProfile(svc); err != nil {
				return err
			}
			defer func() { _ = svc.Close() }()
			server := &http.Server{
				Addr:    addr,
				Handler: api.NewMux(svc),
			}

			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			errCh := make(chan error, 1)
			go func() { errCh <- server.Serve(ln) }()

			apiURL := apiURLForListenerAddr(ln.Addr().String())
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "api serving on %s\n", apiURL)

			// Try embedded dashboard assets first (no npm required)
			if err := tryServeEmbeddedDashboard(cmd, ctx, cfg, ln, apiURL, noOpen, pidFile); err == nil {
				return nil
			}

			// Fall back to npm-based development mode
			dashDir, err := dashboardSourceDir(dashDirFlag)
			if err != nil {
				_ = server.Shutdown(context.Background())
				return err
			}
			if _, err := exec.LookPath("npm"); err != nil {
				_ = server.Shutdown(context.Background())
				return errors.New("npm is required to run the standalone dashboard (embedded assets not available)")
			}
			port, err := pickFreeLocalPort()
			if err != nil {
				_ = server.Shutdown(context.Background())
				return err
			}
			dashURL := fmt.Sprintf("http://127.0.0.1:%d/", port)
			vite := exec.CommandContext(ctx, "npm", "run", "dev", "--", "--host", "127.0.0.1", "--port", fmt.Sprintf("%d", port), "--strictPort")
			vite.Dir = dashDir
			vite.Env = append(os.Environ(), "VITE_API_TARGET="+apiURL)
			vite.Stdout = cmd.ErrOrStderr()
			vite.Stderr = cmd.ErrOrStderr()
			if err := vite.Start(); err != nil {
				_ = server.Shutdown(context.Background())
				return err
			}
			viteCh := make(chan error, 1)
			go func() { viteCh <- vite.Wait() }()

			if err := waitForHTTP(dashURL, 4*time.Second); err != nil {
				_ = server.Shutdown(context.Background())
				_ = ln.Close()
				_ = vite.Process.Kill()
				return err
			}

			if strings.TrimSpace(pidFile) != "" {
				_ = writeDashboardPID(pidPath, dashboardPID{
					PID:       os.Getpid(),
					VitePID:   vite.Process.Pid,
					Workspace: cfg.workspace,
					Addr:      addr,
					URL:       dashURL,
					StartedAt: time.Now().UTC(),
				})
				defer func() { _ = os.Remove(pidPath) }()
			}

			if noOpen {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", dashURL)
			} else {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "opening %s\n", dashURL)
				_ = openInBrowser(dashURL)
			}

			select {
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = server.Shutdown(shutdownCtx)
				cancel()
				_ = ln.Close()
				_ = vite.Process.Kill()
				return nil
			case err := <-viteCh:
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				_ = server.Shutdown(shutdownCtx)
				cancel()
				_ = ln.Close()
				if err != nil {
					return err
				}
				return nil
			case err := <-errCh:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				_ = vite.Process.Kill()
				return err
			}
		},
	}
	addCommonFlags(cmd, &flags)
	_ = cmd.Flags().MarkHidden("workspace")
	cmd.Flags().StringVar(&addr, "addr", ":3210", "HTTP listen address")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "Do not open a browser; just print the URL")
	cmd.Flags().BoolVar(&start, "start", false, "Start dashboard server in the background and exit")
	cmd.Flags().BoolVar(&stop, "stop", false, "Stop the background dashboard server (started via --start)")
	cmd.Flags().BoolVar(&status, "status", false, "Show background dashboard server status")
	cmd.Flags().StringVar(&dashDirFlag, "dashboard-dir", "", "Path to standalone dashboard folder (tools/agent-memory/dashboard)")
	cmd.Flags().StringVar(&pidFile, "pid-file", "", "Internal: pid file path")
	_ = cmd.Flags().MarkHidden("pid-file")
	return cmd
}
