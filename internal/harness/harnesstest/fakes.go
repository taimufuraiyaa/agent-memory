// Package harnesstest provides configurable fake providers for harness conformance.
// Later real providers reuse these fakes to prove they behave like the same
// contract under the same misbehavior without editing shared orchestration.
package harnesstest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
)

// Behavior scripts one fake provider. The zero value is a healthy, available
// provider that answers OK promptly.
type Behavior struct {
	Access        harness.AccessState // live state for every declared capability
	Outcome       harness.Outcome     // outcome returned in replies
	Delay         time.Duration       // time spent before replying
	IgnoreContext bool                // keep sleeping after cancellation
	Panic         bool                // panic inside the provider call
	Err           error               // transport-style error from the provider call
	StaleReply    bool                // echo an envelope for the next generation
	Oversize      bool                // reply larger than the request allows
	UnknownChoice bool                // decision selects a candidate it was not given
	ProbeErr      error               // failure while probing live access
	ProbeStale    bool                // probe reports another revision-zero access
	CloseErr      error               // failure while closing
	CloseDelay    time.Duration       // time Close takes, after it is counted
	RequestTool   string              // model replies ask to run this tool
	Script        []Step              // per-call model replies; the last step repeats
	CostMicros    int64               // cost reported by every model reply
	Usage         harness.Usage       // usage reported by every model reply
	ModelName     string              // provider-reported model identity
	Digest        string              // tool preparation digest (default derived from the tool ID)
	Paths         []string            // paths a prepared tool action reports it would change
	Preview       string              // preview a prepared tool action reports
	Escalate      []string            // escalation codes a prepared tool action reports
	Reason        string              // reason code a prepared tool action reports
	Audit         []string            // audit codes a tool answer reports
}

// Step scripts one model reply in a Behavior.Script.
type Step struct {
	Text      string
	ToolID    string
	Arguments string
	Outcome   harness.Outcome
}

// Counters records provider-side activity so tests can prove no call was made.
type Counters struct {
	Probes, Calls, Invokes, Closes atomic.Int32
	// Closed counts Close calls that have finished, after any CloseDelay, so a test can
	// tell whether a session was fully released at a given moment.
	Closed atomic.Int32
	// Refs holds the evidence references of the most recent model request.
	Refs atomic.Value
	// Requests holds the most recent harness.ModelRequest a fake model received.
	Requests atomic.Value

	historyMu sync.Mutex
	history   []harness.ModelRequest
}

// History returns every model request a fake received, oldest first, bounded.
func (c *Counters) History() []harness.ModelRequest {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()
	return append([]harness.ModelRequest(nil), c.history...)
}

func (c *Counters) record(q harness.ModelRequest) {
	c.historyMu.Lock()
	defer c.historyMu.Unlock()
	c.history = append(c.history, q)
	if len(c.history) > 256 {
		c.history = c.history[len(c.history)-256:]
	}
}

func (b Behavior) state() harness.AccessState {
	if b.Access == "" {
		return harness.AccessAvailable
	}
	return b.Access
}

func (b Behavior) outcome() harness.Outcome {
	if b.Outcome == "" {
		return harness.OutcomeOK
	}
	return b.Outcome
}

func (b Behavior) wait(ctx context.Context) error {
	if b.Delay <= 0 {
		return nil
	}
	if b.IgnoreContext {
		time.Sleep(b.Delay)
		return nil
	}
	select {
	case <-time.After(b.Delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b Behavior) fail(ctx context.Context, counters *Counters) error {
	counters.Calls.Add(1)
	if b.Panic {
		panic("scripted provider panic")
	}
	if err := b.wait(ctx); err != nil {
		return err
	}
	return b.Err
}

func (b Behavior) envelope(e harness.Envelope) harness.Envelope {
	if b.StaleReply {
		e.Scope.Generation++
	}
	return e
}

type base struct {
	Manifest harness.Manifest
	Behavior Behavior
	Counters *Counters
}

func (f *base) Probe(_ context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	f.Counters.Probes.Add(1)
	if f.Behavior.ProbeErr != nil {
		return harness.LiveAccess{}, f.Behavior.ProbeErr
	}
	access := harness.LiveAccess{Version: harness.ContractVersion, Provider: f.Manifest.ID, Scope: scope, Revision: 1,
		Capabilities: make(map[harness.CapabilityID]harness.AccessState, len(f.Manifest.Capabilities))}
	if f.Behavior.ProbeStale {
		access.Revision = 0
	}
	for _, capability := range f.Manifest.Capabilities {
		access.Capabilities[capability] = f.Behavior.state()
	}
	return access, nil
}

func (f *base) Close() error {
	f.Counters.Closes.Add(1)
	if f.Behavior.CloseDelay > 0 {
		time.Sleep(f.Behavior.CloseDelay)
	}
	f.Counters.Closed.Add(1)
	return f.Behavior.CloseErr
}

// Decision is a fake harness.DecisionProvider.
type Decision struct{ base }

func (f *Decision) Decide(ctx context.Context, q harness.DecisionQuestion) (harness.DecisionAnswer, error) {
	if err := f.Behavior.fail(ctx, f.Counters); err != nil {
		return harness.DecisionAnswer{}, err
	}
	selected := []string{q.Candidates[0]}
	if f.Behavior.UnknownChoice {
		selected = []string{"not-offered"}
	}
	if f.Behavior.Oversize {
		selected = []string{strings.Repeat("x", q.MaxBytes+1)}
	}
	return harness.DecisionAnswer{Envelope: f.Behavior.envelope(q.Envelope), Outcome: f.Behavior.outcome(), Selected: selected, Confidence: 0.9}, nil
}

// Model is a fake harness.ModelProvider.
type Model struct{ base }

func (f *Model) Generate(ctx context.Context, q harness.ModelRequest) (harness.ModelAnswer, error) {
	f.Counters.Refs.Store(append([]harness.EvidenceRef(nil), q.InputRefs...))
	f.Counters.Requests.Store(q)
	f.Counters.record(q)
	if err := f.Behavior.fail(ctx, f.Counters); err != nil {
		return harness.ModelAnswer{}, err
	}
	text, tool, arguments, outcome := "bounded model text", f.Behavior.RequestTool, "", f.Behavior.outcome()
	if n := len(f.Behavior.Script); n > 0 {
		index := int(f.Counters.Calls.Load()) - 1
		if index >= n {
			index = n - 1
		}
		step := f.Behavior.Script[index]
		text, tool, arguments = step.Text, step.ToolID, step.Arguments
		if step.Outcome != "" {
			outcome = step.Outcome
		}
	}
	if f.Behavior.Oversize {
		text = strings.Repeat("x", q.MaxBytes+1)
	}
	return harness.ModelAnswer{Envelope: f.Behavior.envelope(q.Envelope), Outcome: outcome, Text: text, ToolID: tool,
		ToolArguments: []byte(arguments), CostMicros: f.Behavior.CostMicros, Usage: f.Behavior.Usage, Model: f.Behavior.ModelName}, nil
}

// Tool is a fake harness.ToolProvider.
type Tool struct{ base }

func (f *Tool) Prepare(ctx context.Context, q harness.ToolRequest) (harness.PreparedAction, error) {
	if err := f.Behavior.fail(ctx, f.Counters); err != nil {
		return harness.PreparedAction{}, err
	}
	digest := f.Behavior.Digest
	if digest == "" {
		digest = "digest-" + q.ToolID
	}
	return harness.PreparedAction{Envelope: f.Behavior.envelope(q.Envelope), Outcome: f.Behavior.outcome(), Digest: digest, Summary: "prepared " + q.ToolID,
		Paths: f.Behavior.Paths, Preview: f.Behavior.Preview, Escalate: f.Behavior.Escalate, Reason: f.Behavior.Reason}, nil
}

func (f *Tool) Invoke(ctx context.Context, action harness.PreparedAction) (harness.ToolAnswer, error) {
	f.Counters.Invokes.Add(1)
	if f.Behavior.Panic {
		panic("scripted provider panic")
	}
	if err := f.Behavior.wait(ctx); err != nil {
		return harness.ToolAnswer{}, err
	}
	if f.Behavior.Err != nil {
		return harness.ToolAnswer{}, f.Behavior.Err
	}
	output := []byte("tool output")
	if f.Behavior.Oversize {
		output = []byte(strings.Repeat("x", action.MaxBytes+1))
	}
	return harness.ToolAnswer{Envelope: f.Behavior.envelope(action.Envelope), Outcome: f.Behavior.outcome(), Output: output, Audit: f.Behavior.Audit}, nil
}

// Register adds a fake of the manifest's kind to the registry and returns its
// counters. It is the same call a real provider's composition root makes.
func Register(registry *harness.Registry, manifest harness.Manifest, behavior Behavior) (*Counters, error) {
	counters := &Counters{}
	b := base{Manifest: manifest, Behavior: behavior, Counters: counters}
	var factory harness.Factory
	switch manifest.Kind {
	case harness.KindDecision:
		factory = func() (harness.Provider, error) { return &Decision{base: b}, nil }
	case harness.KindModel:
		factory = func() (harness.Provider, error) { return &Model{base: b}, nil }
	case harness.KindTool:
		factory = func() (harness.Provider, error) { return &Tool{base: b}, nil }
	default:
		return nil, errors.New("harnesstest: unknown kind")
	}
	return counters, registry.Register(manifest, factory)
}

// Manifest builds a valid manifest for the given identity and kind.
func Manifest(id string, kind harness.ProviderKind, capabilities ...string) harness.Manifest {
	ids := make([]harness.CapabilityID, len(capabilities))
	for i, capability := range capabilities {
		ids[i] = harness.CapabilityID(capability)
	}
	return harness.Manifest{Version: harness.ContractVersion, ID: harness.ProviderID(id), Kind: kind, Capabilities: ids}
}
