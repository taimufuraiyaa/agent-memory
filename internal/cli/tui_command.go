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
	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	memorytui "github.com/taimufuraiyaa/agent-memory/internal/tui"
)

type tuiCommandDependencies struct {
	isTerminal  func(any) bool
	open        func(context.Context, runtimeConfig) (*sqlite.Store, embeddings.Provider, error)
	run         func(context.Context, io.Reader, io.Writer, memorytui.Model) (tea.Model, error)
	credentials func() memorytui.JevCredentialStore
	console     func(context.Context, runtimeConfig, embeddings.Provider) (memorytui.ConsoleBackend, func() error, error)
	historyDir  func(runtimeConfig) string
}

func defaultTUICommandDependencies() tuiCommandDependencies {
	return tuiCommandDependencies{
		isTerminal: func(stream any) bool {
			file, ok := stream.(*os.File)
			return ok && term.IsTerminal(file.Fd())
		},
		open:       openDeps,
		console:    newLocalTUIConsole,
		historyDir: tuiHistoryDirectory,
		credentials: func() memorytui.JevCredentialStore {
			return jevconfig.NewTokenStore(defaultAgentMemoryDataDir())
		},
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
		Long:  "Open an Agent Memory workspace in the Bubble Tea terminal interface. Local mode calls Go services directly; --api connects to a headless backend.",
		RunE: func(command *cobra.Command, _ []string) error {
			config, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			input := command.InOrStdin()
			output := command.OutOrStdout()
			if dependencies.isTerminal == nil || !dependencies.isTerminal(input) || !dependencies.isTerminal(output) {
				return errors.New("the TUI requires an interactive terminal for both input and output")
			}
			if dependencies.run == nil {
				return errors.New("the TUI runtime is not configured")
			}
			ctx, cancel := context.WithCancel(command.Context())
			defer cancel()
			var credentials memorytui.JevCredentialStore
			if dependencies.credentials != nil {
				credentials = dependencies.credentials()
			}
			if strings.TrimSpace(config.apiURL) != "" {
				backend, err := memorytui.NewHTTPBackend(config.apiURL, config.workspace, nil)
				if err != nil {
					return err
				}
				model, err := tuiModelWithHistory(ctx, config, backend, credentials, dependencies.historyDir)
				if err != nil {
					return err
				}
				_, err = dependencies.run(ctx, input, output, model)
				return err
			}
			if dependencies.open == nil {
				return errors.New("the local TUI runtime is not configured")
			}
			store, provider, err := dependencies.open(ctx, config)
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
			defer store.Close()
			var closeConsole func() error
			if dependencies.console != nil {
				backend.console, closeConsole, err = dependencies.console(ctx, config, provider)
				if err != nil {
					return err
				}
			}
			model, err := tuiModelWithHistory(ctx, config, backend, credentials, dependencies.historyDir)
			if err != nil {
				if closeConsole != nil {
					_ = closeConsole()
				}
				return err
			}
			_, runErr := dependencies.run(ctx, input, output, model)
			cancel()
			backend.shutdown()
			if closeConsole != nil {
				if err := closeConsole(); runErr == nil {
					runErr = err
				}
			}
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
