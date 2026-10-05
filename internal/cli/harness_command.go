package cli

import (
	"errors"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessauth"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

// managerDirectory resolves registered workspaces for grant binding and verification.
type managerDirectory struct{ manager *workspace.Manager }

func (d managerDirectory) Root(name string) (string, error) {
	project, err := d.manager.Project(name)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(project.WorkspaceRoot) == "" {
		return "", errors.New("registered workspace has no project root")
	}
	return project.WorkspaceRoot, nil
}

func openHarnessAuthority(dataDir string) (*harnessauth.Authority, error) {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = defaultAgentMemoryDataDir()
	}
	clients, err := clientprofile.Open(dataDir, nil)
	if err != nil {
		return nil, err
	}
	manager, err := workspace.NewManager(dataDir)
	if err != nil {
		return nil, err
	}
	return harnessauth.New(dataDir, harnessauth.Options{Clients: clients, Workspaces: managerDirectory{manager: manager}})
}

// newHarnessCommand manages client authority for the local coding harness. These are
// trusted local operations: a credential is minted here and never through MCP, and a
// grant can never approve a change. Approving is a separate act, done at a terminal with
// "harness approvals".
func newHarnessCommand() *cobra.Command {
	var dataDir string
	cmd := &cobra.Command{
		Use:   "harness",
		Short: "Manage local coding-harness client authority",
		Long: "Manage the opt-in capability pack and the bearer grants that let a registered MCP client use the " +
			"local coding harness. Grants are bound to a client, registered workspaces, an operation allowlist and an expiry. " +
			"Run these from a trusted local terminal.",
	}
	cmd.PersistentFlags().StringVar(&dataDir, "data-dir", "", "Agent Memory data directory")
	cmd.AddCommand(newHarnessAuthorityCommand(&dataDir), newHarnessGrantCommand(&dataDir), newHarnessApprovalsCommand(&dataDir))
	return cmd
}

func newHarnessAuthorityCommand(dataDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:       "authority <status|enable|disable>",
		Short:     "Show or switch the opt-in harness capability pack",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"status", "enable", "disable"},
		RunE: func(cmd *cobra.Command, args []string) error {
			authority, err := openHarnessAuthority(*dataDir)
			if err != nil {
				return err
			}
			switch args[0] {
			case "enable":
				err = authority.SetEnabled(cmd.Context(), true)
			case "disable":
				err = authority.SetEnabled(cmd.Context(), false)
			case "status":
			default:
				return errors.New("expected status, enable, or disable")
			}
			if err != nil {
				return err
			}
			status, err := authority.Status()
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "harness.authority", status)
		},
	}
	return cmd
}

func newHarnessGrantCommand(dataDir *string) *cobra.Command {
	cmd := &cobra.Command{Use: "grant", Short: "Create, list, rotate and revoke harness grants"}

	var client string
	var workspaces, operations []string
	var ttl time.Duration
	create := &cobra.Command{
		Use:   "create",
		Short: "Mint a grant for a registered client and workspaces; the token is shown once",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			authority, err := openHarnessAuthority(*dataDir)
			if err != nil {
				return err
			}
			ops := make([]harnessauth.Operation, 0, len(operations))
			for _, name := range operations {
				operation, err := harnessauth.ParseOperation(name)
				if err != nil {
					return err
				}
				ops = append(ops, operation)
			}
			token, grant, err := authority.Mint(cmd.Context(), harnessauth.MintRequest{ClientID: client, Workspaces: workspaces, Operations: ops, TTL: ttl})
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "harness.grant.create", map[string]any{
				"grant": grant, "token": token, "token_shown_once": true,
			})
		},
	}
	create.Flags().StringVar(&client, "client", "", "Registered client profile ID")
	create.Flags().StringSliceVarP(&workspaces, "workspace", "w", nil, "Registered workspace the grant covers (repeatable)")
	create.Flags().StringSliceVar(&operations, "operation", nil, "Operation to grant: capabilities, start, status, continue, artifact, cancel (repeatable)")
	create.Flags().DurationVar(&ttl, "ttl", harnessauth.DefaultTTL, "Grant lifetime")
	requireExplicitWorkspace(create)

	list := &cobra.Command{
		Use:   "list",
		Short: "List grants without secrets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			authority, err := openHarnessAuthority(*dataDir)
			if err != nil {
				return err
			}
			grants, err := authority.List()
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "harness.grant.list", map[string]any{"grants": grants})
		},
	}

	var rotateTTL time.Duration
	rotate := &cobra.Command{
		Use:   "rotate <grant-id>",
		Short: "Replace a grant's secret; the previous token stops working immediately",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			authority, err := openHarnessAuthority(*dataDir)
			if err != nil {
				return err
			}
			token, grant, err := authority.Rotate(cmd.Context(), args[0], rotateTTL)
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "harness.grant.rotate", map[string]any{
				"grant": grant, "token": token, "token_shown_once": true,
			})
		},
	}
	rotate.Flags().DurationVar(&rotateTTL, "ttl", 0, "New lifetime from now (default keeps the current expiry)")

	revoke := &cobra.Command{
		Use:   "revoke <grant-id>",
		Short: "Permanently revoke a grant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			authority, err := openHarnessAuthority(*dataDir)
			if err != nil {
				return err
			}
			grant, err := authority.Revoke(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "harness.grant.revoke", map[string]any{"grant": grant})
		},
	}

	cmd.AddCommand(create, list, rotate, revoke)
	return cmd
}
