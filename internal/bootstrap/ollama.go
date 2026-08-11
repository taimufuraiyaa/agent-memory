package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	DefaultOllamaEndpoint = "http://127.0.0.1:11434"
	DefaultPlannerModel   = "qwen3:8b"
	maxOllamaResponse     = 1 << 20
	maxInstallScript      = 4 << 20
)

type CommandRunner func(context.Context, string, []string, io.Writer, io.Writer) error
type ExecutableLookup func(string) (string, error)
type ProcessStarter func(string, []string, string) error

type OllamaPlannerOptions struct {
	Endpoint     string
	Model        string
	DataDir      string
	GOOS         string
	Stdout       io.Writer
	Stderr       io.Writer
	Client       *http.Client
	Lookup       ExecutableLookup
	Run          CommandRunner
	Start        ProcessStarter
	PollAttempts int
}

type OllamaPlannerResult struct {
	RuntimeReused    bool
	RuntimeInstalled bool
	ModelPulled      bool
	ModelAvailable   bool
	Endpoint         string
	Model            string
}

func OllamaPlannerReady(ctx context.Context, endpoint, model string) (bool, error) {
	options := normalizeOllamaOptions(OllamaPlannerOptions{Endpoint: endpoint, Model: model})
	if err := validateOllamaEndpoint(options.Endpoint); err != nil {
		return false, err
	}
	if !validOllamaModelID(options.Model) {
		return false, errors.New("Ollama planner model ID is invalid")
	}
	if err := ollamaReachable(ctx, options); err != nil {
		return false, nil
	}
	return ollamaModelAvailable(ctx, options)
}

func OllamaAvailableModels(ctx context.Context, endpoint string, models []string) (map[string]bool, error) {
	options := normalizeOllamaOptions(OllamaPlannerOptions{Endpoint: endpoint})
	if err := validateOllamaEndpoint(options.Endpoint); err != nil {
		return nil, err
	}
	for _, model := range models {
		if !validOllamaModelID(model) {
			return nil, fmt.Errorf("Ollama model ID %q is invalid", model)
		}
	}
	if err := ollamaReachable(ctx, options); err != nil {
		return nil, err
	}
	inventory, err := ollamaModelInventory(ctx, options)
	if err != nil {
		return nil, err
	}
	available := make(map[string]bool, len(models))
	for _, model := range models {
		available[model] = inventory[model]
	}
	return available, nil
}

type runtimeInstallPlan struct {
	command        string
	args           []string
	officialScript bool
}

func (p runtimeInstallPlan) String() string {
	if p.officialScript {
		return "official-install-script"
	}
	return strings.TrimSpace(p.command + " " + strings.Join(p.args, " "))
}

func EnsureOllamaPlanner(ctx context.Context, options OllamaPlannerOptions) (OllamaPlannerResult, error) {
	options = normalizeOllamaOptions(options)
	if err := validateOllamaEndpoint(options.Endpoint); err != nil {
		return OllamaPlannerResult{}, err
	}
	if !validOllamaModelID(options.Model) {
		return OllamaPlannerResult{}, errors.New("Ollama planner model ID is invalid")
	}
	result := OllamaPlannerResult{Endpoint: options.Endpoint, Model: options.Model}
	ollamaPath, lookupErr := options.Lookup("ollama")
	if lookupErr == nil {
		result.RuntimeReused = true
	} else {
		plan, err := ollamaRuntimeInstallPlan(options.GOOS, options.Lookup)
		if err != nil {
			return result, err
		}
		if err := executeRuntimeInstall(ctx, options, plan); err != nil {
			return result, fmt.Errorf("install Ollama runtime: %w", err)
		}
		ollamaPath, err = options.Lookup("ollama")
		if err != nil {
			return result, errors.New("Ollama installation completed but the executable is not on PATH")
		}
		result.RuntimeInstalled = true
	}

	if err := ollamaReachable(ctx, options); err != nil {
		logPath := filepath.Join(options.DataDir, "logs", "ollama.log")
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return result, fmt.Errorf("prepare Ollama log: %w", err)
		}
		if err := options.Start(ollamaPath, []string{"serve"}, logPath); err != nil {
			return result, fmt.Errorf("start Ollama: %w", err)
		}
		var readinessErr error
		for attempt := 0; attempt < options.PollAttempts; attempt++ {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
			readinessErr = ollamaReachable(ctx, options)
			if readinessErr == nil {
				break
			}
		}
		if readinessErr != nil {
			return result, errors.New("Ollama did not become reachable on the approved local endpoint")
		}
	}

	available, err := ollamaModelAvailable(ctx, options)
	if err != nil {
		return result, err
	}
	if !available {
		if err := options.Run(ctx, ollamaPath, []string{"pull", options.Model}, options.Stdout, options.Stderr); err != nil {
			return result, fmt.Errorf("pull Ollama model %s: %w", options.Model, err)
		}
		result.ModelPulled = true
		available, err = ollamaModelAvailable(ctx, options)
		if err != nil {
			return result, err
		}
	}
	if !available {
		return result, fmt.Errorf("Ollama model %s is not available after pull", options.Model)
	}
	result.ModelAvailable = true
	return result, nil
}

func normalizeOllamaOptions(options OllamaPlannerOptions) OllamaPlannerOptions {
	if strings.TrimSpace(options.Endpoint) == "" {
		options.Endpoint = DefaultOllamaEndpoint
	}
	options.Endpoint = strings.TrimRight(strings.TrimSpace(options.Endpoint), "/")
	if strings.TrimSpace(options.Model) == "" {
		options.Model = DefaultPlannerModel
	}
	options.Model = strings.TrimSpace(options.Model)
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	if options.Lookup == nil {
		options.Lookup = exec.LookPath
	}
	if options.Run == nil {
		options.Run = runCommand
	}
	if options.Start == nil {
		options.Start = startProcess
	}
	if options.PollAttempts <= 0 {
		options.PollAttempts = 40
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	} else {
		clone := *client
		client = &clone
		if client.Timeout == 0 || client.Timeout > 3*time.Second {
			client.Timeout = 3 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("Ollama redirects are disabled") }
	options.Client = client
	return options
}

func validateOllamaEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Hostname() == "" {
		return errors.New("Ollama endpoint must be an HTTP or HTTPS loopback URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return errors.New("Ollama endpoint cannot contain credentials, a path, query, or fragment")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("Ollama endpoint must remain on loopback")
	}
	return nil
}

func validOllamaModelID(model string) bool {
	if model == "" || len(model) > 128 {
		return false
	}
	for _, value := range model {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || strings.ContainsRune("._:/-", value) {
			continue
		}
		return false
	}
	return true
}

func ollamaRuntimeInstallPlan(goos string, lookup ExecutableLookup) (runtimeInstallPlan, error) {
	switch goos {
	case "darwin":
		path, err := lookup("brew")
		if err != nil {
			return runtimeInstallPlan{}, errors.New("Ollama is missing; install the official macOS application or Homebrew first")
		}
		return runtimeInstallPlan{command: path, args: []string{"install", "ollama"}}, nil
	case "windows":
		path, err := lookup("winget")
		if err != nil {
			return runtimeInstallPlan{}, errors.New("Ollama is missing; install the official OllamaSetup.exe or WinGet first")
		}
		return runtimeInstallPlan{command: path, args: []string{"install", "--id", "Ollama.Ollama", "-e", "--accept-package-agreements", "--accept-source-agreements"}}, nil
	case "linux":
		return runtimeInstallPlan{officialScript: true}, nil
	default:
		return runtimeInstallPlan{}, fmt.Errorf("automatic Ollama installation is unsupported on %s", goos)
	}
}

func executeRuntimeInstall(ctx context.Context, options OllamaPlannerOptions, plan runtimeInstallPlan) error {
	if !plan.officialScript {
		return options.Run(ctx, plan.command, plan.args, options.Stdout, options.Stderr)
	}
	path, err := downloadOllamaInstallScript(ctx, options)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(path) }()
	return options.Run(ctx, "/bin/sh", []string{path}, options.Stdout, options.Stderr)
}

func downloadOllamaInstallScript(ctx context.Context, options OllamaPlannerOptions) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://ollama.com/install.sh", nil)
	if err != nil {
		return "", err
	}
	response, err := options.Client.Do(request)
	if err != nil {
		return "", errors.New("download official Ollama installer failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", errors.New("official Ollama installer returned an unsuccessful status")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxInstallScript+1))
	if err != nil || len(data) == 0 || len(data) > maxInstallScript || !strings.HasPrefix(string(data), "#!/bin/sh") {
		return "", errors.New("official Ollama installer response is invalid or too large")
	}
	if err := os.MkdirAll(options.DataDir, 0o755); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(options.DataDir, ".ollama-install-*.sh")
	if err != nil {
		return "", err
	}
	path := file.Name()
	cleanup := func() { _ = file.Close(); _ = os.Remove(path) }
	if err := file.Chmod(0o700); err != nil {
		cleanup()
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		cleanup()
		return "", err
	}
	if err := file.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func ollamaReachable(ctx context.Context, options OllamaPlannerOptions) error {
	var version struct {
		Version string `json:"version"`
	}
	if err := ollamaGET(ctx, options, "/api/version", &version); err != nil || strings.TrimSpace(version.Version) == "" {
		return errors.New("Ollama is unavailable")
	}
	return nil
}

func ollamaModelAvailable(ctx context.Context, options OllamaPlannerOptions) (bool, error) {
	inventory, err := ollamaModelInventory(ctx, options)
	if err != nil {
		return false, err
	}
	return inventory[options.Model], nil
}

func ollamaModelInventory(ctx context.Context, options OllamaPlannerOptions) (map[string]bool, error) {
	var inventory struct {
		Models []struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"models"`
	}
	if err := ollamaGET(ctx, options, "/api/tags", &inventory); err != nil {
		return nil, errors.New("Ollama model inventory is unavailable")
	}
	available := make(map[string]bool, len(inventory.Models)*2)
	for _, item := range inventory.Models {
		if item.Name != "" {
			available[item.Name] = true
		}
		if item.Model != "" {
			available[item.Model] = true
		}
	}
	return available, nil
}

func ollamaGET(ctx context.Context, options OllamaPlannerOptions, path string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, options.Endpoint+path, nil)
	if err != nil {
		return err
	}
	response, err := options.Client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("Ollama returned an unsuccessful status")
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxOllamaResponse+1))
	if err := decoder.Decode(destination); err != nil {
		return errors.New("Ollama returned malformed JSON")
	}
	return nil
}

func runCommand(ctx context.Context, name string, args []string, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

func startProcess(name string, args []string, logPath string) error {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	command := exec.Command(name, args...)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = command.Process.Release()
	return logFile.Close()
}
