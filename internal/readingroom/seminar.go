package readingroom

import (
	"context"
	"errors"
	"sort"
)

type SeminarStatus string

const (
	SeminarCompleted SeminarStatus = "completed"
	SeminarPartial   SeminarStatus = "partial"
	SeminarCancelled SeminarStatus = "cancelled"
)

type SeminarResult struct {
	RunID         string            `json:"run_id"`
	Status        SeminarStatus     `json:"status"`
	Contributions []Contribution    `json:"contributions"`
	Synthesis     *SynthesisResult  `json:"synthesis,omitempty"`
	RoleErrors    map[string]string `json:"role_errors,omitempty"`
	Rejected      map[string]string `json:"rejected,omitempty"`
}
type Seminar struct {
	runner   RoleRunner
	gate     *VerifierGate
	profiles map[Role]AgentProfile
}

func NewSeminar(runner RoleRunner, gate *VerifierGate, profiles map[Role]AgentProfile) *Seminar {
	if profiles == nil {
		profiles = DefaultProfiles()
	}
	return &Seminar{runner: runner, gate: gate, profiles: profiles}
}
func (s *Seminar) Run(ctx context.Context, runID string, packet EvidencePacket, maxTokens int) (SeminarResult, error) {
	if s == nil || s.runner == nil || s.gate == nil {
		return SeminarResult{}, errors.New("seminar dependencies are required")
	}
	roles := []Role{RoleLibrarian, RoleSummarizer, RoleCritic, RoleConnector, RoleQuestioner}
	nodes := make([]WorkflowNode, 0, len(roles))
	perRole := maxTokens / (len(roles) + 1)
	if perRole <= 0 {
		return SeminarResult{}, errors.New("seminar token budget is too small")
	}
	for _, role := range roles {
		nodes = append(nodes, WorkflowNode{ID: string(role), Profile: s.profiles[role], MaxOutputTokens: perRole})
	}
	execution, err := NewWorkflowExecutor(s.runner).Execute(ctx, runID, Workflow{Nodes: nodes, MaxFanOut: len(nodes), MaxTotalTokens: maxTokens}, packet)
	result := SeminarResult{RunID: runID, Status: SeminarCompleted, RoleErrors: execution.Errors, Rejected: map[string]string{}}
	if ctx.Err() != nil {
		result.Status = SeminarCancelled
		return result, ctx.Err()
	}
	if err != nil {
		if ctx.Err() != nil {
			result.Status = SeminarCancelled
			return result, ctx.Err()
		}
		return result, err
	}
	ids := make([]string, 0, len(execution.Results))
	for id := range execution.Results {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	drafts := []Contribution{}
	for _, id := range ids {
		drafts = append(drafts, execution.Results[id].Contributions...)
	}
	gated, err := s.gate.Verify(ctx, drafts, packet.Evidence)
	if err != nil {
		return result, err
	}
	result.Contributions = gated.Verified
	result.Rejected = gated.Rejected
	if len(execution.Errors) > 0 || len(gated.Rejected) > 0 {
		result.Status = SeminarPartial
	}
	if len(gated.Verified) > 0 {
		synthesis, err := Synthesize("synthesis:"+runID, "Verified seminar synthesis; disagreements and open questions remain explicit.", gated.Verified, s.profiles[RoleSynthesizer])
		if err != nil {
			return result, err
		}
		result.Synthesis = &synthesis
	}
	return result, nil
}
