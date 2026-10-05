package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessapproval"
	"github.com/taimufuraiyaa/agent-memory/internal/harnesstools"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

// harnessSavedDir is where the edit tools keep a private copy of each file before they
// change it, so an applied change can be undone.
func harnessSavedDir(dataDir string) string { return filepath.Join(dataDir, "harness", "saved") }

// interactive reports whether both ends are a terminal. It is a variable so a test can
// stand in for one; production code never sets it.
var interactive = func(in io.Reader, out io.Writer) bool {
	inFile, inOK := in.(*os.File)
	outFile, outOK := out.(*os.File)
	return inOK && outOK && term.IsTerminal(inFile.Fd()) && term.IsTerminal(outFile.Fd())
}

func resolveDataDir(dataDir string) string {
	if strings.TrimSpace(dataDir) == "" {
		return defaultAgentMemoryDataDir()
	}
	return dataDir
}

// newHarnessApprovalsCommand is the trusted local channel for deciding what a coding run may
// change. A decision is made here, at a terminal, by a person who has read the change; no
// bearer grant, route or MCP tool can make one. There is deliberately no flag that supplies
// the code or answers the prompt, and no way to approve more than one action at a time.
func newHarnessApprovalsCommand(dataDir *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approvals",
		Short: "Review and decide what a coding run asks to change",
		Long: "A coding run that wants to change a file stops and asks. Review the exact change here and approve it by " +
			"typing the short code shown next to it, or deny it. Approval needs an interactive terminal, covers exactly one " +
			"action, and only applies to the file as it was when you reviewed it.",
	}
	cmd.AddCommand(newApprovalsListCommand(dataDir), newApprovalsShowCommand(dataDir), newApprovalsApproveCommand(dataDir),
		newApprovalsDenyCommand(dataDir), newApprovalsUndoCommand(dataDir), newApprovalsAuditCommand(dataDir))
	return cmd
}

type approvalSummary struct {
	ID        string    `json:"id"`
	Run       string    `json:"run"`
	Workspace string    `json:"workspace"`
	Client    string    `json:"client"`
	Tool      string    `json:"tool"`
	Summary   string    `json:"summary"`
	Paths     []string  `json:"paths"`
	Friction  string    `json:"friction"`
	Reasons   []string  `json:"reasons,omitempty"`
	State     string    `json:"state"`
	Outcome   string    `json:"outcome,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func summarize(r harnessapproval.Record) approvalSummary {
	return approvalSummary{ID: r.ID, Run: r.RunID, Workspace: r.Owner.Workspace, Client: r.Owner.ClientID, Tool: r.Tool, Summary: safeText(r.Summary),
		Paths: safePaths(r.Paths), Friction: string(r.Friction), Reasons: r.Reasons, State: string(r.State), Outcome: r.Outcome, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}
}

// safeText removes anything that could move the cursor or rewrite the screen. Records are
// validated when they are written, so this is a second line of defence for a person's terminal.
func safeText(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r == unicode.ReplacementChar {
			return -1
		}
		return r
	}, strings.ToValidUTF8(value, ""))
}

func safePaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = strings.ReplaceAll(safeText(p), "\n", " ")
	}
	return out
}

func newApprovalsListCommand(dataDir *string) *cobra.Command {
	var workspaceName string
	var all bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List approvals waiting for a decision (--all includes decided ones)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store := harnessapproval.Open(resolveDataDir(*dataDir))
			records, err := store.List(cmd.Context(), harnessapproval.ListOptions{Workspace: workspaceName, LiveOnly: !all})
			if err != nil {
				return err
			}
			out := make([]approvalSummary, len(records))
			for i, record := range records {
				out[i] = summarize(record)
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "harness.approvals.list", map[string]any{"approvals": out})
		},
	}
	cmd.Flags().StringVarP(&workspaceName, "workspace", "w", "", "Only approvals for this workspace")
	cmd.Flags().BoolVar(&all, "all", false, "Include approvals that were already decided or ended")
	return cmd
}

// review writes what a person needs to judge one action: who is asking, what, why it is
// being treated carefully, when it lapses, and the exact change.
func review(out io.Writer, r harnessapproval.Record) {
	_, _ = fmt.Fprintf(out, "\nApproval %s  (%s)\n", r.ID, r.State)
	_, _ = fmt.Fprintf(out, "  Action    %s\n", safeText(r.Summary))
	_, _ = fmt.Fprintf(out, "  Tool      %s\n", r.Tool)
	_, _ = fmt.Fprintf(out, "  Workspace %s   Client %s   Run %s\n", r.Owner.Workspace, r.Owner.ClientID, r.RunID)
	_, _ = fmt.Fprintf(out, "  Files     %s\n", strings.Join(safePaths(r.Paths), ", "))
	if r.Friction == harnessapproval.FrictionStrict {
		_, _ = fmt.Fprintf(out, "  CARE      this change needs extra care: %s\n", strings.Join(r.Reasons, ", "))
	}
	_, _ = fmt.Fprintf(out, "  Expires   %s\n\n", r.ExpiresAt.Local().Format("15:04:05"))
	_, _ = fmt.Fprintln(out, safeText(r.Preview))
}

func newApprovalsShowCommand(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "show <approval-id>",
		Short: "Show the exact change an approval would allow",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := harnessapproval.Open(resolveDataDir(*dataDir))
			record, err := store.Get(cmd.Context(), args[0])
			if err != nil {
				return friendlyApprovalError(err)
			}
			review(cmd.OutOrStdout(), record)
			return nil
		},
	}
}

func friendlyApprovalError(err error) error {
	switch {
	case errors.Is(err, harnessapproval.ErrNotFound):
		return errors.New("no such approval")
	case errors.Is(err, harnessapproval.ErrExpired):
		return errors.New("that approval has expired; the run was told, and can ask again")
	case errors.Is(err, harnessapproval.ErrNotLive):
		return errors.New("that approval is no longer waiting for a decision")
	}
	return err
}

func newApprovalsApproveCommand(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "approve <approval-id>",
		Short: "Review one change and approve it by typing its code (interactive terminal only)",
		Long: "Shows the exact change, then asks you to type the short code printed beneath it. The code is not accepted from a flag, " +
			"an environment variable or a pipe, and one approval covers exactly one action.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !interactive(cmd.InOrStdin(), cmd.OutOrStdout()) {
				return errors.New("approving needs an interactive terminal; run this command yourself in a terminal")
			}
			store := harnessapproval.Open(resolveDataDir(*dataDir))
			record, err := store.Get(cmd.Context(), args[0])
			if err != nil {
				return friendlyApprovalError(err)
			}
			if record.State != harnessapproval.StatePending {
				return friendlyApprovalError(harnessapproval.ErrNotLive)
			}
			out := cmd.OutOrStdout()
			review(out, record)
			if record.Friction == harnessapproval.FrictionStrict {
				_, _ = fmt.Fprintln(out, "This change touches something that steers how the project is built, run or deployed, or removes a file.")
				_, _ = fmt.Fprintln(out, "Read it again before you type the code.")
			}
			_, _ = fmt.Fprintf(out, "To approve, type  %s  and press Enter. To refuse, press Enter alone or type deny: ", harnessapproval.Code(record))
			typed, err := readLine(cmd.InOrStdin())
			if err != nil {
				return errors.New("no answer was given; nothing was approved")
			}
			typed = strings.TrimSpace(typed)
			if typed == "" || strings.EqualFold(typed, "deny") || strings.EqualFold(typed, "no") || strings.EqualFold(typed, "n") {
				return writeSuccessEnvelope(out, "harness.approvals.approve", map[string]any{"id": record.ID, "decided": false, "state": string(record.State)})
			}
			decided, err := store.Decide(cmd.Context(), record.ID, harnessapproval.Decision{Approve: true, Code: typed, Via: "terminal"})
			if err != nil {
				if errors.Is(err, harnessapproval.ErrCode) {
					left := harnessapproval.MaxCodeFailures - decided.CodeFailures
					if decided.State == harnessapproval.StateDenied {
						return errors.New("the code did not match and the approval was denied after too many wrong codes")
					}
					return fmt.Errorf("the code did not match; nothing was approved (%d attempts left)", left)
				}
				return friendlyApprovalError(err)
			}
			return writeSuccessEnvelope(out, "harness.approvals.approve", map[string]any{"id": decided.ID, "decided": true, "state": string(decided.State)})
		},
	}
}

// readLine reads one line and never more than 256 bytes of input, keeping at most 64, so a
// stream cannot be used to flood the prompt or exhaust memory.
func readLine(in io.Reader) (string, error) {
	line, err := bufio.NewReaderSize(io.LimitReader(in, 256), 256).ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	if len(line) > 64 {
		line = line[:64]
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func newApprovalsDenyCommand(dataDir *string) *cobra.Command {
	var stop bool
	cmd := &cobra.Command{
		Use:   "deny <approval-id>",
		Short: "Refuse one change; the run is told and can try something else (--stop ends the run)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store := harnessapproval.Open(resolveDataDir(*dataDir))
			decided, err := store.Decide(cmd.Context(), args[0], harnessapproval.Decision{Stop: stop, Via: "terminal"})
			if err != nil {
				return friendlyApprovalError(err)
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "harness.approvals.deny", map[string]any{"id": decided.ID, "state": string(decided.State), "stop": decided.Stop})
		},
	}
	cmd.Flags().BoolVar(&stop, "stop", false, "Also end the run")
	return cmd
}

func newApprovalsUndoCommand(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "undo <approval-id>",
		Short: "Restore the file an applied change replaced, if it is still as that change left it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !interactive(cmd.InOrStdin(), cmd.OutOrStdout()) {
				return errors.New("undoing needs an interactive terminal; run this command yourself in a terminal")
			}
			dir := resolveDataDir(*dataDir)
			store := harnessapproval.Open(dir)
			record, err := store.Get(cmd.Context(), args[0])
			if err != nil {
				return friendlyApprovalError(err)
			}
			if record.State != harnessapproval.StateConsumed || record.Outcome != "applied" {
				return errors.New("only a change that was applied can be undone")
			}
			manager, err := workspace.NewManager(dir)
			if err != nil {
				return err
			}
			root, err := managerDirectory{manager: manager}.Root(record.Owner.Workspace)
			if err != nil {
				return errors.New("the project this change was applied to is no longer registered")
			}
			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "\nThis restores %s to what it was before: %s\n", strings.Join(safePaths(record.Paths), ", "), safeText(record.Summary))
			_, _ = fmt.Fprint(out, "It is refused if the file has changed since. Type  undo  to continue: ")
			answer, err := readLine(cmd.InOrStdin())
			if err != nil || !strings.EqualFold(strings.TrimSpace(answer), "undo") {
				return errors.New("nothing was undone")
			}
			result, err := harnesstools.Undo(harnessSavedDir(dir), root, record.Digest, time.Now())
			if err != nil {
				switch {
				case errors.Is(err, harnesstools.ErrNotRestorable):
					return errors.New("the file changed after that action, so it was left alone")
				case errors.Is(err, harnesstools.ErrUndone):
					return errors.New("that action was already undone")
				case errors.Is(err, harnesstools.ErrNoPreimage):
					return errors.New("no saved copy exists for that action")
				}
				return err
			}
			_ = store.Note(cmd.Context(), record.ID, "undone", "")
			return writeSuccessEnvelope(out, "harness.approvals.undo", map[string]any{"id": record.ID, "path": result.Path, "tool": result.Tool})
		},
	}
}

func newApprovalsAuditCommand(dataDir *string) *cobra.Command {
	return &cobra.Command{
		Use:   "audit",
		Short: "Verify the tamper-evident log of approval requests, decisions and outcomes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store := harnessapproval.Open(resolveDataDir(*dataDir))
			count, err := store.VerifyAudit(cmd.Context())
			if err != nil {
				return err
			}
			return writeSuccessEnvelope(cmd.OutOrStdout(), "harness.approvals.audit", map[string]any{"verified": true, "lines": count})
		},
	}
}
