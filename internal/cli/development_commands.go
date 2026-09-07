package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	developmentBaseComposePath     = "deploy/saas/compose.yaml"
	developmentOverrideComposePath = "deploy/saas/compose.dev.yaml"
)

var developmentInfrastructureServices = []string{"postgres", "minio", "nats"}
var developmentDependentServices = []string{"worker", "reconciler", "edge", "frontend"}

type developmentLifecycleStep struct {
	name string
	args []string
}

type upgradeDevelopmentServicesResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

var runDevelopmentCommand = func(ctx context.Context, dir, name string, args []string, stdout, stderr io.Writer) error {
	child := exec.CommandContext(ctx, name, args...)
	child.Dir = dir
	child.Stdout = stdout
	child.Stderr = stderr
	return child.Run()
}

var probeDevelopmentServices = func(ctx context.Context, root string) ([]string, error) {
	args := append(developmentComposeBaseArgs(root), "ps", "--status", "running", "--services")
	command := exec.CommandContext(ctx, "docker", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return strings.Fields(string(output)), nil
}

func newDevelopmentCommand(operation string) *cobra.Command {
	descriptions := map[string]string{
		"start":   "Start backend services and the hot-reload frontend",
		"stop":    "Stop all local Agent Memory containers",
		"restart": "Restart the backend first, then other containers",
		"build":   "Build and recreate the backend, then restart other containers",
	}
	var enableSaaS bool
	command := &cobra.Command{
		Use:   operation,
		Short: descriptions[operation],
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve current directory: %w", err)
			}
			root, err := findDevelopmentRoot(cwd)
			if err != nil {
				return err
			}
			return executeDevelopmentLifecycle(cmd, root, operation, enableSaaS)
		},
	}
	if operation == "start" || operation == "restart" {
		command.Flags().BoolVar(&enableSaaS, "enable-saas", false, "Start the complete SaaS development topology and hot-reload frontend")
	}
	return command
}

func findDevelopmentRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	for {
		base := filepath.Join(current, developmentBaseComposePath)
		override := filepath.Join(current, developmentOverrideComposePath)
		if regularFile(base) && regularFile(override) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", errors.New("Agent Memory source checkout not found; run this command inside the agent-memory repository")
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func executeDevelopmentLifecycle(cmd *cobra.Command, root, operation string, enableSaaS bool) error {
	base := developmentComposeBaseArgs(root)
	steps, err := developmentLifecycleSteps(operation, enableSaaS)
	if err != nil {
		return err
	}
	for _, step := range steps {
		args := append(append([]string(nil), base...), step.args...)
		if err := runDevelopmentCommand(cmd.Context(), root, "docker", args, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
			return fmt.Errorf("docker compose %s failed: %w", step.name, err)
		}
	}
	status := map[string]struct {
		message string
		running bool
	}{
		"start":   {message: "Agent Memory started.", running: true},
		"stop":    {message: "Agent Memory stopped."},
		"restart": {message: "Agent Memory restarted.", running: true},
		"build":   {message: "Agent Memory build complete.", running: true},
	}[operation]
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), status.message)
	if status.running && (enableSaaS || operation == "build") {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Frontend: http://localhost:3100")
	}
	return nil
}

func developmentComposeBaseArgs(root string) []string {
	return []string{
		"compose",
		"-f", filepath.Join(root, developmentBaseComposePath),
		"-f", filepath.Join(root, developmentOverrideComposePath),
	}
}

func upgradeDevelopmentServices(cmd *cobra.Command, sourceRoot string, disabled bool) (upgradeDevelopmentServicesResult, error) {
	if disabled {
		return upgradeDevelopmentServicesResult{Status: "disabled", Reason: "disabled by --no-services-build"}, nil
	}
	sourceRoot = strings.TrimSpace(sourceRoot)
	if sourceRoot == "" {
		return upgradeDevelopmentServicesResult{Status: "unavailable", Reason: "source checkout unavailable; service images were not rebuilt"}, nil
	}
	if !regularFile(filepath.Join(sourceRoot, developmentBaseComposePath)) || !regularFile(filepath.Join(sourceRoot, developmentOverrideComposePath)) {
		return upgradeDevelopmentServicesResult{Status: "unavailable", Reason: "source checkout does not contain the development Compose stack"}, nil
	}
	services, err := probeDevelopmentServices(cmd.Context(), sourceRoot)
	if err != nil {
		return upgradeDevelopmentServicesResult{Status: "unavailable", Reason: "Docker stack inspection unavailable: " + err.Error()}, nil
	}
	apiRunning := false
	for _, service := range services {
		if service == "api" {
			apiRunning = true
			break
		}
	}
	if !apiRunning {
		return upgradeDevelopmentServicesResult{Status: "not_running", Reason: "local API service is not running"}, nil
	}
	if err := executeDevelopmentLifecycle(cmd, sourceRoot, "build", false); err != nil {
		return upgradeDevelopmentServicesResult{Status: "failed", Reason: err.Error()}, err
	}
	return upgradeDevelopmentServicesResult{Status: "rebuilt"}, nil
}

func developmentLifecycleSteps(operation string, enableSaaS bool) ([]developmentLifecycleStep, error) {
	restartInfrastructure := append([]string{"restart"}, developmentInfrastructureServices...)
	waitForInfrastructure := append([]string{"up", "-d", "--wait"}, developmentInfrastructureServices...)
	recreateDependents := append([]string{"up", "-d", "--force-recreate", "--wait"}, developmentDependentServices...)
	switch operation {
	case "start":
		args := []string{"up", "-d", "--build", "--wait", "--remove-orphans"}
		if !enableSaaS {
			args = append(args, "api")
		}
		return []developmentLifecycleStep{{name: "start stack", args: args}}, nil
	case "stop":
		return []developmentLifecycleStep{{name: "stop stack", args: []string{"down"}}}, nil
	case "restart":
		if !enableSaaS {
			return []developmentLifecycleStep{{name: "restart api", args: []string{"restart", "api"}}}, nil
		}
		return []developmentLifecycleStep{
			{name: "restart api", args: []string{"restart", "api"}},
			{name: "restart infrastructure", args: restartInfrastructure},
			{name: "wait for infrastructure", args: waitForInfrastructure},
			{name: "recreate dependent services", args: recreateDependents},
		}, nil
	case "build":
		return []developmentLifecycleStep{
			{name: "build api", args: []string{"build", "api"}},
			{name: "recreate api", args: []string{"up", "-d", "--force-recreate", "--wait", "api"}},
			{name: "restart infrastructure", args: restartInfrastructure},
			{name: "wait for infrastructure", args: waitForInfrastructure},
			{name: "recreate dependent services", args: recreateDependents},
		}, nil
	default:
		return nil, fmt.Errorf("unknown development lifecycle operation %q", operation)
	}
}
