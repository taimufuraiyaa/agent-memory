package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func schedulerSummaryForWorkspace(status *SchedulerStatus, workspace string) map[string]any {
	if status == nil {
		return nil
	}
	summary := map[string]any{
		"enabled":      status.Enabled,
		"started_at":   status.StartedAt,
		"last_tick_at": status.LastTickAt,
		"next_tick_at": status.NextTickAt,
	}
	for _, item := range status.Workspaces {
		if item.Workspace != workspace {
			continue
		}
		summary["workspace"] = item
		break
	}
	return summary
}

func externalSchedulerSummary(ctx context.Context, baseDir, workspace string) map[string]any {
	for _, pidPath := range externalServePIDCandidates(baseDir, workspace) {
		data, err := os.ReadFile(pidPath)
		if err != nil {
			continue
		}
		var st struct {
			PID int    `json:"pid"`
			URL string `json:"url"`
		}
		if err := json.Unmarshal(data, &st); err != nil || st.PID <= 0 {
			continue
		}
		process, err := os.FindProcess(st.PID)
		if err != nil || process.Signal(syscall.Signal(0)) != nil {
			continue
		}
		if status := fetchExternalSchedulerStatus(ctx, strings.TrimSpace(st.URL), workspace); status != nil {
			return status
		}
		return map[string]any{"enabled": true}
	}
	return nil
}

func externalServePIDCandidates(baseDir, workspace string) []string {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return nil
	}
	paths := make([]string, 0, 4)
	seen := map[string]struct{}{}
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	names := []string{}
	if ws := strings.TrimSpace(workspace); ws != "" {
		names = append(names, "serve."+ws+".pid")
	}
	names = append(names, "serve.pid")
	for _, name := range names {
		add(filepath.Join(baseDir, name))
		add(filepath.Join(baseDir, ".agent-memory", name))
	}
	return paths
}

func fetchExternalSchedulerStatus(ctx context.Context, baseURL, workspace string) map[string]any {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	reqCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"/api/v1/scheduler/status", nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var env struct {
		OK   bool             `json:"ok"`
		Data *SchedulerStatus `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil
	}
	if !env.OK || env.Data == nil {
		return nil
	}
	return schedulerSummaryForWorkspace(env.Data, workspace)
}
