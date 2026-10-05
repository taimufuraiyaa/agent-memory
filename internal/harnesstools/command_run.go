package harnesstools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessexec"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessfs"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessproof"
)

var plainArgRE = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// displayCommand writes an argument vector the way a person would type it, quoting only what
// needs it. It is for reading: the command is never run from this string.
func displayCommand(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a != "" && plainArgRE.MatchString(a) {
			parts[i] = a
		} else {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}

// commandPreview is what a person reads before approving: exactly what will run, where, with
// what limits and environment, and an honest statement of what is not being promised.
func commandPreview(p *commandPlan, maxOutput int) string {
	argvJSON, _ := json.Marshal(p.argv)
	var b strings.Builder
	b.WriteString("RUN COMMAND  (no shell: every argument is passed exactly as shown)\n")
	fmt.Fprintf(&b, "  command      %s\n", visible(displayCommand(p.argv)))
	fmt.Fprintf(&b, "  argv         %s\n", visible(string(argvJSON)))
	fmt.Fprintf(&b, "  program      %s\n", visible(p.program))
	fmt.Fprintf(&b, "  directory    %s   (inside the project)\n", visible(p.dir))
	fmt.Fprintf(&b, "  limits       %s of run time, %d KiB of output kept\n", p.timeout, maxOutput>>10)
	if p.entry != "" {
		fmt.Fprintf(&b, "  recognized   %s: %s\n", p.entry, p.describe)
	} else {
		b.WriteString("  recognized   NOT A KNOWN TEST, BUILD OR LINT COMMAND: read the line above carefully\n")
	}
	for _, reason := range p.escalate {
		if text, ok := commandReasons[reason]; ok {
			fmt.Fprintf(&b, "  care         %s\n", text)
		}
	}
	b.WriteString("  environment  built, not inherited:\n")
	for _, kv := range p.env {
		fmt.Fprintf(&b, "                 %s\n", visible(kv))
	}
	b.WriteString("  not promised this command is not sandboxed: it runs as you and can read and change anything you can,\n")
	b.WriteString("               and the settings above only discourage network use. What it changes in the project is reported afterwards.\n")
	return b.String()
}

var commandReasons = map[string]string{
	"not_cataloged":   "this is not one of the recognized toolchain commands",
	"project_program": "this runs a script that lives in the project",
	"runs_recipes":    "this runs a recipe defined in the project's own files",
	"inline_code":     "an interpreter is being given program text on its command line",
}

func (s *session) prepareCommand(q harness.ToolRequest, root string, action harness.PreparedAction) harness.PreparedAction {
	fail := func(outcome harness.Outcome, reason string) harness.PreparedAction {
		action.Outcome, action.Reason = outcome, reason
		return action
	}
	cfg := s.provider.cfg.Command
	if !cfg.Enabled || !harnessexec.Supported() {
		return fail(harness.OutcomeUnsupported, "")
	}
	project, err := harnessfs.Open(root)
	if err != nil {
		return fail(harness.OutcomeUnavailable, "")
	}
	defer project.Close()
	plan, outcome, reason := planCommand(project, root, cfg, q.Arguments)
	if outcome != harness.OutcomeOK {
		return fail(outcome, reason)
	}
	preview := commandPreview(plan, cfg.maxOutput())
	if !previewFits(preview) {
		return fail(harness.OutcomeFailed, "change_too_large")
	}
	digest := plan.digest(root)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) >= maxPending || s.pendingMutations() >= maxPendingMutations {
		return fail(harness.OutcomeFailed, "busy")
	}
	s.pending[digest] = call{tool: q.ToolID, root: root, command: plan}
	action.Outcome, action.Digest = harness.OutcomeOK, digest
	action.Summary = fmt.Sprintf("run %s (in %s)", visible(displayCommand(plan.argv)), plan.dir)
	if len(action.Summary) > 240 {
		action.Summary = action.Summary[:230] + "…"
	}
	action.Paths, action.Preview, action.Escalate = []string{plan.dir}, preview, plan.escalate
	return action
}

// pendingMutations counts prepared calls that would change something. The caller holds the lock.
func (s *session) pendingMutations() int {
	n := 0
	for _, c := range s.pending {
		if c.plan != nil || c.command != nil {
			n++
		}
	}
	return n
}

// programStillTheSame rechecks the executable against what was reviewed.
func programStillTheSame(project *harnessfs.Root, p *commandPlan) bool {
	if p.has("project_program") {
		rel, err := filepath.Rel(p.realRoot, p.program)
		if err != nil {
			return false
		}
		_, revision, _, err := project.ReadWhole(filepath.ToSlash(rel))
		return err == nil && fmt.Sprintf("%s|%s", p.program, revision) == p.identity
	}
	identity, err := identityOf(p.program)
	return err == nil && identity == p.identity
}

// execute runs one prepared, approved command. It refuses unless the context carries the
// manager's proof for exactly this digest and the program is still what was reviewed.
func (s *session) execute(ctx context.Context, project *harnessfs.Root, prepared call, action harness.PreparedAction) ([]byte, harness.Outcome, []string) {
	if action.Digest == "" || harnessproof.Approved(ctx) != action.Digest {
		return nil, harness.OutcomeDenied, nil
	}
	cfg := s.provider.cfg.Command
	plan := prepared.command
	if !cfg.Enabled || !harnessexec.Supported() {
		return nil, harness.OutcomeUnavailable, nil
	}
	if !programStillTheSame(project, plan) {
		return nil, harness.OutcomeStale, nil
	}
	if err := privateDir(cfg.TempRoot); err != nil {
		return nil, harness.OutcomeFailed, nil
	}
	temp, err := os.MkdirTemp(cfg.TempRoot, "cmd-")
	if err != nil {
		return nil, harness.OutcomeFailed, nil
	}
	defer os.RemoveAll(temp)
	env := append(append([]string(nil), plan.env...), "TMPDIR="+temp)

	before, beforeErr := harnessexec.Snapshot(plan.realRoot)
	result, err := harnessexec.Run(ctx, harnessexec.Spec{Program: plan.program, Args: plan.argv[1:], Dir: plan.realDir, Env: env, Timeout: plan.timeout, MaxOutput: cfg.maxOutput()})
	if err != nil {
		if errors.Is(err, harnessexec.ErrUnsupported) {
			return nil, harness.OutcomeUnavailable, nil
		}
		return nil, harness.OutcomeFailed, nil
	}
	if result.Cancelled {
		return nil, harness.OutcomeCancelled, nil
	}
	var change *harnessexec.Change
	if beforeErr == nil {
		if after, err := harnessexec.Snapshot(plan.realRoot); err == nil {
			c := harnessexec.Diff(before, after)
			change = &c
		}
	}
	text := s.formatCommandResult(plan, result, change, action.MaxBytes)
	var audit []string
	if change != nil && len(change.Protected) > 0 {
		audit = append(audit, "protected_changed")
	}
	if result.TimedOut {
		audit = append(audit, "timed_out")
	}
	outcome := harness.OutcomeOK
	if result.TimedOut || result.OmittedBytes > 0 || result.Incomplete {
		outcome = harness.OutcomePartial
	}
	return []byte(text), outcome, audit
}

func fitHeadTail(text string, max int) string {
	if len(text) <= max {
		return text
	}
	if max < 64 {
		return text[:max]
	}
	marker := "\n…[output shortened to fit]…\n"
	room := max - len(marker)
	head := room / 3
	return text[:head] + marker + text[len(text)-(room-head):]
}

func (s *session) formatCommandResult(plan *commandPlan, r harnessexec.Result, change *harnessexec.Change, maxBytes int) string {
	var head strings.Builder
	fmt.Fprintf(&head, "$ %s   (in %s)\n", displayCommand(plan.argv), plan.dir)
	switch {
	case r.TimedOut:
		fmt.Fprintf(&head, "timed out after %s and was stopped\n", plan.timeout)
	case r.Signal != "":
		fmt.Fprintf(&head, "killed by signal %s after %s\n", r.Signal, r.Duration.Round(time.Millisecond))
	default:
		fmt.Fprintf(&head, "exit status %d after %s\n", r.ExitCode, r.Duration.Round(time.Millisecond))
	}
	if r.Incomplete {
		head.WriteString("note: a process outside the command's group kept the output open, so its end may be missing\n")
	}
	var foot strings.Builder
	if change != nil {
		foot.WriteString(formatChange(*change))
	} else {
		foot.WriteString("\nfiles changed by this command: could not be determined\n")
	}
	redacted := s.provider.cfg.Redact(head.String() + "\x00" + r.Output + "\x00" + foot.String())
	parts := strings.Split(redacted, "\x00")
	if len(parts) != 3 { // a redactor must not change the structure; fall back to keeping it all
		return fitHeadTail(redacted, maxBytes)
	}
	room := maxBytes - len(parts[0]) - len(parts[2]) - 1
	if room < 128 {
		return fitHeadTail(parts[0]+parts[2], maxBytes)
	}
	return parts[0] + fitHeadTail(parts[1], room) + "\n" + parts[2]
}

func formatChange(c harnessexec.Change) string {
	if c.Empty() {
		if c.Incomplete {
			return "\nfiles changed by this command: none seen (the project is large, so this may be incomplete)\n"
		}
		return "\nfiles changed by this command: none\n"
	}
	var b strings.Builder
	b.WriteString("\nfiles changed by this command:\n")
	for _, group := range []struct {
		label string
		paths []string
	}{{"created", c.Created}, {"modified", c.Modified}, {"removed", c.Removed}} {
		if len(group.paths) > 0 {
			fmt.Fprintf(&b, "  %s: %s\n", group.label, strings.Join(group.paths, ", "))
		}
	}
	if len(c.Protected) > 0 {
		fmt.Fprintf(&b, "  PROTECTED LOCATIONS CHANGED (hooks, repository settings, automation, environment files): %s\n", strings.Join(c.Protected, ", "))
	}
	if c.Omitted > 0 {
		fmt.Fprintf(&b, "  …and %d more\n", c.Omitted)
	}
	if c.Incomplete {
		b.WriteString("  (the project is large, so this list may be incomplete)\n")
	}
	return b.String()
}
