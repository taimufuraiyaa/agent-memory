package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"github.com/taimufuraiyaa/agent-memory/internal/connectors"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type Options struct {
	Root       string
	DataDir    string
	Workspace  string
	ServiceURL string
	ModelDir   string
	Connectors []config.ConnectorConfig
}

type namedCheck struct {
	name string
	run  func(context.Context) Result
}

func (c namedCheck) Name() string                   { return c.name }
func (c namedCheck) Run(ctx context.Context) Result { return c.run(ctx) }

func DefaultChecks(options Options) []Check {
	return []Check{
		namedCheck{"binary", func(context.Context) Result {
			path, err := os.Executable()
			if err != nil {
				return failed(err, "reinstall agent-memory")
			}
			return passed(path)
		}},
		namedCheck{"workspace_registry", func(context.Context) Result {
			path := filepath.Join(options.DataDir, "workspaces.json")
			data, err := os.ReadFile(path)
			if err != nil {
				return failed(err, "run agent-memory init")
			}
			var registry any
			if err := json.Unmarshal(data, &registry); err != nil {
				return failed(err, "repair or restore workspaces.json")
			}
			return passed(path)
		}},
		namedCheck{"database", func(context.Context) Result {
			path := filepath.Join(options.DataDir, strings.TrimSpace(options.Workspace)+".db")
			info, err := os.Stat(path)
			if err != nil {
				return failed(err, "initialize or repair the workspace")
			}
			return passed(fmt.Sprintf("%s (%d bytes)", path, info.Size()))
		}},
		namedCheck{"embedding_model", func(context.Context) Result {
			path := filepath.Join(options.ModelDir, "model.onnx")
			if _, err := os.Stat(path); err != nil {
				return failed(err, "run agent-memory install or configure model-dir")
			}
			return passed(path)
		}},
		namedCheck{"embedding_runtime", func(context.Context) Result {
			path := filepath.Join(options.DataDir, "onnxruntime")
			if _, err := os.Stat(path); err != nil {
				return warning(err.Error(), "run agent-memory install")
			}
			return passed(path)
		}},
		namedCheck{"service", func(ctx context.Context) Result {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(options.ServiceURL, "/")+"/health", nil)
			if err != nil {
				return failed(err, "correct the service URL")
			}
			client := &http.Client{Timeout: 750 * time.Millisecond}
			response, err := client.Do(request)
			if err != nil {
				return failed(err, "start agent-memory serve")
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				return failed(fmt.Errorf("HTTP %d", response.StatusCode), "inspect service logs")
			}
			body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
			if err != nil {
				return failed(err, "inspect service health response")
			}
			return serviceHealthResult(body)
		}},
		namedCheck{"port", func(context.Context) Result {
			parsed, err := url.Parse(options.ServiceURL)
			if err != nil {
				return failed(err, "correct the service URL")
			}
			connection, err := net.DialTimeout("tcp", parsed.Host, 300*time.Millisecond)
			if err != nil {
				return failed(err, "start the service or free the configured port")
			}
			_ = connection.Close()
			return passed(parsed.Host)
		}},
		namedCheck{"writable_root", func(context.Context) Result {
			info, err := os.Stat(options.DataDir)
			if err != nil {
				return failed(err, "create the data directory")
			}
			if info.Mode().Perm()&0o200 == 0 {
				return failed(fmt.Errorf("owner write bit is not set"), "grant owner write permission")
			}
			return passed(options.DataDir)
		}},
		namedCheck{"hooks", func(context.Context) Result {
			paths := []string{filepath.Join(options.Root, ".codex", "hooks.json"), filepath.Join(options.Root, ".claude", "settings.json")}
			for _, path := range paths {
				if _, err := os.Stat(path); err == nil {
					return passed(path)
				}
			}
			return warning("no supported hook artifact found", "run agent-memory connect <agent>")
		}},
		namedCheck{"mcp", func(context.Context) Result {
			path, err := exec.LookPath("agent-memory-mcp")
			if err != nil {
				return warning(err.Error(), "install the agent-memory MCP package")
			}
			return passed(path)
		}},
		namedCheck{"connectors", func(ctx context.Context) Result {
			if len(options.Connectors) == 0 {
				return passed("no connectors enabled")
			}
			store, err := sqlite.Open(ctx, filepath.Join(options.DataDir, strings.TrimSpace(options.Workspace)+".db"))
			if err != nil {
				return failed(err, "repair the workspace database")
			}
			defer store.Close()
			for _, item := range options.Connectors {
				if !item.Enabled {
					continue
				}
				if item.Type != "filesystem" {
					return warning("unsupported connector "+item.ID, "disable it or install a supported connector")
				}
				candidate := connectors.NewFilesystem(connectors.FilesystemConfig{ID: item.ID, Workspace: item.Workspace, Roots: item.Roots, Ignore: item.Ignore, PreviewBytes: item.PreviewBytes})
				if err := candidate.Validate(); err != nil {
					return failed(err, "correct connector "+item.ID+" configuration")
				}
				cp, err := store.LoadConnectorCheckpoint(ctx, item.ID)
				if err != nil {
					return warning("connector "+item.ID+" has no checkpoint", "start the connector")
				}
				if cp.LastError != "" {
					return warning("connector "+item.ID+" degraded; checkpoint age "+time.Since(cp.UpdatedAt).Round(time.Second).String(), "inspect connector error and rescan")
				}
			}
			return passed(fmt.Sprintf("%d configured connector(s) healthy", len(options.Connectors)))
		}},
	}
}

func serviceHealthResult(body []byte) Result {
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			ServiceMode          string `json:"service_mode"`
			RegisteredWorkspaces int    `json:"registered_workspaces"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return failed(err, "upgrade or restart agent-memory serve")
	}
	if !envelope.OK || envelope.Data.ServiceMode != "multi_workspace" {
		return failed(fmt.Errorf("legacy or workspace-bound service is running"), "stop legacy services and start the multi-workspace daemon")
	}
	return passed(fmt.Sprintf("multi-workspace daemon (%d registered)", envelope.Data.RegisteredWorkspaces))
}

func passed(evidence string) Result { return Result{Status: StatusPass, Evidence: evidence} }
func warning(evidence, action string) Result {
	return Result{Status: StatusWarning, Evidence: evidence, NextAction: action}
}
func failed(err error, action string) Result {
	return Result{Status: StatusFail, Err: err, NextAction: action}
}
