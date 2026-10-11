package harnesstools

import "github.com/taimufuraiyaa/agent-memory/internal/harnessrun"

const (
	decisionAllow     = harnessrun.DecisionAllow
	decisionAsk       = harnessrun.DecisionAsk
	decisionAskStrict = harnessrun.DecisionAskStrict
	decisionDeny      = harnessrun.DecisionDeny
)

func harnessrunOwner() harnessrun.Owner { return harnessrun.Owner{} }
