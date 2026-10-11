package harnesstools

import (
	"context"
	"sync"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

// RiskAdvisor judges how careful to be about one prepared action. Its answer can only add
// caution to what the deterministic policy decided; it is consulted only after that decision
// exists, and only for an action the policy already asks about.
type RiskAdvisor interface {
	Risk(ctx context.Context, action harness.PreparedAction) (harnessdecide.Risk, harnessdecide.Outcome)
}

const (
	// ReasonJevCareful and ReasonJevHazardous are the extra-friction reasons shown when advice
	// raised an ordinary ask; ReasonJevCautious when a required advisor could not answer.
	ReasonJevCareful   = "jev_careful"
	ReasonJevHazardous = "jev_hazardous"
	ReasonJevCautious  = "jev_cautious"

	adviceMemory = 64
)

// AdvisedPolicy is a Policy with a risk advisor. Anything the base policy denies stays denied,
// anything it allows stays allowed and anything it asks about with extra friction stays so;
// the only change advice can make is to turn an ordinary ask into an extra-friction ask, and
// it can make no other. The preview a person reviews, the digest it is bound to and the proof
// that must accompany the run are not touched.
type AdvisedPolicy struct {
	base    Policy
	advisor RiskAdvisor

	mu     sync.Mutex
	advice map[string]string // digest -> reason code
	order  []string
}

var (
	_ harnessrun.ToolPolicy     = (*AdvisedPolicy)(nil)
	_ harnessrun.StrictReasoner = (*AdvisedPolicy)(nil)
)

// NewAdvisedPolicy wraps a base policy. With a nil advisor it behaves exactly as the base.
func NewAdvisedPolicy(base Policy, advisor RiskAdvisor) *AdvisedPolicy {
	return &AdvisedPolicy{base: base, advisor: advisor, advice: map[string]string{}}
}

func (p *AdvisedPolicy) Decide(ctx context.Context, owner harnessrun.Owner, action harness.PreparedAction) harnessrun.Decision {
	decision := p.base.Decide(ctx, owner, action)
	if decision != harnessrun.DecisionAsk || p.advisor == nil {
		return decision
	}
	reason, remembered := p.recall(action.Digest)
	if !remembered {
		risk, out := p.advisor.Risk(ctx, action)
		reason = reasonFor(risk, out)
		p.remember(action.Digest, reason)
	}
	if reason != "" {
		return harnessrun.DecisionAskStrict
	}
	return decision
}

func (p *AdvisedPolicy) Reasons(action harness.PreparedAction) []string {
	reasons := p.base.Reasons(action)
	if reason, ok := p.recall(action.Digest); ok && reason != "" {
		reasons = append(reasons, reason)
	}
	return reasons
}

// reasonFor turns an advisor's answer into the reason code it justifies. A cautious outcome
// (a required advisor that could not answer) and any risk above routine ask with extra
// friction; a routine, empty or unrecognized risk adds nothing.
func reasonFor(risk harnessdecide.Risk, out harnessdecide.Outcome) string {
	switch {
	case out.Cautious:
		return ReasonJevCautious
	case risk == harnessdecide.RiskHazardous:
		return ReasonJevHazardous
	case risk == harnessdecide.RiskCareful:
		return ReasonJevCareful
	}
	return ""
}

func (p *AdvisedPolicy) recall(digest string) (string, bool) {
	if digest == "" {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	reason, ok := p.advice[digest]
	return reason, ok
}

// remember keeps advice for a bounded number of recent actions, so approving the action
// asks the advisor once, not again at replay, and its reasons can be shown.
func (p *AdvisedPolicy) remember(digest, reason string) {
	if digest == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.advice[digest]; !ok {
		p.order = append(p.order, digest)
	}
	p.advice[digest] = reason
	for len(p.order) > adviceMemory {
		delete(p.advice, p.order[0])
		p.order = p.order[1:]
	}
}

// ServiceRiskAdvisor routes the judgment through the decision service of the action's run. It
// describes an action only by its capability, its fixed escalation codes and counts, and
// never by its arguments, paths or preview.
type ServiceRiskAdvisor struct {
	For func(runID string) *harnessdecide.Service
}

var _ RiskAdvisor = ServiceRiskAdvisor{}

func (s ServiceRiskAdvisor) Risk(ctx context.Context, action harness.PreparedAction) (harnessdecide.Risk, harnessdecide.Outcome) {
	var service *harnessdecide.Service
	if s.For != nil {
		service = s.For(action.Envelope.Scope.Run)
	}
	if service == nil {
		return "", harnessdecide.Outcome{Kind: harnessdecide.KindCommandRisk, Status: harnessdecide.StatusDisabled}
	}
	return service.CommandRisk(ctx, string(action.Capability), riskFacts(action))
}

// riskFacts reads the numbers and flags an action can be described by.
func riskFacts(action harness.PreparedAction) harnessdecide.Facts {
	facts := harnessdecide.Facts{"n": len(action.Paths)}
	flags := map[string]string{"not_cataloged": "np", "project_program": "pp", "runs_recipes": "rr", "inline_code": "ic", "large_change": "lg"}
	for _, code := range action.Escalate {
		if key, ok := flags[code]; ok {
			facts[key] = 1
		}
	}
	if action.Capability == ToolDeleteFile {
		facts["de"] = 1
	}
	if action.Capability != ToolRunCommand {
		for _, path := range action.Paths {
			if ControlPlane(path) != "" {
				facts["cp"] = 1
			}
		}
	}
	return facts
}
