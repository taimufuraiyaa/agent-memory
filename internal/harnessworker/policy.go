package harnessworker

import (
	"context"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
	"github.com/taimufuraiyaa/agent-memory/internal/harnesstools"
)

// OwnershipPolicy adds file ownership to any policy. It runs after the base policy and can only
// turn an allowance or a question into a denial: an action that changes files another run owns
// is denied, and one the base policy already denied stays denied. A command's directory is not
// a file it changes, so commands are not claimed.
type OwnershipPolicy struct {
	Base   harnessrun.ToolPolicy
	Claims *Claims
}

var _ harnessrun.ToolPolicy = OwnershipPolicy{}

func (p OwnershipPolicy) Decide(ctx context.Context, owner harnessrun.Owner, action harness.PreparedAction) harnessrun.Decision {
	decision := p.Base.Decide(ctx, owner, action)
	if decision == harnessrun.DecisionDeny || p.Claims == nil || len(action.Paths) == 0 ||
		!harnesstools.Mutating(action.Capability) || action.Capability == harnesstools.ToolRunCommand {
		return decision
	}
	if p.Claims.Claim(action.Envelope.Scope.Run, action.Paths) != nil {
		return harnessrun.DecisionDeny
	}
	return decision
}

// Reasons forwards the base policy's reasons, so approvals still explain themselves.
func (p OwnershipPolicy) Reasons(action harness.PreparedAction) []string {
	if r, ok := p.Base.(harnessrun.StrictReasoner); ok {
		return r.Reasons(action)
	}
	return nil
}

var _ harnessrun.StrictReasoner = OwnershipPolicy{}
