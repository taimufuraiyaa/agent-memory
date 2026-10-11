package harnesscontext

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

// RunSource adapts the assembler to the run loop. Each turn it gathers the run's own
// chunks plus whatever the extra gatherers add, assembles them under the run's
// eligibility and shared budget, and returns the included items as references.
type RunSource struct {
	Assembler   *Assembler
	Eligibility Eligibility
	Budget      Budget
	// Gather adds repository, instruction, memory or solution chunks for this run. It
	// must return only chunks the run's owner is authorized to see.
	Gather func(ctx context.Context, owner harnessrun.Owner) ([]Chunk, error)
	// Advisor, when set, may nudge ordering; it is consulted per assembly.
	Advisor func() Advisor
	// AdvisorFor is Advisor for a composition that keeps one decision budget per run. When
	// set it is used instead of Advisor.
	AdvisorFor func(runID string) Advisor
	// SensitivityFor, when set, returns the advisor that may raise the sensitivity of the
	// repository chunks Gather returned. It can only raise, and only for chunks that could
	// still be shared.
	SensitivityFor func(runID string) SensitivityAdvisor
	// CacheFor, when set, returns the advisor for how to order a run's evidence. Advice is
	// used only when it is valid and in time; otherwise the relevance order applies.
	CacheFor func(runID string) CacheAdvisor
}

// cacheAdviceTimeout bounds how long a turn waits for ordering advice.
const cacheAdviceTimeout = 2 * time.Second

var _ harnessrun.ContextSource = (*RunSource)(nil)

func (s *RunSource) Context(ctx context.Context, snapshot harnessrun.Snapshot) (harnessrun.PromptContext, error) {
	if s == nil || s.Assembler == nil {
		return harnessrun.PromptContext{}, errors.New("no context assembler")
	}
	chunks := FromRun(snapshot.Owner.Workspace, snapshot.Chunks)
	if s.Gather != nil {
		extra, err := s.Gather(ctx, snapshot.Owner)
		if err != nil {
			return harnessrun.PromptContext{}, err
		}
		if s.SensitivityFor != nil {
			if advisor := s.SensitivityFor(snapshot.RunID); advisor != nil {
				extra = raiseSensitivity(ctx, advisor, s.Eligibility.MaxSensitivity, extra)
			}
		}
		chunks = append(chunks, extra...)
	}
	var advisor Advisor
	switch {
	case s.AdvisorFor != nil:
		advisor = s.AdvisorFor(snapshot.RunID)
	case s.Advisor != nil:
		advisor = s.Advisor()
	}
	assembled, err := s.Assembler.Assemble(ctx, Request{Workspace: snapshot.Owner.Workspace, Eligibility: s.Eligibility, Budget: s.Budget, BoundarySeed: snapshot.RunID,
		Order: s.order(ctx, snapshot.RunID)}, chunks, advisor)
	if err != nil {
		return harnessrun.PromptContext{}, err
	}
	return harnessrun.PromptContext{Refs: assembled.EvidenceRefs(), Prompt: Render(assembled), PrefixID: assembled.PrefixID,
		Class: int(assembled.MaxClass()), InputTokens: assembled.PolicyTok + assembled.Report.TokensUsed}, nil
}

// order asks the run's cache advisor, contained against a panic and bounded in time, and
// returns the relevance order unless it gets a valid answer.
func (s *RunSource) order(ctx context.Context, runID string) Order {
	if s.CacheFor == nil {
		return OrderRelevance
	}
	advisor := s.CacheFor(runID)
	if advisor == nil {
		return OrderRelevance
	}
	adviceCtx, cancel := context.WithTimeout(ctx, cacheAdviceTimeout)
	defer cancel()
	type result struct {
		order Order
		err   error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- result{err: errors.New("cache advisor panicked")}
			}
		}()
		order, err := advisor.Order(adviceCtx, runID)
		done <- result{order, err}
	}()
	select {
	case got := <-done:
		if got.err == nil && got.order.valid() {
			return got.order
		}
	case <-adviceCtx.Done():
	}
	return OrderRelevance
}
