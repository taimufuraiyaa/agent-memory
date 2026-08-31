package contracts

import (
	"context"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillJobFinalization struct {
	Scope                      core.SkillOrchestratorScope
	JobID                      string
	Owner                      string
	Fence                      int64
	ExpectedWorkflowGeneration int64
	ResultKind                 core.SkillJobResultKind
	ResultReferences           []core.SkillOrchestratorReference
	FailureClass               core.SkillJobFailureClass
	FailureCode                string
	DeadLetter                 bool
	Now                        time.Time
}

type SkillJobRetry struct {
	Scope                      core.SkillOrchestratorScope
	JobID                      string
	Owner                      string
	Fence                      int64
	ExpectedWorkflowGeneration int64
	FailureClass               core.SkillJobFailureClass
	FailureCode                string
	ReadyAt                    time.Time
	Now                        time.Time
}

type SkillJobBlock struct {
	Scope                      core.SkillOrchestratorScope
	JobID                      string
	Owner                      string
	Fence                      int64
	ExpectedWorkflowGeneration int64
	FailureClass               core.SkillJobFailureClass
	ReasonCode                 string
	RecheckAt                  time.Time
	Now                        time.Time
}

type SkillSignalRouteResult struct {
	Workflow     core.SkillWorkflow
	Job          core.SkillJob
	Dependencies []core.SkillJobDependency
	Created      bool
	Ignored      bool
}

type SkillOrchestratorRepository interface {
	RouteSkillSignal(context.Context, core.SkillWorkflow, core.SkillJob, []core.SkillJobDependency) (SkillSignalRouteResult, error)
	CreateSkillWorkflow(context.Context, core.SkillWorkflow) (core.SkillWorkflow, bool, error)
	EnqueueSkillJob(context.Context, core.SkillJob, []core.SkillJobDependency) (core.SkillJob, bool, error)
	ClaimSkillJobs(context.Context, core.SkillOrchestratorScope, string, int, time.Duration, time.Duration, time.Time) ([]core.SkillJob, error)
	RenewSkillJobLease(context.Context, core.SkillOrchestratorScope, string, string, int64, time.Time, time.Time) error
	FinalizeSkillJob(context.Context, SkillJobFinalization) error
	RetrySkillJob(context.Context, SkillJobRetry) error
	BlockSkillJob(context.Context, SkillJobBlock) error
	CancelSkillJob(context.Context, core.SkillOrchestratorScope, string, int64, string, time.Time) error
	GetSkillJob(context.Context, core.SkillOrchestratorScope, string) (core.SkillJob, error)
	ListSkillJobs(context.Context, core.SkillOrchestratorScope, string, string, int) ([]core.SkillJob, string, error)
}
