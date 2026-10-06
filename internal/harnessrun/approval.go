package harnessrun

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessapproval"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessproof"
)

// There is deliberately no method on Manager that approves anything, and none can be
// reached from a route, a tool or a grant. A person records a decision in the approval
// store at a terminal; the manager only reads that store. A test asserts the manager has
// no method that could take an approval from a caller.

func ownerRecord(o Owner) harnessapproval.Owner {
	return harnessapproval.Owner{ClientID: o.ClientID, Workspace: o.Workspace, GrantID: o.GrantID, GrantRevision: o.GrantRevision}
}

// reasonsFor lists the fixed codes that explain why an action asks with extra friction.
func (m *Manager) reasonsFor(action harness.PreparedAction) []string {
	var reasons []string
	if r, ok := m.cfg.Policy.(StrictReasoner); ok {
		reasons = r.Reasons(action)
	} else {
		reasons = append(reasons, action.Escalate...)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason != "" && !seen[reason] && len(out) < 8 {
			seen[reason] = true
			out = append(out, reason)
		}
	}
	return out
}

// awaitApproval records the action for a person to decide and parks the run on it. It
// returns true when the loop should take another turn, which is only when the approval
// could not be recorded: the action does not run and the model is told so.
func (m *Manager) awaitApproval(ctx context.Context, run Run, answer harness.ModelAnswer, prepared harness.PreparedAction, strict bool, tool *harness.Session) bool {
	id := run.ID
	tool.Discard(prepared)
	if m.cfg.Approvals == nil {
		m.park(ctx, id, Attention{Kind: AttentionApproval, ActionDigest: prepared.Digest, Turn: run.Usage.Turns}, "approval_required")
		return false
	}
	friction := harnessapproval.FrictionStandard
	if strict {
		friction = harnessapproval.FrictionStrict
	}
	// The run moves to needs_attention at the next generation, and nothing else can change
	// its state while this worker owns it, so the approval is bound to that generation.
	record, err := m.cfg.Approvals.Request(ctx, harnessapproval.Request{RunID: id, RunGeneration: run.Generation + 1, Owner: ownerRecord(run.Owner),
		Tool: answer.ToolID, Digest: prepared.Digest, Summary: prepared.Summary, Paths: prepared.Paths, Friction: friction, Reasons: m.reasonsFor(prepared),
		Preview: prepared.Preview, Arguments: answer.ToolArguments})
	if err != nil {
		if ctx.Err() != nil {
			m.interrupted(ctx, id)
			return false
		}
		m.recordTool(id, "tool_denied", "tool_denied: approval_unavailable", len("tool_denied: approval_unavailable"), run.Usage.Turns)
		return true
	}
	m.teardown(id)
	parked, err := m.mutate(id, func(r *Run) (bool, error) {
		if r.State != StateRunning || r.Generation != run.Generation {
			return false, errAbort
		}
		if err := r.move(StateNeedsAttention, "approval_required", m.now().UTC()); err != nil {
			return false, err
		}
		r.Attention = &Attention{Kind: AttentionApproval, ActionDigest: prepared.Digest, ApprovalID: record.ID, Turn: run.Usage.Turns}
		return true, nil
	})
	if err != nil {
		// The run changed while the approval was being recorded, for instance a cancellation.
		_, _ = m.cfg.Approvals.Resolve(context.WithoutCancel(ctx), record.ID, harnessapproval.StateSuperseded, "run_changed")
		m.interrupted(ctx, id)
		return false
	}
	m.step(ctx, parked, "decision", "completed", "run needs attention")
	return false
}

func (m *Manager) authorized(owner Owner) bool {
	return m.cfg.StillAuthorized == nil || m.cfg.StillAuthorized(owner)
}

func (m *Manager) watchApprovals() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.cfg.ApprovalPoll)
	defer ticker.Stop()
	for {
		select {
		case <-m.root.Done():
			return
		case <-ticker.C:
			_, _ = m.ReconcileApprovals(m.root)
		}
	}
}

// approvalGrace is how long a run that is still running or queued may take to park on an
// approval that was just recorded. The record exists a moment before the run parks, and a
// reconcile in that window must not mistake the run for one that moved on.
const approvalGrace = 2 * time.Minute

// orphaned reports whether a run can no longer lead to this approval: it ended, it is
// parked on something else, or it has been running past the grace period without parking.
func (m *Manager) orphaned(run Run, record harnessapproval.Record) bool {
	switch {
	case run.State.Terminal():
		return true
	case run.State == StateNeedsAttention:
		return !waiting(run, record.ID)
	}
	return m.now().Sub(record.CreatedAt) > approvalGrace
}

// waiting reports whether the run is parked on exactly this approval.
func waiting(run Run, approvalID string) bool {
	return run.State == StateNeedsAttention && run.Attention != nil && run.Attention.Kind == AttentionApproval && run.Attention.ApprovalID == approvalID
}

// ReconcileApprovals applies the decisions a person recorded through the trusted local
// channel to the runs waiting on them, and returns how many runs it moved. It reads the
// approval store and never takes a decision from a caller. An approval whose run is gone,
// has moved on, or whose grant was revoked is ended so it cannot be used later.
func (m *Manager) ReconcileApprovals(ctx context.Context) (int, error) {
	if m.cfg.Approvals == nil || m.isClosed() {
		return 0, nil
	}
	records, err := m.cfg.Approvals.List(ctx, harnessapproval.ListOptions{})
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, record := range records {
		if ctx.Err() != nil {
			break
		}
		if record.State == harnessapproval.StateConsumed {
			continue
		}
		run, err := m.store.Load(record.RunID)
		if err != nil {
			if record.State.Live() {
				_, _ = m.cfg.Approvals.Resolve(ctx, record.ID, harnessapproval.StateSuperseded, "run_missing")
			}
			continue
		}
		here := waiting(run, record.ID)
		queued := run.Pending != nil && run.Pending.ApprovalID == record.ID && (run.State == StateQueued || run.State == StateRunning)
		switch record.State {
		case harnessapproval.StatePending:
			switch {
			case !here:
				if m.orphaned(run, record) {
					_, _ = m.cfg.Approvals.Resolve(ctx, record.ID, harnessapproval.StateSuperseded, "run_changed")
				}
			case !m.authorized(run.Owner):
				_, _ = m.cfg.Approvals.Resolve(ctx, record.ID, harnessapproval.StateRevoked, "grant_revoked")
				if m.cancelWaiting(ctx, run.ID, record.ID, "authorization_revoked") {
					moved++
				}
			}
		case harnessapproval.StateApproved:
			switch {
			case queued:
			case !here:
				if m.orphaned(run, record) {
					_, _ = m.cfg.Approvals.Resolve(ctx, record.ID, harnessapproval.StateSuperseded, "run_changed")
				}
			case !m.authorized(run.Owner):
				_, _ = m.cfg.Approvals.Resolve(ctx, record.ID, harnessapproval.StateRevoked, "grant_revoked")
				if m.cancelWaiting(ctx, run.ID, record.ID, "authorization_revoked") {
					moved++
				}
			case record.RunGeneration != run.Generation || record.Owner != ownerRecord(run.Owner) || record.Digest != run.Attention.ActionDigest:
				_, _ = m.cfg.Approvals.Resolve(ctx, record.ID, harnessapproval.StateRevoked, "binding_mismatch")
				if m.requeueAfterApproval(ctx, run.ID, record.ID, "approval_invalid", "tool_stale: approval_invalid", nil) {
					moved++
				}
			default:
				if m.requeueAfterApproval(ctx, run.ID, record.ID, "approved", "", &PendingAction{ApprovalID: record.ID, Digest: record.Digest}) {
					moved++
				}
			}
		case harnessapproval.StateDenied:
			if !here {
				continue
			}
			if record.Stop {
				if m.cancelWaiting(ctx, run.ID, record.ID, "cancelled_by_approver") {
					moved++
				}
			} else if m.requeueAfterApproval(ctx, run.ID, record.ID, "approval_denied", "tool_denied: denied by the person", nil) {
				moved++
			}
		default: // expired, revoked, superseded
			if here && m.requeueAfterApproval(ctx, run.ID, record.ID, "approval_ended", "tool_stale: approval_"+string(record.State), nil) {
				moved++
			}
		}
	}
	return moved, nil
}

// requeueAfterApproval moves a run waiting on one approval back to the queue, recording a
// note for the model when the action will not run, and the approved action to replay when
// it will. It reports whether it moved the run.
func (m *Manager) requeueAfterApproval(ctx context.Context, runID, approvalID, code, note string, pending *PendingAction) bool {
	if !m.reserve() {
		return false // the queue is full; the next poll tries again
	}
	queued := false
	run, err := m.mutate(runID, func(r *Run) (bool, error) {
		if !waiting(*r, approvalID) {
			return false, errAbort
		}
		now := m.now().UTC()
		if note != "" {
			r.addChunk("tool_result", note, r.Checkpoint.Turn)
		}
		if err := r.move(StateQueued, code, now); err != nil {
			return false, err
		}
		r.Pending = pending
		queued = true
		return true, nil
	})
	if err != nil || !queued {
		m.release()
		return false
	}
	m.queue <- runID
	m.step(ctx, run, "decision", "completed", "approval answered")
	return true
}

// cancelWaiting ends a run that was waiting on one approval.
func (m *Manager) cancelWaiting(ctx context.Context, runID, approvalID, code string) bool {
	cancelled := false
	run, err := m.mutate(runID, func(r *Run) (bool, error) {
		if !waiting(*r, approvalID) {
			return false, errAbort
		}
		if err := r.move(StateCancelled, code, m.now().UTC()); err != nil {
			return false, err
		}
		cancelled = true
		return true, nil
	})
	if err != nil || !cancelled {
		return false
	}
	m.step(ctx, run, "result", "completed", "run cancelled")
	return true
}

// clearPending drops the replay marker once its approval has been consumed.
func (m *Manager) clearPending(id, approvalID string) {
	_, _ = m.mutate(id, func(r *Run) (bool, error) {
		if r.Pending == nil || r.Pending.ApprovalID != approvalID {
			return false, errAbort
		}
		r.Pending = nil
		return true, nil
	})
}

// runApproved replays exactly the call a person approved. It consumes the approval first, so
// no crash can make it run twice, prepares the stored arguments again and requires the same
// digest, asks policy once more, and only then invokes under the manager's proof. A file
// that changed since the review gives a different digest, so the model sees a stale result
// and nothing is written. It returns true when the loop should take another turn.
func (m *Manager) runApproved(ctx context.Context, current Run, tool *harness.Session) bool {
	id, pending := current.ID, *current.Pending
	note := func(code, text string) bool {
		m.recordTool(id, code, text, len(text), current.Usage.Turns)
		return true
	}
	finish := func(outcome string) {
		_ = m.cfg.Approvals.Finish(context.WithoutCancel(ctx), pending.ApprovalID, outcome)
	}
	if m.cfg.Approvals == nil {
		m.clearPending(id, pending.ApprovalID)
		return note("tool_stale", "tool_stale: approval_unavailable")
	}
	record, err := m.cfg.Approvals.Consume(ctx, pending.ApprovalID, id, pending.Digest)
	m.clearPending(id, pending.ApprovalID)
	if err != nil {
		if ctx.Err() != nil {
			m.interrupted(ctx, id)
			return false
		}
		reason := "approval_unavailable"
		switch {
		case errors.Is(err, harnessapproval.ErrExpired):
			reason = "approval_expired"
		case errors.Is(err, harnessapproval.ErrNotLive), errors.Is(err, harnessapproval.ErrNotUsable):
			reason = "approval_used"
		}
		return note("tool_stale", "tool_stale: "+reason)
	}
	if tool == nil {
		finish("unavailable")
		return note("tool_unavailable", "")
	}
	envelope, err := tool.Envelope(harness.CapabilityID(record.Tool), m.cfg.ReplyBytes)
	if err != nil {
		finish("unavailable")
		return note("tool_unavailable", "")
	}
	prepared, err := tool.Prepare(ctx, harness.ToolRequest{Envelope: envelope, ToolID: record.Tool, Arguments: record.Arguments})
	if err != nil || ctx.Err() != nil {
		finish("interrupted")
		if ctx.Err() != nil {
			m.interrupted(ctx, id)
			return false
		}
		return note("tool_failed", "")
	}
	if prepared.Outcome != harness.OutcomeOK || prepared.Digest != pending.Digest {
		if prepared.Outcome == harness.OutcomeOK {
			tool.Discard(prepared)
		}
		finish("stale")
		return note("tool_stale", "tool_stale: changed_since_review")
	}
	if m.cfg.Policy.Decide(ctx, current.Owner, prepared) == DecisionDeny {
		tool.Discard(prepared)
		finish("denied_by_policy")
		return note("tool_denied", "tool_denied: policy")
	}
	began := m.now()
	result, err := tool.Invoke(harnessproof.Mint(ctx, pending.Digest), prepared)
	m.account(id, m.now().Sub(began), 0, harness.Usage{}) // tool time counts against the run's time budget
	if ctx.Err() != nil {
		finish("interrupted")
		m.interrupted(ctx, id)
		return false
	}
	if err != nil {
		finish("failed")
		return note("tool_failed", "")
	}
	if result.Outcome == harness.OutcomeOK || result.Outcome == harness.OutcomePartial {
		finish("applied")
	} else {
		finish(string(result.Outcome))
	}
	for _, code := range result.Audit {
		_ = m.cfg.Approvals.Note(context.WithoutCancel(ctx), pending.ApprovalID, code, "")
	}
	return m.finishToolCall(ctx, id, current.Usage.Turns, result)
}

// finishToolCall records what a tool returned and reports whether the loop continues.
func (m *Manager) finishToolCall(ctx context.Context, id string, turn int, result harness.ToolAnswer) bool {
	if result.Outcome == harness.OutcomeOK || result.Outcome == harness.OutcomePartial {
		updated := m.recordTool(id, "tool_"+string(result.Outcome), string(result.Output), len(result.Output), turn)
		if updated != nil && budgetStop(*updated) == "budget_output" {
			m.finishBudget(ctx, id, "budget_output")
			return false
		}
		return true
	}
	m.recordTool(id, "tool_"+string(result.Outcome), "", 0, turn)
	return true
}
