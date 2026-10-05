package harnesstools

import "github.com/taimufuraiyaa/agent-memory/internal/harnessrun"

const (
	decisionAllow = harnessrun.DecisionAllow
	decisionAsk   = harnessrun.DecisionAsk
	decisionDeny  = harnessrun.DecisionDeny
)

func harnessrunOwner() harnessrun.Owner { return harnessrun.Owner{} }
