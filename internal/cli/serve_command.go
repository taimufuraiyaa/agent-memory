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
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/api"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type servePID struct {
	PID       int       `json:"pid"`
	Workspace string    `json:"workspace"`
	Addr      string    `json:"addr"`
	URL       string    `json:"url"`
	StartedAt time.Time `json:"started_at"`
}

type serveStatus struct {
	Running   bool      `json:"running"`
	Healthy   bool      `json:"healthy"`
	PID       int       `json:"pid,omitempty"`
	Workspace string    `json:"workspace,omitempty"`
	Addr      string    `json:"addr,omitempty"`
	URL       string    `json:"url,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

const defaultServeAddr = ":3211"

func servePIDPath(cfg runtimeConfig) string {
	base := filepath.Dir(cfg.dbPath)
	return filepath.Join(base, "serve.pid")
}

func legacyServePIDPaths(baseDir string) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(baseDir, "serve.*.pid"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func firstRunningLegacyServe(baseDir string) (string, servePID, bool) {
	paths, _ := legacyServePIDPaths(baseDir)
	for _, path := range paths {
		pid, err := readServePID(path)
		if err == nil && isProcessAlive(pid.PID) {
			return path, pid, true
		}
	}
	return "", servePID{}, false
}

func readServePID(path string) (servePID, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return servePID{}, err
	}
	var out servePID
	if err := json.Unmarshal(b, &out); err == nil && out.PID > 0 {
		return out, nil
	}
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid); err == nil && pid > 0 {
		return servePID{PID: pid}, nil
	}
	return servePID{}, errors.New("invalid serve pid file")
}

func writeServePID(path string, v servePID) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func buildServeProcessArgs(cfg runtimeConfig, addr string, pidFile string) []string {
	args := []string{
		"serve",
		"--no-open",
		"--addr", addr,
		"--db", cfg.dbPath,
		"--model-dir", cfg.modelDir,
	}
	if strings.TrimSpace(pidFile) != "" {
		args = append(args, "--pid-file", pidFile)
	}
	return args
}

func startServeProcess(cfg runtimeConfig, addr string, pidFile string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	args := buildServeProcessArgs(cfg, addr, pidFile)
	c := exec.Command(exe, args...)
	c.Stdout = io.Discard
	c.Stderr = io.Discard
	if err := c.Start(); err != nil {
		return 0, err
	}
	return c.Process.Pid, nil
}

func resolveServeRuntime(flags commonFlags) (runtimeConfig, error) {
	cfg, err := resolveDashboardRuntime(flags)
	if err != nil {
		return runtimeConfig{}, err
	}
	if cfg.apiURL != "" {
		return runtimeConfig{}, errors.New("serve cannot proxy to another api via --api")
	}
	// Serve is a daemon-level command. The current directory may identify a
	// workspace for ordinary CLI commands, but must never bind the daemon to it.
	cfg.workspace = ""
	if strings.TrimSpace(flags.dbPath) == "" {
		baseDir, err := defaultDBBaseDir()
		if err != nil {
			return runtimeConfig{}, err
		}
		cfg.dbPath = filepath.Join(baseDir, ".serve-placeholder.db")
	}
	return cfg, nil
}

func newServeCommand() *cobra.Command {
	var flags commonFlags
	var addr string
	var noOpen bool
	var start bool
	var stop bool
	var status bool
	var pidFile string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the local API service with background lifecycle maintenance",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			cfg, err := resolveServeRuntime(flags)
			if err != nil {
				return err
			}
			if err := validateOutputFormat(flags.format, false); err != nil {
				return err
			}
			if (start && stop) || (start && status) || (stop && status) {
				return errors.New("only one of --start, --stop, or --status can be set")
			}

			pidPath := strings.TrimSpace(pidFile)
			if pidPath == "" {
				pidPath = servePIDPath(cfg)
			}

			if status {
				out, err := readServeStatus(pidPath)
				if err != nil {
					if legacyPath, _, ok := firstRunningLegacyServe(filepath.Dir(pidPath)); ok {
						out, _ = readServeStatus(legacyPath)
					} else {
						out = serveStatus{Running: false, Healthy: false}
					}
				}
				return writeSuccessEnvelope(cmd.OutOrStdout(), "serve", out)
			}

			if stop {
				v, err := readServePID(pidPath)
				if err != nil {
					paths, _ := legacyServePIDPaths(filepath.Dir(pidPath))
					stopped := 0
					for _, path := range paths {
						legacy, readErr := readServePID(path)
						if readErr == nil && legacy.PID > 0 {
							_ = stopProcess(legacy.PID)
							stopped++
						}
						_ = os.Remove(path)
					}
					if stopped == 0 {
						return fmt.Errorf("serve stop: %w", err)
					}
					return writeSuccessEnvelope(cmd.OutOrStdout(), "serve", map[string]any{"running": false, "healthy": false, "legacy_processes_stopped": stopped})
				}
				if v.PID > 0 {
					_ = stopProcess(v.PID)
				}
				_ = os.Remove(pidPath)
				return writeSuccessEnvelope(cmd.OutOrStdout(), "serve", serveStatus{
					Running: false,
					Healthy: false,
					PID:     v.PID,
				})
			}

			if start {
				if err := validateDashboardStartAddr(addr); err != nil {
					return err
				}
				if v, err := readServePID(pidPath); err == nil && isProcessAlive(v.PID) {
					out, _ := readServeStatus(pidPath)
					return writeSuccessEnvelope(cmd.OutOrStdout(), "serve", out)
				}
				_ = os.Remove(pidPath)
				if legacyPath, legacy, ok := firstRunningLegacyServe(filepath.Dir(pidPath)); ok {
					return fmt.Errorf("legacy workspace service is running (pid %d, %s); run agent-memory serve --stop before starting the multi-workspace daemon", legacy.PID, legacyPath)
				}
				pid, err := startServeProcess(cfg, addr, pidPath)
				if err != nil {
					return err
				}
				_ = writeServePID(pidPath, servePID{
					PID:       pid,
					Workspace: cfg.workspace,
					Addr:      addr,
					URL:       "",
					StartedAt: time.Now().UTC(),
				})
				for i := 0; i < 40; i++ {
					time.Sleep(125 * time.Millisecond)
					out, err := readServeStatus(pidPath)
					if err == nil && out.Running && strings.TrimSpace(out.URL) != "" {
						return writeSuccessEnvelope(cmd.OutOrStdout(), "serve", out)
					}
				}
				if v, err := readServePID(pidPath); err == nil && v.PID > 0 {
					_ = stopProcess(v.PID)
				}
				_ = os.Remove(pidPath)
				return errors.New("serve failed to start (service did not become ready)")
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
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "active embedding provider: %s (%s)\n", provider.Name(), provider.ModelVersion())
			scheduler := newServeScheduler(cfg, provider, cmd.ErrOrStderr())
			svc := &api.Service{
				Workspace:         cfg.workspace,
				BaseDir:           filepath.Dir(cfg.dbPath),
				EmbeddingProvider: provider,
				Scheduler:         scheduler,
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
			server := &http.Server{
				Addr:    addr,
				Handler: api.InstrumentedHandler(api.NewMux(svc)),
			}
			defer func() { _ = svc.Close() }()
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			apiURL := apiURLForListenerAddr(ln.Addr().String())
			if strings.TrimSpace(pidPath) != "" {
				_ = writeServePID(pidPath, servePID{
					PID:       os.Getpid(),
					Workspace: cfg.workspace,
					Addr:      addr,
					URL:       apiURL,
					StartedAt: time.Now().UTC(),
				})
				defer func() { _ = os.Remove(pidPath) }()
			}

			go scheduler.Run(ctx)

			errCh := make(chan error, 1)
			go func() { errCh <- server.Serve(ln) }()

			if noOpen {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", apiURL)
			} else {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "api serving on %s\n", apiURL)
			}

			select {
			case <-ctx.Done():
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer shutdownCancel()
				_ = server.Shutdown(shutdownCtx)
				return nil
			case err := <-errCh:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			}
		},
	}

	addCommonFlags(cmd, &flags)
	_ = cmd.Flags().MarkHidden("workspace")
	cmd.Flags().StringVar(&addr, "addr", defaultServeAddr, "HTTP listen address")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "Do not open anything; just print the API URL")
	cmd.Flags().BoolVar(&start, "start", false, "Start the local memory service in the background and exit")
	cmd.Flags().BoolVar(&stop, "stop", false, "Stop the background memory service")
	cmd.Flags().BoolVar(&status, "status", false, "Show background memory service status")
	cmd.Flags().StringVar(&pidFile, "pid-file", "", "Internal: pid file path")
	_ = cmd.Flags().MarkHidden("pid-file")
	return cmd
}

func readServeStatus(pidPath string) (serveStatus, error) {
	v, err := readServePID(pidPath)
	if err != nil {
		return serveStatus{}, err
	}
	running := isProcessAlive(v.PID)
	healthy := false
	if running && strings.TrimSpace(v.URL) != "" {
		client := &http.Client{Timeout: 500 * time.Millisecond}
		req, _ := http.NewRequest(http.MethodGet, strings.TrimRight(v.URL, "/")+"/health", nil)
		if res, err := client.Do(req); err == nil {
			healthy = res.StatusCode >= 200 && res.StatusCode < 300
			_ = res.Body.Close()
		}
	}
	return serveStatus{
		Running:   running,
		Healthy:   healthy,
		PID:       v.PID,
		Workspace: v.Workspace,
		Addr:      v.Addr,
		URL:       v.URL,
		StartedAt: v.StartedAt,
	}, nil
}

const (
	schedulerDailyInterval  = 24 * time.Hour
	schedulerHygieneWindow  = 7 * 24 * time.Hour
	schedulerHistoryLimit   = 30
	schedulerDefaultHistory = 30
)

const (
	schedulerTriggerStartup = "startup_catchup"
	schedulerTriggerDaily   = "daily_tick"
	schedulerTriggerManual  = "manual"
)

const (
	schedulerResultCompleted = "completed"
	schedulerResultSkipped   = "skipped"
	schedulerResultFailed    = "failed"
)

const (
	schedulerSkipNoMemories    = "no_memories"
	schedulerSkipInactiveToday = "inactive_today"
	schedulerSkipAlreadyRun    = "already_running"
)

type wallClockAnchor struct {
	hour       int
	minute     int
	second     int
	nanosecond int
	location   *time.Location
}

type schedulerDecision struct {
	eligible       bool
	hygieneOverdue bool
	skipReason     string
	lastActivityAt time.Time
}

type serveScheduler struct {
	cfg         runtimeConfig
	provider    embeddings.Provider
	logOut      io.Writer
	now         func() time.Time
	anchor      wallClockAnchor
	startedAt   time.Time
	historyKeep int

	mu          sync.RWMutex
	lastTickAt  time.Time
	nextTickAt  time.Time
	runningByWS map[string]time.Time
}

func newServeScheduler(cfg runtimeConfig, provider embeddings.Provider, logOut io.Writer) *serveScheduler {
	now := time.Now()
	return &serveScheduler{
		cfg:         cfg,
		provider:    provider,
		logOut:      logOut,
		now:         time.Now,
		anchor:      newWallClockAnchor(now),
		startedAt:   now.UTC(),
		historyKeep: schedulerHistoryLimit,
		runningByWS: make(map[string]time.Time),
	}
}

func newWallClockAnchor(now time.Time) wallClockAnchor {
	local := now.In(time.Local)
	return wallClockAnchor{
		hour:       local.Hour(),
		minute:     local.Minute(),
		second:     local.Second(),
		nanosecond: local.Nanosecond(),
		location:   local.Location(),
	}
}

func (a wallClockAnchor) NextAfter(now time.Time) time.Time {
	localNow := now.In(a.location)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), a.hour, a.minute, a.second, a.nanosecond, a.location)
	if !next.After(localNow) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func (s *serveScheduler) Run(ctx context.Context) {
	s.runTick(ctx, schedulerTriggerStartup, false)
	for {
		next := s.anchor.NextAfter(s.now())
		s.setNextTick(next)
		wait := time.Until(next)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
			s.runTick(ctx, schedulerTriggerDaily, false)
		}
	}
}

func (s *serveScheduler) Status(ctx context.Context) (*api.SchedulerStatus, error) {
	items, err := s.listManagedWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	status := &api.SchedulerStatus{
		Enabled:    true,
		StartedAt:  s.startedAt,
		Workspaces: make([]api.SchedulerWorkspaceStatus, 0, len(items)),
	}
	s.mu.RLock()
	status.LastTickAt = s.lastTickAt
	status.NextTickAt = s.nextTickAt
	running := make(map[string]time.Time, len(s.runningByWS))
	for ws, startedAt := range s.runningByWS {
		running[ws] = startedAt
	}
	s.mu.RUnlock()

	for _, item := range items {
		wsStatus, err := s.workspaceStatus(ctx, item, schedulerTriggerDaily, false)
		if err != nil {
			s.logf("serve lifecycle: workspace=%s status error: %v", item.Name, err)
			continue
		}
		if _, ok := running[item.Name]; ok {
			wsStatus.RunInProgress = true
		}
		status.Workspaces = append(status.Workspaces, wsStatus)
	}
	sort.Slice(status.Workspaces, func(i, j int) bool {
		return status.Workspaces[i].Workspace < status.Workspaces[j].Workspace
	})
	return status, nil
}

func (s *serveScheduler) History(ctx context.Context, workspaceName string, limit int) ([]api.SchedulerRun, error) {
	if limit <= 0 {
		limit = schedulerDefaultHistory
	}
	if limit > 200 {
		limit = 200
	}
	if strings.TrimSpace(workspaceName) != "" {
		item := workspace.ListItem{Name: strings.TrimSpace(workspaceName), DBPath: s.workspaceDBPath(workspace.ListItem{Name: strings.TrimSpace(workspaceName)})}
		return s.historyForWorkspace(ctx, item, limit)
	}
	items, err := s.listManagedWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]api.SchedulerRun, 0, limit)
	for _, item := range items {
		runs, err := s.historyForWorkspace(ctx, item, limit)
		if err != nil {
			s.logf("serve lifecycle: workspace=%s history error: %v", item.Name, err)
			continue
		}
		out = append(out, runs...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *serveScheduler) RunNow(ctx context.Context, workspaceName string, force bool) (*api.SchedulerRun, error) {
	workspaceName = strings.TrimSpace(workspaceName)
	if workspaceName == "" {
		workspaceName = strings.TrimSpace(s.cfg.workspace)
	}
	if workspaceName == "" {
		return nil, errors.New("workspace is required for manual scheduler runs")
	}
	item := workspace.ListItem{Name: workspaceName, DBPath: s.workspaceDBPath(workspace.ListItem{Name: workspaceName})}
	run, err := s.runWorkspace(ctx, item, schedulerTriggerManual, force)
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *serveScheduler) runTick(ctx context.Context, trigger string, force bool) {
	s.setLastTick(s.now().UTC())
	items, err := s.listManagedWorkspaces(ctx)
	if err != nil {
		s.logf("serve lifecycle: list workspaces error: %v", err)
		return
	}
	for _, item := range items {
		if _, err := s.runWorkspace(ctx, item, trigger, force); err != nil {
			s.logf("serve lifecycle: workspace=%s run error: %v", item.Name, err)
		}
	}
}

func (s *serveScheduler) runWorkspace(ctx context.Context, item workspace.ListItem, trigger string, force bool) (api.SchedulerRun, error) {
	store, err := sqlite.Open(ctx, s.workspaceDBPath(item))
	if err != nil {
		return api.SchedulerRun{}, fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	summary, err := store.GetWorkspaceActivitySummary(ctx, item.Name)
	if err != nil {
		return api.SchedulerRun{}, err
	}
	state, err := store.GetSchedulerWorkspaceState(ctx, item.Name)
	if err != nil {
		return api.SchedulerRun{}, err
	}
	decision := evaluateSchedulerDecision(trigger, summary, state, s.now().UTC(), force)
	startedAt := s.now().UTC()
	if !decision.eligible {
		run := api.SchedulerRun{
			ID:          schedulerRunID(item.Name, trigger, startedAt),
			Workspace:   item.Name,
			StartedAt:   startedAt,
			CompletedAt: startedAt,
			Trigger:     trigger,
			Result:      schedulerResultSkipped,
			SkipReason:  decision.skipReason,
		}
		if err := persistSchedulerOutcome(ctx, store, state, run); err != nil {
			return api.SchedulerRun{}, err
		}
		return run, nil
	}
	if !s.acquireWorkspace(item.Name, startedAt) {
		run := api.SchedulerRun{
			ID:          schedulerRunID(item.Name, trigger, startedAt),
			Workspace:   item.Name,
			StartedAt:   startedAt,
			CompletedAt: startedAt,
			Trigger:     trigger,
			Result:      schedulerResultSkipped,
			SkipReason:  schedulerSkipAlreadyRun,
		}
		if err := persistSchedulerOutcome(ctx, store, state, run); err != nil {
			return api.SchedulerRun{}, err
		}
		return run, nil
	}
	defer s.releaseWorkspace(item.Name)

	pipeline := engine.NewWritePipelineWithEmbedder(store, s.provider)
	manager := engine.NewLifecycleManager(store, pipeline)
	manager.WithArchive(filepath.Dir(s.cfg.dbPath))
	metrics, err := manager.Run(ctx, item.Name)
	completedAt := s.now().UTC()
	run := api.SchedulerRun{
		ID:          schedulerRunID(item.Name, trigger, startedAt),
		Workspace:   item.Name,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		Trigger:     trigger,
		DurationMS:  int(completedAt.Sub(startedAt).Milliseconds()),
	}
	if err != nil {
		run.Result = schedulerResultFailed
		run.Error = err.Error()
		if persistErr := persistSchedulerOutcome(ctx, store, state, run); persistErr != nil {
			return api.SchedulerRun{}, persistErr
		}
		return run, nil
	}
	run.Result = schedulerResultCompleted
	run.DecayUpdated = metrics.DecayUpdated
	run.Consolidated = metrics.Consolidated
	run.ConflictsFound = metrics.ConflictsFound
	run.Evicted = metrics.Evicted
	run.Promoted = metrics.Promoted
	run.Demoted = metrics.Demoted
	if err := persistSchedulerOutcome(ctx, store, state, run); err != nil {
		return api.SchedulerRun{}, err
	}
	return run, nil
}

func (s *serveScheduler) workspaceStatus(ctx context.Context, item workspace.ListItem, trigger string, force bool) (api.SchedulerWorkspaceStatus, error) {
	store, err := sqlite.Open(ctx, s.workspaceDBPath(item))
	if err != nil {
		return api.SchedulerWorkspaceStatus{}, fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	summary, err := store.GetWorkspaceActivitySummary(ctx, item.Name)
	if err != nil {
		return api.SchedulerWorkspaceStatus{}, err
	}
	state, err := store.GetSchedulerWorkspaceState(ctx, item.Name)
	if err != nil {
		return api.SchedulerWorkspaceStatus{}, err
	}
	decision := evaluateSchedulerDecision(trigger, summary, state, s.now().UTC(), force)
	status := api.SchedulerWorkspaceStatus{
		Workspace:         item.Name,
		MemoryCount:       summary.MemoryCount,
		LastActivityAt:    decision.lastActivityAt,
		HygieneOverdue:    decision.hygieneOverdue,
		EligibleDaily:     decision.eligible,
		CurrentSkipReason: decision.skipReason,
	}
	if state != nil {
		status.LastScheduledAt = state.LastScheduledAt
		status.LastCompletedAt = state.LastCompletedAt
		status.LastResult = state.LastResult
		status.LastSkipReason = state.LastSkipReason
		status.LastDurationMS = state.LastDurationMS
		status.LastImpacts = state.LastImpacts
		status.LastError = state.LastError
	}
	return status, nil
}

func (s *serveScheduler) historyForWorkspace(ctx context.Context, item workspace.ListItem, limit int) ([]api.SchedulerRun, error) {
	store, err := sqlite.Open(ctx, s.workspaceDBPath(item))
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	runs, err := store.ListSchedulerRunHistory(ctx, item.Name, limit)
	if err != nil {
		return nil, err
	}
	out := make([]api.SchedulerRun, 0, len(runs))
	for _, run := range runs {
		out = append(out, api.SchedulerRun{
			ID:             run.ID,
			Workspace:      run.Workspace,
			StartedAt:      run.StartedAt,
			CompletedAt:    run.CompletedAt,
			Trigger:        run.Trigger,
			Result:         run.Result,
			SkipReason:     run.SkipReason,
			DurationMS:     run.DurationMS,
			DecayUpdated:   run.DecayUpdated,
			Consolidated:   run.Consolidated,
			ConflictsFound: run.ConflictsFound,
			Evicted:        run.Evicted,
			Promoted:       run.Promoted,
			Demoted:        run.Demoted,
			Error:          run.Error,
		})
	}
	return out, nil
}

func (s *serveScheduler) listManagedWorkspaces(ctx context.Context) ([]workspace.ListItem, error) {
	if ws := strings.TrimSpace(s.cfg.workspace); ws != "" {
		return []workspace.ListItem{{
			Name:   ws,
			DBPath: s.cfg.dbPath,
		}}, nil
	}
	mgr, err := workspace.NewManager(filepath.Dir(s.cfg.dbPath))
	if err != nil {
		return nil, err
	}
	items, err := mgr.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]workspace.ListItem, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *serveScheduler) workspaceDBPath(item workspace.ListItem) string {
	dbPath := strings.TrimSpace(item.DBPath)
	if dbPath == "" {
		dbPath = filepath.Join(filepath.Dir(s.cfg.dbPath), item.Name+".db")
	}
	if strings.TrimSpace(item.Name) != "" && filepath.Base(dbPath) == ".dashboard-placeholder.db" {
		return filepath.Join(filepath.Dir(dbPath), item.Name+".db")
	}
	return dbPath
}

func (s *serveScheduler) acquireWorkspace(workspaceName string, startedAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runningByWS[workspaceName]; ok {
		return false
	}
	s.runningByWS[workspaceName] = startedAt
	return true
}

func (s *serveScheduler) releaseWorkspace(workspaceName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runningByWS, workspaceName)
}

func (s *serveScheduler) setLastTick(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTickAt = at
}

func (s *serveScheduler) setNextTick(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextTickAt = at
}

func (s *serveScheduler) logf(format string, args ...any) {
	if s.logOut == nil {
		return
	}
	_, _ = fmt.Fprintf(s.logOut, format+"\n", args...)
}

func evaluateSchedulerDecision(trigger string, summary *sqlite.WorkspaceActivitySummary, state *sqlite.SchedulerWorkspaceState, now time.Time, force bool) schedulerDecision {
	decision := schedulerDecision{}
	if summary == nil || summary.MemoryCount == 0 {
		decision.skipReason = schedulerSkipNoMemories
		return decision
	}
	if summary.LastUpdatedAt.After(decision.lastActivityAt) {
		decision.lastActivityAt = summary.LastUpdatedAt
	}
	if summary.LastAccessedAt.After(decision.lastActivityAt) {
		decision.lastActivityAt = summary.LastAccessedAt
	}
	if state == nil || state.LastCompletedAt.IsZero() || now.Sub(state.LastCompletedAt) >= schedulerHygieneWindow {
		decision.hygieneOverdue = true
	}
	hasRecentActivity := !decision.lastActivityAt.IsZero() && now.Sub(decision.lastActivityAt) <= schedulerDailyInterval

	switch trigger {
	case schedulerTriggerStartup:
		decision.eligible = decision.hygieneOverdue
	case schedulerTriggerManual:
		decision.eligible = force || summary.MemoryCount > 0
	default:
		decision.eligible = force || hasRecentActivity || decision.hygieneOverdue
	}
	if !decision.eligible {
		decision.skipReason = schedulerSkipInactiveToday
	}
	return decision
}

func persistSchedulerOutcome(ctx context.Context, store *sqlite.Store, prior *sqlite.SchedulerWorkspaceState, run api.SchedulerRun) error {
	record := sqlite.SchedulerRunRecord{
		ID:             run.ID,
		Workspace:      run.Workspace,
		StartedAt:      run.StartedAt,
		CompletedAt:    run.CompletedAt,
		Trigger:        run.Trigger,
		Result:         run.Result,
		SkipReason:     run.SkipReason,
		DurationMS:     run.DurationMS,
		DecayUpdated:   run.DecayUpdated,
		Consolidated:   run.Consolidated,
		ConflictsFound: run.ConflictsFound,
		Evicted:        run.Evicted,
		Promoted:       run.Promoted,
		Demoted:        run.Demoted,
		Error:          run.Error,
	}
	if err := store.InsertSchedulerRunRecord(ctx, record, schedulerHistoryLimit); err != nil {
		return err
	}
	impacts := run.DecayUpdated + run.Consolidated + run.Evicted + run.Promoted + run.Demoted
	state := sqlite.SchedulerWorkspaceState{
		Workspace:       run.Workspace,
		LastScheduledAt: run.StartedAt,
		LastResult:      run.Result,
		LastSkipReason:  run.SkipReason,
		LastDurationMS:  run.DurationMS,
		LastImpacts:     impacts,
		LastError:       run.Error,
		UpdatedAt:       run.CompletedAt,
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = run.StartedAt
	}
	if prior != nil && !prior.LastCompletedAt.IsZero() {
		state.LastCompletedAt = prior.LastCompletedAt
	}
	if run.Result == schedulerResultCompleted {
		state.LastCompletedAt = run.CompletedAt
		state.LastSkipReason = ""
		state.LastError = ""
	}
	return store.UpsertSchedulerWorkspaceState(ctx, state)
}

func schedulerRunID(workspaceName, trigger string, startedAt time.Time) string {
	return fmt.Sprintf("%s-%s-%d", strings.TrimSpace(workspaceName), strings.TrimSpace(trigger), startedAt.UnixNano())
}
