package cli

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/integration"
	"github.com/taimufuraiyaa/agent-memory/internal/jev"
	"github.com/taimufuraiyaa/agent-memory/internal/jevconfig"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

type jevDecisionClient interface {
	Probe(context.Context, string) error
	Choose(context.Context, string, string, map[string]jev.ChoiceQuestion) (map[string]jev.ChoiceAnswer, error)
}

var newJevClient = func() jevDecisionClient { return jev.NewClient() }

func newListenCommand() *cobra.Command {
	var workspaceName, dataDir string
	var withJev bool
	cmd := &cobra.Command{
		Use:   "listen <on|off|status|jev-on|jev-off>",
		Short: "Control opt-in Claude Code prompt context for one registered workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(dataDir) == "" {
				dataDir = defaultAgentMemoryDataDir()
			}
			manager, err := workspace.NewManager(dataDir)
			if err != nil {
				return err
			}
			project, err := manager.Project(workspaceName)
			if err != nil {
				return err
			}
			if strings.TrimSpace(project.WorkspaceRoot) == "" {
				return errors.New("registered workspace has no project root")
			}
			if withJev && args[0] != "on" {
				return errors.New("--jev is only valid with listen on")
			}
			store := integration.NewClaudeListenStore(dataDir)
			var enabled, jevEnabled, jevReady bool
			switch args[0] {
			case "on":
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				// Enabling is a user action in the registered project. The
				// store itself still binds every later hook to that root.
				if !integration.ProjectDirectoryWithin(project.WorkspaceRoot, cwd) {
					return errors.New("run listen on from the registered project")
				}
				if withJev {
					if err := verifyJevAccess(cmd.Context(), dataDir); err != nil {
						return err
					}
				}
				state, err := store.Enable(workspaceName, project.WorkspaceRoot)
				if err != nil {
					return err
				}
				enabled = state.Enabled
				jevEnabled = state.JevEnabled
				if withJev {
					state, err = store.SetJev(workspaceName, project.WorkspaceRoot, true)
					if err != nil {
						return err
					}
					jevEnabled, jevReady = state.JevEnabled, true
				}
			case "off":
				if err := store.Disable(workspaceName); err != nil {
					return err
				}
			case "status":
				state, err := store.Status(workspaceName)
				if err != nil {
					return err
				}
				enabled = state.Enabled && store.Allows(workspaceName, project.WorkspaceRoot, project.WorkspaceRoot)
				jevEnabled = enabled && state.JevEnabled
			case "jev-on":
				cwd, err := os.Getwd()
				if err != nil || !store.Allows(workspaceName, project.WorkspaceRoot, cwd) {
					return errors.New("enable local listen from the registered project before Jev")
				}
				if err := verifyJevAccess(cmd.Context(), dataDir); err != nil {
					return err
				}
				state, err := store.SetJev(workspaceName, project.WorkspaceRoot, true)
				if err != nil {
					return err
				}
				enabled, jevEnabled, jevReady = state.Enabled, state.JevEnabled, true
			case "jev-off":
				state, err := store.SetJev(workspaceName, project.WorkspaceRoot, false)
				if err != nil {
					return err
				}
				enabled = state.Enabled
			default:
				return errors.New("expected on, off, status, jev-on, or jev-off")
			}
			if args[0] == "status" && jevEnabled {
				if token, configured, err := jevconfig.NewTokenStore(dataDir).Load(cmd.Context()); err == nil && configured {
					jevReady = newJevClient().Probe(cmd.Context(), token) == nil
				}
			}
			mode := "memory_context"
			if jevReady {
				mode = "memory_context+jev_advisory"
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "listen", map[string]any{
				"workspace": workspaceName, "enabled": enabled, "jev_enabled": jevEnabled,
				"mode": mode, "jev_decisions_ready": jevReady,
				"model_switch_supported": false, "skill_auto_load_supported": false,
			})
		},
	}
	cmd.Flags().StringVarP(&workspaceName, "workspace", "w", "", "Registered workspace name")
	cmd.Flags().StringVar(&dataDir, "data-dir", "", "Agent Memory data directory")
	cmd.Flags().BoolVar(&withJev, "jev", false, "Also enable Jev advisory (explicit remote prompt opt-in; listen on only)")
	return cmd
}

func verifyJevAccess(ctx context.Context, dataDir string) error {
	token, configured, err := jevconfig.NewTokenStore(dataDir).Load(ctx)
	if err != nil || !configured {
		return errors.New("configure a TypeSafe Jev access token first")
	}
	if err := newJevClient().Probe(ctx, token); err != nil {
		return errors.New("TypeSafe Jev model access could not be verified")
	}
	return nil
}
