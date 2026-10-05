package harnesscontext

import (
	"context"
	"errors"

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
}

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
		chunks = append(chunks, extra...)
	}
	var advisor Advisor
	if s.Advisor != nil {
		advisor = s.Advisor()
	}
	assembled, err := s.Assembler.Assemble(ctx, Request{Workspace: snapshot.Owner.Workspace, Eligibility: s.Eligibility, Budget: s.Budget, BoundarySeed: snapshot.RunID}, chunks, advisor)
	if err != nil {
		return harnessrun.PromptContext{}, err
	}
	return harnessrun.PromptContext{Refs: assembled.EvidenceRefs(), Prompt: Render(assembled), PrefixID: assembled.PrefixID,
		Class: int(assembled.MaxClass()), InputTokens: assembled.PolicyTok + assembled.Report.TokensUsed}, nil
}
