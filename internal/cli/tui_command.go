package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	memorytui "github.com/taimufuraiyaa/agent-memory/internal/tui"
)

type tuiCommandDependencies struct {
	isTerminal func(any) bool
	open       func(context.Context, runtimeConfig) (*sqlite.Store, embeddings.Provider, error)
	run        func(context.Context, io.Reader, io.Writer, memorytui.Model) (tea.Model, error)
}

func defaultTUICommandDependencies() tuiCommandDependencies {
	return tuiCommandDependencies{
		isTerminal: func(stream any) bool {
			file, ok := stream.(*os.File)
			return ok && term.IsTerminal(file.Fd())
		},
		open: openDeps,
		run: func(ctx context.Context, input io.Reader, output io.Writer, model memorytui.Model) (tea.Model, error) {
			return tea.NewProgram(
				model,
				tea.WithContext(ctx),
				tea.WithInput(input),
				tea.WithOutput(output),
			).Run()
		},
	}
}

func newTUICommand() *cobra.Command {
	return newTUICommandWithDependencies(defaultTUICommandDependencies())
}

func newTUICommandWithDependencies(dependencies tuiCommandDependencies) *cobra.Command {
	var flags commonFlags
	command := &cobra.Command{
		Use:   "tui",
		Short: "Open the interactive terminal workspace",
		Long:  "Open a local Agent Memory workspace in a keyboard-driven Bubble Tea terminal interface.",
		RunE: func(command *cobra.Command, _ []string) error {
			config, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if strings.TrimSpace(config.apiURL) != "" {
				return errors.New("the TUI currently supports local workspaces only; omit --api")
			}
			input := command.InOrStdin()
			output := command.OutOrStdout()
			if dependencies.isTerminal == nil || !dependencies.isTerminal(input) || !dependencies.isTerminal(output) {
				return errors.New("the TUI requires an interactive terminal for both input and output")
			}
			if dependencies.open == nil || dependencies.run == nil {
				return errors.New("the TUI runtime is not configured")
			}
			store, provider, err := dependencies.open(command.Context(), config)
			if err != nil {
				return err
			}
			if store == nil || provider == nil {
				if store != nil {
					_ = store.Close()
				}
				return errors.New("the TUI runtime returned incomplete dependencies")
			}
			backend := newLocalTUIBackend(store, provider, config.workspace)
			model := memorytui.NewModelWithContext(command.Context(), config.workspace, backend)
			_, runErr := dependencies.run(command.Context(), input, output, model)
			closeErr := store.Close()
			if runErr != nil {
				return runErr
			}
			return closeErr
		},
	}
	addCommonFlags(command, &flags)
	return command
}
