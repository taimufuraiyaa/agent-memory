package harnessrun

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// runOne claims one queued run and drives it until it finishes, parks for a human,
// or is interrupted. The claim is the only place a worker epoch begins.
func (m *Manager) runOne(id string) {
	ctx, cancel := context.WithCancel(m.root)
	defer cancel()
	m.setActive(id, cancel)
	defer m.clearActive(id)

	var claimed Run
	won := false
	_, err := m.mutate(id, func(r *Run) (bool, error) {
		now := m.now().UTC()
		switch r.State {
		case StateQueued:
			r.Epoch++
			if r.StartedAt.IsZero() {
				r.StartedAt = now
			}
			if err := r.move(StateRunning, "started", now); err != nil {
				return false, err
			}
			claimed, won = *r, true
			return true, nil
		case StateCancelling:
			if err := r.move(StateCancelled, "cancelled", now); err != nil {
				return false, err
			}
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		if errors.Is(err, ErrStorage) {
			_ = m.store.Quarantine(id)
		}
		return
	}
	if won {
		m.drive(ctx, claimed)
	}
}

// drive is the bounded loop: one model call, then optionally one tool action, per
// turn. Every provider call goes through a harness session, so cancellation,
// deadlines, stale replies and oversized output are handled by the shared contract.
func (m *Manager) drive(ctx context.Context, run Run) {
	id := run.ID
	scope := harness.Scope{Workspace: run.Owner.Workspace, Run: id, Generation: run.Epoch}
	models := &modelSet{manager: m, ctx: ctx, scope: scope}
	if m.cfg.Router == nil {
		// One configured provider: open it up front so an unusable one fails the run at once.
		if _, err := models.get(m.cfg.Model); err != nil {
			if ctx.Err() == nil {
				m.finish(ctx, id, StateFailed, "model_unavailable", "", "")
			}
			return
		}
	}
	var tool *harness.Session
	if m.cfg.Tool != nil {
		opened, err := m.cfg.Registry.OpenSession(ctx, m.cfg.Tool.Provider, scope, harness.WithCallTimeout(m.cfg.CallTimeout))
		if err == nil {
			tool = opened
		} else {
			m.record(id, "tool", "tool_unavailable")
		}
	}
	release := sync.OnceFunc(func() {
		closeBounded(tool, m.cfg.CloseTimeout)
		models.closeAll(m.cfg.CloseTimeout)
	})
	m.setRelease(id, release)
	defer release()
	for {
		if ctx.Err() != nil {
			m.interrupted(ctx, id)
			return
		}
		current, err := m.store.Load(id)
		if err != nil {
			return
		}
		switch current.State {
		case StateCancelling:
			m.finishCancelled(ctx, id)
			return
		case StateRunning:
		default:
			return
		}
		if m.cfg.StillAuthorized != nil && !m.cfg.StillAuthorized(current.Owner) {
			m.requestCancel(id, "authorization_revoked")
			continue
		}
		if code := budgetStop(current); code != "" {
			m.finishBudget(ctx, id, code)
			return
		}
		if !m.turn(ctx, current, models, tool) {
			return
		}
	}
}

// budgetStop reports which consumed budget, if any, ends the run now.
func budgetStop(r Run) string {
	switch {
	case r.Usage.Turns >= r.Budget.MaxTurns:
		return "budget_turns"
	case r.Usage.ActiveMillis >= r.Budget.MaxActive.Milliseconds():
		return "budget_time"
	case r.Usage.SpendMicros > r.Budget.MaxSpendMicros:
		return "budget_spend"
	case r.Usage.OutputBytes > r.Budget.MaxOutputBytes:
		return "budget_output"
	}
	return ""
}

// modelResult is what the attempt chain produced for one turn.
type modelResult struct {
	answer      harness.ModelAnswer
	remaining   time.Duration // active budget left when the final attempt started
	elapsed     time.Duration // duration of the final attempt
	contract    bool          // the final failure was a contract violation, not a typed outcome
	interrupted bool
	fatal       string // routing failed before any call: a fixed reason code
}

// turn performs one model call and its optional tool action. It returns false when
// the worker should stop: the run finished, parked, was cancelled or was interrupted.
func (m *Manager) turn(ctx context.Context, current Run, models *modelSet, tool *harness.Session) bool {
	id := current.ID
	pc := m.promptContext(ctx, current)
	res := m.callModel(ctx, current, models, tool, pc)
	switch {
	case res.interrupted:
		m.interrupted(ctx, id)
		return false
	case res.fatal != "":
		m.finish(ctx, id, StateFailed, res.fatal, "", "")
		return false
	case res.contract:
		m.finish(ctx, id, StateFailed, "model_contract", "", "")
		return false
	}
	answer, elapsed, remaining := res.answer, res.elapsed, res.remaining

	committed, err := m.mutate(id, func(r *Run) (bool, error) {
		if r.State != StateRunning {
			return false, errAbort
		}
		now := m.now().UTC()
		r.Usage.ActiveMillis += elapsed.Milliseconds()
		r.Usage.SpendMicros += answer.CostMicros
		r.Usage.InputTokens += answer.Usage.InputTokens
		r.Usage.OutputTokens += answer.Usage.OutputTokens
		r.Usage.CachedInputTokens += answer.Usage.CachedInputTokens
		r.UpdatedAt = now
		if answer.Outcome == harness.OutcomeOK || answer.Outcome == harness.OutcomePartial {
			r.Usage.Turns++
			r.Usage.OutputBytes += len(answer.Text) + len(answer.ToolArguments)
			r.Checkpoint = checkpoint{Turn: r.Usage.Turns, Epoch: r.Epoch, At: now}
			if answer.Text != "" {
				r.addChunk("model_text", m.clean(answer.Text, MaxChunkBytes), r.Usage.Turns)
			}
			r.addEvent("turn", "model_reply", now)
		} else {
			r.addEvent("turn", "model_"+string(answer.Outcome), now)
		}
		return true, nil
	})
	if err != nil {
		if errors.Is(err, errAbort) {
			m.interrupted(ctx, id)
		}
		return false
	}

	switch answer.Outcome {
	case harness.OutcomeOK, harness.OutcomePartial:
	case harness.OutcomeCancelled:
		m.interrupted(ctx, id)
		return false
	case harness.OutcomeTimeout:
		// The deadline was either the call timeout or the run's remaining active budget.
		if remaining <= m.cfg.CallTimeout {
			m.finishBudget(ctx, id, "budget_time")
		} else {
			m.finish(ctx, id, StateFailed, "model_timeout", "", "")
		}
		return false
	default:
		m.finish(ctx, id, StateFailed, "model_"+string(answer.Outcome), "", "")
		return false
	}
	if code := budgetStop(committed); code == "budget_spend" || code == "budget_output" {
		m.finishBudget(ctx, id, code)
		return false
	}
	switch {
	case answer.ToolID == "":
		if answer.Outcome == harness.OutcomePartial {
			m.finish(ctx, id, StatePartial, "model_partial", "partial", answer.Text)
		} else {
			m.finish(ctx, id, StateCompleted, "completed", "result", answer.Text)
		}
		return false
	case answer.ToolID == ToolClarify:
		m.park(ctx, id, Attention{Kind: AttentionClarification, Prompt: m.clean(answer.Text, MaxPromptBytes), Turn: committed.Usage.Turns}, "clarification_needed")
		return false
	default:
		return m.runTool(ctx, committed, answer, tool)
	}
}

// callModel plans the eligible chain of providers for this call and tries them in order.
// A failed attempt is accounted for at once, because it may still have cost money or time,
// and the next attempt goes only to the next provider in the chain: a provider the router
// judged ineligible for this data is never reached by falling back.
func (m *Manager) callModel(ctx context.Context, current Run, models *modelSet, tool *harness.Session, pc PromptContext) modelResult {
	id := current.ID
	chain, fatal := m.modelChain(ctx, current, pc)
	if fatal != "" {
		return modelResult{fatal: fatal}
	}
	toolIDs := offeredTools(tool)
	var spent time.Duration
	var last modelResult
	for i, binding := range chain {
		if i > 0 {
			m.record(id, "turn", "model_fallback")
		}
		remaining := current.Budget.MaxActive - time.Duration(current.Usage.ActiveMillis)*time.Millisecond - spent
		if remaining <= 0 {
			return modelResult{answer: harness.ModelAnswer{Outcome: harness.OutcomeTimeout}}
		}
		session, err := models.get(binding)
		if err != nil {
			if ctx.Err() != nil {
				return modelResult{interrupted: true}
			}
			m.observe(binding, harness.OutcomeUnavailable, 0, harness.ModelAnswer{}, pc)
			last = modelResult{answer: harness.ModelAnswer{Outcome: harness.OutcomeUnavailable}, remaining: remaining}
			continue
		}
		envelope, err := session.Envelope(binding.Capability, m.cfg.ReplyBytes)
		if err != nil {
			last = modelResult{contract: true}
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, remaining)
		started := m.now()
		answer, err := session.Generate(callCtx, harness.ModelRequest{Envelope: envelope, InputRefs: pc.Refs, MaxOutputTokens: m.cfg.MaxOutputTokens,
			ToolSchemaIDs: toolIDs, Prompt: pc.Prompt, PrefixID: pc.PrefixID})
		cancel()
		elapsed := m.now().Sub(started)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, harness.ErrClosed) {
				return modelResult{interrupted: true}
			}
			m.account(id, elapsed, 0, harness.Usage{})
			spent += elapsed
			m.observe(binding, harness.OutcomeFailed, elapsed, harness.ModelAnswer{}, pc)
			last = modelResult{contract: true}
			continue
		}
		m.observe(binding, answer.Outcome, elapsed, answer, pc)
		last = modelResult{answer: answer, elapsed: elapsed, remaining: remaining}
		success := answer.Outcome == harness.OutcomeOK || answer.Outcome == harness.OutcomePartial
		budgetTimeout := answer.Outcome == harness.OutcomeTimeout && remaining <= m.cfg.CallTimeout
		if success || !fallbackable(answer.Outcome) || budgetTimeout || i == len(chain)-1 {
			return last
		}
		m.account(id, elapsed, answer.CostMicros, answer.Usage)
		spent += elapsed
	}
	return last
}

// modelChain returns the providers to try, in order. With no router it is the one
// configured provider; with one it is the router's eligible chain for this data class,
// size and remaining spend, and an empty chain is an explicit, content-free failure.
func (m *Manager) modelChain(ctx context.Context, current Run, pc PromptContext) ([]Binding, string) {
	if m.cfg.Router == nil {
		return []Binding{m.cfg.Model}, ""
	}
	remaining := current.Budget.MaxSpendMicros - current.Usage.SpendMicros
	if remaining < 0 {
		remaining = 0
	}
	chain, err := m.cfg.Router.Plan(ctx, ModelNeed{Class: pc.Class, InputTokens: pc.InputTokens, OutputTokens: m.cfg.MaxOutputTokens,
		PrefixID: pc.PrefixID, HasSpendCap: true, SpendCapMicros: remaining})
	switch {
	case err == nil && len(chain) > 0:
		if len(chain) > 4 {
			chain = chain[:4]
		}
		return chain, ""
	case errors.Is(err, ErrNoEligibleModel):
		return nil, "model_ineligible"
	default:
		return nil, "model_unavailable"
	}
}

func (m *Manager) observe(binding Binding, outcome harness.Outcome, elapsed time.Duration, answer harness.ModelAnswer, pc PromptContext) {
	if m.cfg.Router == nil {
		return
	}
	m.cfg.Router.Observe(binding.Provider, ModelObservation{Outcome: outcome, Latency: elapsed, Usage: answer.Usage, CostMicros: answer.CostMicros, PrefixID: pc.PrefixID})
}

// promptContext returns the prompt and references for this call: the configured source's,
// or the loop's own references with no prompt when there is none or it fails.
func (m *Manager) promptContext(ctx context.Context, current Run) PromptContext {
	fallback := PromptContext{Refs: evidenceRefs(current), Class: 1}
	if m.cfg.Context == nil {
		return fallback
	}
	pc, err := m.cfg.Context.Context(ctx, Snapshot{RunID: current.ID, Owner: current.Owner, Turn: current.Usage.Turns, Chunks: append([]Chunk(nil), current.Chunks...)})
	if err != nil || len(pc.Refs) == 0 || len(pc.Refs) > harness.MaxEvidenceRefs || len(pc.Prompt) > harness.MaxPromptBytes ||
		pc.Class < 0 || pc.Class > 3 || pc.InputTokens < 0 {
		m.record(current.ID, "turn", "context_fallback")
		return fallback
	}
	return pc
}

func offeredTools(tool *harness.Session) []string {
	ids := []string{ToolClarify}
	if tool != nil {
		for _, capability := range tool.Manifest().Capabilities {
			if tool.State(capability) == harness.AccessAvailable && string(capability) != ToolClarify {
				ids = append(ids, string(capability))
			}
		}
	}
	sort.Strings(ids)
	return ids
}

// evidenceRefs names the goal and the most recent run chunks. Content is resolved by
// the context assembler (task 5); the loop never forwards raw text on its own.
func evidenceRefs(r Run) []harness.EvidenceRef {
	refs := make([]harness.EvidenceRef, 0, 5)
	recent := 0
	for i := len(r.Chunks) - 1; i >= 0 && recent < 4; i-- {
		if r.Chunks[i].Kind == "goal" {
			continue
		}
		refs = append(refs, harness.EvidenceRef{ID: r.Chunks[i].ID, Revision: fmt.Sprint(r.Generation), Class: r.Chunks[i].Kind})
		recent++
	}
	return append([]harness.EvidenceRef{{ID: "goal", Revision: "1", Class: "goal"}}, refs...)
}

// runTool prepares, decides and (if allowed) invokes one tool action. It returns
// true when the loop should take another turn.
func (m *Manager) runTool(ctx context.Context, run Run, answer harness.ModelAnswer, tool *harness.Session) bool {
	id := run.ID
	if run.Usage.ToolCalls >= run.Budget.MaxToolCalls {
		m.finishBudget(ctx, id, "budget_tool_calls")
		return false
	}
	if tool == nil {
		m.recordTool(id, "tool_unavailable", "", 0, run.Usage.Turns)
		return true
	}
	envelope, err := tool.Envelope(harness.CapabilityID(answer.ToolID), m.cfg.ReplyBytes)
	if err != nil {
		m.recordTool(id, "tool_unavailable", "", 0, run.Usage.Turns)
		return true
	}
	prepared, err := tool.Prepare(ctx, harness.ToolRequest{Envelope: envelope, ToolID: answer.ToolID, Arguments: answer.ToolArguments})
	if err != nil || ctx.Err() != nil {
		if ctx.Err() != nil {
			m.interrupted(ctx, id)
			return false
		}
		m.recordTool(id, "tool_failed", "", 0, run.Usage.Turns)
		return true
	}
	if prepared.Outcome != harness.OutcomeOK {
		m.recordTool(id, "tool_"+string(prepared.Outcome), "", 0, run.Usage.Turns)
		return true
	}
	switch m.cfg.Policy.Decide(ctx, run.Owner, prepared) {
	case DecisionAllow:
	case DecisionAsk:
		tool.Discard(prepared)
		m.park(ctx, id, Attention{Kind: AttentionApproval, ActionDigest: prepared.Digest, Turn: run.Usage.Turns}, "approval_required")
		return false
	default:
		tool.Discard(prepared)
		m.recordTool(id, "tool_denied", "", 0, run.Usage.Turns)
		return true
	}
	result, err := tool.Invoke(ctx, prepared)
	if ctx.Err() != nil {
		m.interrupted(ctx, id)
		return false
	}
	if err != nil {
		m.recordTool(id, "tool_failed", "", 0, run.Usage.Turns)
		return true
	}
	if result.Outcome == harness.OutcomeOK || result.Outcome == harness.OutcomePartial {
		updated := m.recordTool(id, "tool_"+string(result.Outcome), string(result.Output), len(result.Output), run.Usage.Turns)
		if updated != nil && budgetStop(*updated) == "budget_output" {
			m.finishBudget(ctx, id, "budget_output")
			return false
		}
		return true
	}
	m.recordTool(id, "tool_"+string(result.Outcome), "", 0, run.Usage.Turns)
	return true
}

// recordTool counts one tool attempt and keeps a bounded, content-minimized result.
func (m *Manager) recordTool(id, code, output string, outputBytes, turn int) *Run {
	run, err := m.mutate(id, func(r *Run) (bool, error) {
		if r.State != StateRunning {
			return false, errAbort
		}
		now := m.now().UTC()
		r.Usage.ToolCalls++
		r.Usage.OutputBytes += outputBytes
		text := output
		if text == "" {
			text = code
		}
		r.addChunk("tool_result", m.clean(text, MaxChunkBytes), turn)
		r.UpdatedAt = now
		r.addEvent("tool", code, now)
		return true, nil
	})
	if err != nil {
		return nil
	}
	return &run
}

// record appends a content-free event without changing state.
func (m *Manager) record(id, kind, code string) {
	_, _ = m.mutate(id, func(r *Run) (bool, error) {
		r.UpdatedAt = m.now().UTC()
		r.addEvent(kind, code, r.UpdatedAt)
		return true, nil
	})
}

// account adds the time, spend and tokens of an attempt that did not become the turn's
// result, without counting a turn.
func (m *Manager) account(id string, elapsed time.Duration, cost int64, usage harness.Usage) {
	_, _ = m.mutate(id, func(r *Run) (bool, error) {
		if r.State != StateRunning {
			return false, errAbort
		}
		r.Usage.ActiveMillis += elapsed.Milliseconds()
		r.Usage.SpendMicros += cost
		r.Usage.InputTokens += usage.InputTokens
		r.Usage.OutputTokens += usage.OutputTokens
		r.Usage.CachedInputTokens += usage.CachedInputTokens
		r.UpdatedAt = m.now().UTC()
		return true, nil
	})
}

// finish ends a running run, optionally keeping a bounded result artifact.
func (m *Manager) finish(ctx context.Context, id string, to State, code, artifactKind, artifactText string) {
	m.teardown(id)
	run, err := m.mutate(id, func(r *Run) (bool, error) {
		if r.State != StateRunning {
			return false, errAbort
		}
		now := m.now().UTC()
		if artifactText != "" {
			r.addArtifact(artifactKind, m.clean(artifactText, MaxArtifactBytes), r.Usage.Turns, digestOf)
			r.addEvent("artifact", artifactKind, now)
		}
		if err := r.move(to, code, now); err != nil {
			return false, err
		}
		return true, nil
	})
	if err == nil {
		m.step(ctx, run, "result", stepStatus(to), "run "+string(to))
	}
}

// finishBudget stops a run that used up a budget. It is partial, never failed, and
// keeps the latest model text as an artifact so work is not lost.
func (m *Manager) finishBudget(ctx context.Context, id, code string) {
	m.teardown(id)
	run, err := m.mutate(id, func(r *Run) (bool, error) {
		if r.State != StateRunning {
			return false, errAbort
		}
		now := m.now().UTC()
		for i := len(r.Chunks) - 1; i >= 0; i-- {
			if r.Chunks[i].Kind == "model_text" && r.Chunks[i].Text != "" {
				r.addArtifact("partial", r.Chunks[i].Text, r.Usage.Turns, digestOf)
				r.addEvent("artifact", "partial", now)
				break
			}
		}
		r.addEvent("budget", code, now)
		if err := r.move(StatePartial, code, now); err != nil {
			return false, err
		}
		return true, nil
	})
	if err == nil {
		m.step(ctx, run, "result", "completed", "run stopped at a budget")
	}
}

// park stops the worker until a human responds. The run keeps its state and budget.
func (m *Manager) park(ctx context.Context, id string, attention Attention, code string) {
	m.teardown(id)
	run, err := m.mutate(id, func(r *Run) (bool, error) {
		if r.State != StateRunning {
			return false, errAbort
		}
		r.Attention = &attention
		if err := r.move(StateNeedsAttention, code, m.now().UTC()); err != nil {
			return false, err
		}
		return true, nil
	})
	if err == nil {
		m.step(ctx, run, "decision", "completed", "run needs attention")
	}
}

func (m *Manager) requestCancel(id, code string) {
	_, _ = m.mutate(id, func(r *Run) (bool, error) {
		if r.State != StateRunning {
			return false, errAbort
		}
		if err := r.move(StateCancelling, code, m.now().UTC()); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (m *Manager) finishCancelled(ctx context.Context, id string) {
	m.teardown(id)
	run, err := m.mutate(id, func(r *Run) (bool, error) {
		if r.State != StateCancelling {
			return false, errAbort
		}
		if err := r.move(StateCancelled, "cancelled", m.now().UTC()); err != nil {
			return false, err
		}
		return true, nil
	})
	if err == nil {
		m.step(ctx, run, "result", "completed", "run cancelled")
	}
}

// interrupted runs when the worker's context ends. If the client asked for
// cancellation the run finishes cancelling; if the manager is shutting down the run
// keeps its checkpoint, still marked running, for Recover to resume.
func (m *Manager) interrupted(ctx context.Context, id string) {
	current, err := m.store.Load(id)
	if err == nil && current.State == StateCancelling {
		m.finishCancelled(ctx, id)
	}
}

func stepStatus(to State) string {
	if to == StateFailed {
		return "failed"
	}
	return "completed"
}

// closeBounded closes a session but stops waiting after timeout; a provider that hangs
// in Close finishes on its own goroutine without blocking the run.
func closeBounded(session *harness.Session, timeout time.Duration) {
	if session == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_ = session.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}
