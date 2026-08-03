package readingroom

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

type WorkflowNode struct {
	ID              string       `json:"id"`
	Profile         AgentProfile `json:"profile"`
	DependsOn       []string     `json:"depends_on,omitempty"`
	Retries         int          `json:"retries"`
	MaxOutputTokens int          `json:"max_output_tokens"`
}
type Workflow struct {
	Nodes          []WorkflowNode `json:"nodes"`
	MaxFanOut      int            `json:"max_fan_out"`
	MaxTotalTokens int            `json:"max_total_tokens"`
}

func (w Workflow) Validate() error {
	if len(w.Nodes) == 0 || w.MaxFanOut <= 0 || w.MaxTotalTokens <= 0 {
		return errors.New("workflow requires nodes, fan-out, and token budget")
	}
	ids := map[string]bool{}
	total := 0
	for _, n := range w.Nodes {
		if n.ID == "" || ids[n.ID] || n.Retries < 0 || n.Retries > 3 || n.MaxOutputTokens <= 0 {
			return errors.New("invalid workflow node")
		}
		if err := n.Profile.Validate(); err != nil {
			return err
		}
		ids[n.ID] = true
		total += n.MaxOutputTokens
	}
	if total > w.MaxTotalTokens {
		return errors.New("workflow token budget exceeded")
	}
	for _, n := range w.Nodes {
		for _, d := range n.DependsOn {
			if !ids[d] || d == n.ID {
				return errors.New("invalid workflow dependency")
			}
		}
	}
	if workflowHasCycle(w.Nodes) {
		return errors.New("workflow dependency cycle")
	}
	return nil
}

type WorkflowResult struct {
	Results map[string]RoleRunResult `json:"results"`
	Errors  map[string]string        `json:"errors,omitempty"`
	Order   []string                 `json:"order"`
}
type WorkflowExecutor struct{ runner RoleRunner }

func NewWorkflowExecutor(r RoleRunner) *WorkflowExecutor { return &WorkflowExecutor{runner: r} }
func (e *WorkflowExecutor) Execute(ctx context.Context, runID string, workflow Workflow, packet EvidencePacket) (WorkflowResult, error) {
	if e == nil || e.runner == nil {
		return WorkflowResult{}, errors.New("role runner is required")
	}
	if err := workflow.Validate(); err != nil {
		return WorkflowResult{}, err
	}
	fingerprint, err := packet.Fingerprint()
	if err != nil {
		return WorkflowResult{}, err
	}
	result := WorkflowResult{Results: map[string]RoleRunResult{}, Errors: map[string]string{}, Order: []string{}}
	pending := map[string]WorkflowNode{}
	for _, n := range workflow.Nodes {
		pending[n.ID] = n
	}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		ready := []WorkflowNode{}
		for _, n := range pending {
			ok := true
			for _, d := range n.DependsOn {
				if _, done := result.Results[d]; !done {
					if _, failed := result.Errors[d]; failed {
						result.Errors[n.ID] = "dependency failed"
						delete(pending, n.ID)
					}
					ok = false
					break
				}
			}
			if ok {
				ready = append(ready, n)
			}
		}
		if len(ready) == 0 {
			if len(pending) == 0 {
				break
			}
			return result, errors.New("workflow cannot make progress")
		}
		sort.Slice(ready, func(i, j int) bool { return ready[i].ID < ready[j].ID })
		if len(ready) > workflow.MaxFanOut {
			ready = ready[:workflow.MaxFanOut]
		}
		type completed struct {
			id    string
			value RoleRunResult
			err   error
		}
		ch := make(chan completed, len(ready))
		var wg sync.WaitGroup
		for _, node := range ready {
			delete(pending, node.ID)
			wg.Add(1)
			go func(n WorkflowNode) {
				defer wg.Done()
				input := RoleRunInput{RunID: runID, NodeID: n.ID, Profile: n.Profile, EvidencePacketFingerprint: fingerprint, Packet: packet, MaxOutputTokens: n.MaxOutputTokens}
				var value RoleRunResult
				var runErr error
				for attempt := 0; attempt <= n.Retries; attempt++ {
					value, runErr = e.runner.Run(ctx, input)
					if runErr == nil {
						runErr = value.Validate(input)
					}
					if runErr == nil || ctx.Err() != nil {
						break
					}
				}
				ch <- completed{id: n.ID, value: value, err: runErr}
			}(node)
		}
		wg.Wait()
		close(ch)
		batch := []completed{}
		for item := range ch {
			batch = append(batch, item)
		}
		sort.Slice(batch, func(i, j int) bool { return batch[i].id < batch[j].id })
		for _, item := range batch {
			result.Order = append(result.Order, item.id)
			if item.err != nil {
				result.Errors[item.id] = item.err.Error()
			} else {
				result.Results[item.id] = item.value
			}
		}
	}
	return result, nil
}
func workflowHasCycle(nodes []WorkflowNode) bool {
	deps := map[string][]string{}
	for _, n := range nodes {
		deps[n.ID] = n.DependsOn
	}
	state := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		if state[id] == 1 {
			return true
		}
		if state[id] == 2 {
			return false
		}
		state[id] = 1
		for _, d := range deps[id] {
			if visit(d) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	for id := range deps {
		if visit(id) {
			return true
		}
	}
	return false
}

var _ = fmt.Sprintf
