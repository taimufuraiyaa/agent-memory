package harnessworker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

// Subgoal is one piece of work for a worker. Key names it for the caller.
type Subgoal struct {
	Key  string
	Goal string
}

// Result is how one worker ended. It carries codes and artifact metadata, never goals or output.
type Result struct {
	Key       string
	RunID     string
	State     harnessrun.State
	Code      string
	Artifacts []harnessrun.ArtifactMeta
	// Merged names the kept subgoal when this one was folded into it and so never ran.
	Merged string
}

// Config describes a coordinator.
type Config struct {
	Runs *harnessrun.Manager
	// MaxParallel is how many workers run at once. One serializes the work, which is also the
	// rollback: nothing else about the behavior changes.
	MaxParallel int
	// Decisions, when set, may point out that a subgoal repeats one already planned. It is
	// advice about work to skip; it can never add a subgoal or widen a worker.
	Decisions *harnessdecide.Service
	// Redact removes secrets from a subgoal before it is described to Decisions.
	Redact func(string) string
	// Poll is how often worker and parent states are read; the default is 50 ms.
	Poll time.Duration
}

// MaxSubgoals bounds one fan-out.
const MaxSubgoals = 8

// maxWorkerWait is the longest the coordinator waits for one worker; a worker has its own budget.
const maxWorkerWait = 70 * time.Minute

// Coordinator runs subgoals as child runs.
type Coordinator struct {
	cfg      Config
	claims   *Claims
	snapshot *Snapshot
}

// New validates a configuration.
func New(cfg Config) (*Coordinator, error) {
	if cfg.Runs == nil {
		return nil, errors.New("harnessworker: a run manager is required")
	}
	if cfg.MaxParallel < 1 {
		cfg.MaxParallel = 1
	}
	if cfg.MaxParallel > MaxSubgoals {
		cfg.MaxParallel = MaxSubgoals
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 50 * time.Millisecond
	}
	if cfg.Redact == nil {
		cfg.Redact = func(s string) string { return s }
	}
	return &Coordinator{cfg: cfg, claims: NewClaims(0), snapshot: NewSnapshot(0)}, nil
}

// Claims is the ownership registry workers' policies consult.
func (c *Coordinator) Claims() *Claims { return c.claims }

// Snapshot is the shared set of revisions the workers have read.
func (c *Coordinator) Snapshot() *Snapshot { return c.snapshot }

// normalize makes two statements of the same goal compare equal.
func normalize(goal string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(goal) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		case !space && b.Len() > 0:
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// Plan removes subgoals that repeat an earlier one. Exact repeats (after normalization) are
// always merged. When a decision service is configured it is asked about the rest, and its
// answer is used only if it names a subgoal already kept: merging only removes work.
func (c *Coordinator) Plan(ctx context.Context, proposed []Subgoal) (keep []Subgoal, merged map[string]string, err error) {
	if len(proposed) == 0 || len(proposed) > MaxSubgoals {
		return nil, nil, fmt.Errorf("harnessworker: between 1 and %d subgoals are required", MaxSubgoals)
	}
	merged = map[string]string{}
	seenKey := map[string]bool{}
	byText := map[string]string{}
	for _, s := range proposed {
		n := normalize(s.Goal)
		if s.Key == "" || n == "" || seenKey[s.Key] {
			return nil, nil, errors.New("harnessworker: every subgoal needs a unique key and a goal")
		}
		seenKey[s.Key] = true
		if kept, dup := byText[n]; dup {
			merged[s.Key] = kept
			continue
		}
		if c.cfg.Decisions != nil && len(keep) > 0 {
			existing := make([]harnessdecide.Item, len(keep))
			for i, k := range keep {
				existing[i] = harnessdecide.Item{ID: k.Key, Note: clipNote(c.cfg.Redact(k.Goal))}
			}
			if match, _ := c.cfg.Decisions.Subgoals(ctx, existing, harnessdecide.Item{ID: s.Key, Note: clipNote(c.cfg.Redact(s.Goal))}); match != "" {
				merged[s.Key] = match
				continue
			}
		}
		byText[n] = s.Key
		keep = append(keep, s)
	}
	return keep, merged, nil
}

func clipNote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200]
		for len(s) > 0 && s[len(s)-1] >= 0x80 && s[len(s)-1] < 0xC0 {
			s = s[:len(s)-1]
		}
	}
	if s == "" {
		return "subgoal"
	}
	return s
}

// Run plans, starts one child run per remaining subgoal beneath the parent, and waits for them.
// Each child gets an equal share of what the parent has left, so the children together cannot
// exceed the parent's ceilings, and every child is cancelled if the parent stops. Workers that
// cannot start, because the budget or depth does not allow it, are reported, not retried.
func (c *Coordinator) Run(ctx context.Context, owner harnessrun.Owner, parentID string, proposed []Subgoal) ([]Result, error) {
	keep, merged, err := c.Plan(ctx, proposed)
	if err != nil {
		return nil, err
	}
	parent, err := c.cfg.Runs.Status(ctx, owner, parentID)
	if err != nil {
		return nil, err
	}
	share, err := split(parent, len(keep), c.cfg.MaxParallel)
	if err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(proposed))
	for key, into := range merged {
		results = append(results, Result{Key: key, Merged: into})
	}
	workCtx, stop := context.WithCancel(ctx)
	defer stop()
	go c.watchParent(workCtx, owner, parentID, stop)

	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, c.cfg.MaxParallel)
	for _, s := range keep {
		wg.Add(1)
		slots <- struct{}{}
		go func(s Subgoal) {
			defer wg.Done()
			defer func() { <-slots }()
			r := c.runOne(workCtx, owner, parentID, s, share)
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	return results, nil
}

// split divides what the parent has left among the workers that will run together.
func split(parent harnessrun.Status, workers, parallel int) (harnessrun.Budget, error) {
	if parent.State != harnessrun.StateRunning {
		return harnessrun.Budget{}, fmt.Errorf("harnessworker: the parent is %s", parent.State)
	}
	n := max(workers, 1)
	left := harnessrun.Budget{
		MaxTurns:       (parent.Budget.MaxTurns - parent.Usage.Turns) / n,
		MaxActive:      (parent.Budget.MaxActive - time.Duration(parent.Usage.ActiveMillis)*time.Millisecond) / time.Duration(max(parallel, 1)),
		MaxToolCalls:   (parent.Budget.MaxToolCalls - parent.Usage.ToolCalls) / n,
		MaxOutputBytes: (parent.Budget.MaxOutputBytes - parent.Usage.OutputBytes) / n,
		MaxSpendMicros: (parent.Budget.MaxSpendMicros - parent.Usage.SpendMicros) / int64(n),
		MaxDepth:       parent.Budget.MaxDepth,
	}
	if left.MaxTurns < 1 || left.MaxActive < time.Second || left.MaxToolCalls < 1 || left.MaxOutputBytes < 1 || left.MaxSpendMicros < 1 {
		return harnessrun.Budget{}, errors.New("harnessworker: the parent has too little budget left to share")
	}
	return left, nil
}

func childKey(parentID, key string) string {
	sum := sha256.Sum256([]byte(parentID + "\x00" + key))
	return "worker-" + hex.EncodeToString(sum[:])[:24]
}

func (c *Coordinator) runOne(ctx context.Context, owner harnessrun.Owner, parentID string, s Subgoal, share harnessrun.Budget) Result {
	result := Result{Key: s.Key, State: harnessrun.StateFailed, Code: "worker_not_started"}
	started, err := c.cfg.Runs.StartChild(ctx, owner, parentID, harnessrun.StartRequest{IdempotencyKey: childKey(parentID, s.Key), Goal: s.Goal, Budget: share})
	if err != nil {
		return result
	}
	result.RunID = started.ID
	defer c.claims.Release(started.ID)
	ticker := time.NewTicker(c.cfg.Poll)
	defer ticker.Stop()
	deadline := time.Now().Add(maxWorkerWait)
	for {
		status, err := c.cfg.Runs.Status(context.WithoutCancel(ctx), owner, started.ID)
		if err != nil {
			result.Code = "worker_unreadable"
			return result
		}
		if status.State.Terminal() {
			return Result{Key: s.Key, RunID: started.ID, State: status.State, Code: status.Code, Artifacts: status.Artifacts}
		}
		if ctx.Err() != nil && status.State != harnessrun.StateCancelling {
			_, _ = c.cfg.Runs.Cancel(context.WithoutCancel(ctx), owner, started.ID, harnessrun.Mutation{IdempotencyKey: childKey(started.ID, "cancel"), ExpectedGeneration: status.Generation})
		}
		if time.Now().After(deadline) {
			result.Code = "worker_timeout"
			return result
		}
		<-ticker.C
	}
}

// watchParent stops the workers as soon as the parent is no longer running.
func (c *Coordinator) watchParent(ctx context.Context, owner harnessrun.Owner, parentID string, stop context.CancelFunc) {
	ticker := time.NewTicker(c.cfg.Poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status, err := c.cfg.Runs.Status(ctx, owner, parentID)
			if err != nil || (status.State != harnessrun.StateRunning && status.State != harnessrun.StateNeedsAttention) {
				stop()
				return
			}
		}
	}
}
