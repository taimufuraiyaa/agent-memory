// Package harnessworker coordinates background workers: child runs that share the parent's
// ceilings, never run the same subgoal twice, never write the same file at once, and stop when
// the parent stops. A worker is an ordinary run; nothing here widens what a run may do.
package harnessworker

import (
	"errors"
	"sort"
	"sync"
)

// ErrConflict is returned when another run owns a file.
var ErrConflict = errors.New("another worker owns this file")

// Claims is the registry of which run may change which file. A run takes all the files of an
// action or none of them, so two workers cannot interleave edits to one file. Ownership is
// released when the run ends.
type Claims struct {
	mu     sync.Mutex
	owners map[string]string // path -> run
	byRun  map[string]map[string]struct{}
	max    int
}

// NewClaims returns an empty registry holding at most max paths (default 4096).
func NewClaims(max int) *Claims {
	if max <= 0 {
		max = 4096
	}
	return &Claims{owners: map[string]string{}, byRun: map[string]map[string]struct{}{}, max: max}
}

// Claim gives run every path, or none of them and ErrConflict if another run holds any.
func (c *Claims) Claim(run string, paths []string) error {
	if run == "" {
		return errors.New("a claim needs a run")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	fresh := 0
	for _, p := range paths {
		owner, held := c.owners[p]
		if held && owner != run {
			return ErrConflict
		}
		if !held {
			fresh++
		}
	}
	if len(c.owners)+fresh > c.max {
		return errors.New("too many files are claimed")
	}
	if c.byRun[run] == nil {
		c.byRun[run] = map[string]struct{}{}
	}
	for _, p := range paths {
		c.owners[p] = run
		c.byRun[run][p] = struct{}{}
	}
	return nil
}

// Release frees everything a run holds.
func (c *Claims) Release(run string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for p := range c.byRun[run] {
		if c.owners[p] == run {
			delete(c.owners, p)
		}
	}
	delete(c.byRun, run)
}

// Owner reports who holds a path.
func (c *Claims) Owner(path string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	run, ok := c.owners[path]
	return run, ok
}

// Held lists a run's paths in order.
func (c *Claims) Held(run string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.byRun[run]))
	for p := range c.byRun[run] {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
