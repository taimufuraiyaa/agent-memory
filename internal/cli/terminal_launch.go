package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type terminalLaunchDependencies struct {
	tui     tuiCommandDependencies
	names   func() ([]string, error)
	pick    func(*cobra.Command, []string) (string, error)
	rebuild func(context.Context, string) (string, error)
	launch  func(*cobra.Command, string, []string) error
}

func defaultTerminalLaunchDependencies() terminalLaunchDependencies {
	return terminalLaunchDependencies{
		tui: defaultTUICommandDependencies(),
		names: func() ([]string, error) {
			manager, err := workspace.NewManager(defaultAgentMemoryDataDir())
			if err != nil {
				return nil, err
			}
			return manager.ProjectNames()
		},
		pick:    pickTerminalWorkspace,
		rebuild: rebuildInstalledTerminal,
		launch: func(command *cobra.Command, binary string, args []string) error {
			process := exec.CommandContext(command.Context(), binary, args...)
			process.Stdin, process.Stdout, process.Stderr = command.InOrStdin(), command.OutOrStdout(), command.ErrOrStderr()
			return process.Run()
		},
	}
}

func newTerminalLaunchCommand(restart bool, dependencies terminalLaunchDependencies) *cobra.Command {
	command := newTUICommandWithDependencies(dependencies.tui)
	runTUI := command.RunE
	command.Use = "start"
	command.Short = "Open the terminal workspace (choose a workspace if omitted)"
	command.Long = "Open the Go terminal interface. Use --workspace to open a specific workspace, or choose a registered local workspace interactively."
	command.Args = cobra.NoArgs
	var source string
	if restart {
		command.Use = "restart"
		command.Short = "Rebuild both installed binaries and open the updated terminal workspace"
		command.Long = "Rebuild Agent Memory from its source checkout, update agent-memory and am, then open the rebuilt terminal interface. Close your current TUI before running restart."
		command.Flags().StringVar(&source, "source-dir", "", "Agent Memory source checkout (default: existing upgrade source discovery)")
	}
	command.RunE = func(command *cobra.Command, args []string) error {
		if dependencies.tui.isTerminal == nil || !dependencies.tui.isTerminal(command.InOrStdin()) || !dependencies.tui.isTerminal(command.OutOrStdout()) {
			return errors.New("the TUI requires an interactive terminal for both input and output")
		}
		selected, _ := command.Flags().GetString("workspace")
		if !command.Flags().Changed("workspace") {
			api, _ := command.Flags().GetString("api")
			if resolveAPIURL(api) != "" {
				return errors.New("--workspace is required when starting the TUI with an API backend")
			}
			names, err := dependencies.names()
			if err != nil {
				return fmt.Errorf("list registered workspaces: %w", err)
			}
			if len(names) == 0 {
				return errors.New("no registered workspaces; register one with agent-memory init first")
			}
			selected, err = dependencies.pick(command, names)
			if errors.Is(err, errWorkspaceSelectionCancelled) {
				return nil
			}
			if err != nil {
				return err
			}
			found := false
			for _, name := range names {
				if name == selected {
					found = true
					break
				}
			}
			if !found {
				return errors.New("selected workspace is not registered")
			}
			if err := command.Flags().Set("workspace", selected); err != nil {
				return err
			}
		}
		if strings.TrimSpace(selected) == "" {
			return errors.New("--workspace must name a workspace")
		}
		if _, err := resolveWorkspace(selected); err != nil {
			return err
		}
		if !restart {
			return runTUI(command, args)
		}
		_, _ = fmt.Fprintln(command.ErrOrStderr(), "Rebuilding agent-memory and am...")
		binary, err := dependencies.rebuild(command.Context(), source)
		if err != nil {
			return err
		}
		forward := []string{"tui", "--workspace", selected}
		for _, name := range []string{"db", "model-dir", "api"} {
			value, _ := command.Flags().GetString(name)
			if value != "" {
				forward = append(forward, "--"+name, value)
			}
		}
		_, _ = fmt.Fprintln(command.ErrOrStderr(), "Both binaries updated. Opening the rebuilt TUI...")
		return dependencies.launch(command, binary, forward)
	}
	return command
}

func rebuildInstalledTerminal(ctx context.Context, source string) (string, error) {
	if strings.TrimSpace(source) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		source = findSourceRoot(cwd)
	}
	root, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	if source == "" {
		return "", errors.New("Agent Memory source checkout not found; pass --source-dir")
	}
	if err := validateUpgradeSource(root); err != nil {
		return "", err
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	target := canonicalUpgradeTarget(executable)
	if filepath.Base(target) != binNameWithExt("agent-memory") {
		return "", errors.New("restart must run from an installed agent-memory or am executable")
	}
	buildDirectory, err := os.MkdirTemp("", "agent-memory-terminal-rebuild-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(buildDirectory)
	built := filepath.Join(buildDirectory, binNameWithExt("agent-memory"))
	buildContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	process := exec.CommandContext(buildContext, "go", "build", "-trimpath", "-o", built, "./cmd/agent-memory")
	process.Dir = root
	_, stderr, err := runAndCapture(process)
	if err != nil {
		if buildContext.Err() != nil {
			return "", fmt.Errorf("terminal rebuild cancelled: %w", buildContext.Err())
		}
		if len(stderr) > 8192 {
			stderr = stderr[len(stderr)-8192:]
		}
		return "", fmt.Errorf("terminal rebuild failed: %w\n%s", err, strings.TrimSpace(stderr))
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := publishTerminalBinaries(target, built, replaceFileAtomic); err != nil {
		return "", err
	}
	return target, nil
}

func publishTerminalBinaries(canonical, built string, replace func(string, string) error) error {
	backupDirectory, err := os.MkdirTemp(filepath.Dir(canonical), ".agent-memory-restart-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(backupDirectory)
	backup := filepath.Join(backupDirectory, "previous")
	if err := replace(backup, canonical); err != nil {
		return fmt.Errorf("back up installed executable: %w", err)
	}
	if err := replace(canonical, built); err != nil {
		return fmt.Errorf("update agent-memory: %w", err)
	}
	alias := filepath.Join(filepath.Dir(canonical), binNameWithExt("am"))
	if err := replace(alias, built); err != nil {
		if rollbackErr := replace(canonical, backup); rollbackErr != nil {
			return fmt.Errorf("update am failed: %v; agent-memory rollback failed: %w", err, rollbackErr)
		}
		return fmt.Errorf("update am failed; agent-memory restored: %w", err)
	}
	return nil
}
