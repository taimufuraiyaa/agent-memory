package harnesscontext

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
)

// SensitivityAdvisor says how sensitive a gathered chunk is at least. Its answer can only
// raise the class the deterministic adapters gave the chunk: a chunk is never made more
// shareable by advice, and a chunk that advice raises is withheld from every provider whose
// class does not cover it, by the same gate as any other chunk.
type SensitivityAdvisor interface {
	Sensitivity(ctx context.Context, c Chunk) (Sensitivity, error)
}

const (
	// maxSensitivityAsks bounds the chunks asked about in one assembly.
	maxSensitivityAsks = 16
	// sensitivityTimeout bounds how long an assembly waits for all of it.
	sensitivityTimeout = 4 * time.Second
	sensitivityCache   = 256
	maxPathNote        = 200
)

// raiseSensitivity asks the advisor about the repository chunks a run gathered and raises
// the class of those it says are sensitive. Only a chunk that could still be shared with
// the provider (below the eligibility ceiling, not pinned, not already sensitive) is asked
// about; run chunks and instructions are never judged. Anything wrong with the advisor, a
// panic, a deadline or an out-of-range answer, leaves the chunk as it was.
func raiseSensitivity(ctx context.Context, advisor SensitivityAdvisor, ceiling Sensitivity, chunks []Chunk) []Chunk {
	out := append([]Chunk(nil), chunks...)
	ctx, cancel := context.WithTimeout(ctx, sensitivityTimeout)
	defer cancel()
	asked := 0
	for i := range out {
		c := out[i]
		if c.Source != SourceRepository || c.Pinned || c.Sensitivity >= SensitivitySensitive || c.Sensitivity > ceiling || ctx.Err() != nil {
			continue
		}
		if asked == maxSensitivityAsks {
			break
		}
		asked++
		if got, ok := askSensitivity(ctx, advisor, c); ok && got > c.Sensitivity && got.valid() {
			out[i].Sensitivity = got
		}
	}
	return out
}

func askSensitivity(ctx context.Context, advisor SensitivityAdvisor, c Chunk) (Sensitivity, bool) {
	type result struct {
		s   Sensitivity
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if recover() != nil {
				done <- result{err: errors.New("sensitivity advisor panicked")}
			}
		}()
		s, err := advisor.Sensitivity(ctx, c)
		done <- result{s, err}
	}()
	select {
	case got := <-done:
		return got.s, got.err == nil
	case <-ctx.Done():
		return 0, false
	}
}

// ServiceSensitivityAdvisor routes the judgment through the decision service. A chunk is
// described by its redacted path, the one piece of project text any decision sends, so the
// kind is internal and is asked only when the provider may receive that class. Verdicts are
// remembered per source and revision, so the same file is not asked about every turn.
type ServiceSensitivityAdvisor struct {
	service *harnessdecide.Service
	redact  func(string) string

	mu    sync.Mutex
	known map[string]Sensitivity
	order []string
}

var _ SensitivityAdvisor = (*ServiceSensitivityAdvisor)(nil)

// NewServiceSensitivityAdvisor builds the advisor. redact removes secrets and personal data
// from a path before it is sent; nil sends nothing sensitive-looking only if the path is
// already clean, so callers should always supply one.
func NewServiceSensitivityAdvisor(service *harnessdecide.Service, redact func(string) string) *ServiceSensitivityAdvisor {
	if redact == nil {
		redact = func(s string) string { return s }
	}
	return &ServiceSensitivityAdvisor{service: service, redact: redact, known: map[string]Sensitivity{}}
}

func (a *ServiceSensitivityAdvisor) Sensitivity(ctx context.Context, c Chunk) (Sensitivity, error) {
	if a == nil || a.service == nil {
		return SensitivityPublic, errors.New("no decision service")
	}
	key := c.Ref + "@" + c.Revision
	a.mu.Lock()
	known, ok := a.known[key]
	a.mu.Unlock()
	if ok {
		return known, nil
	}
	p := strings.TrimSpace(a.redact(c.Ref))
	if len(p) > maxPathNote {
		p = p[len(p)-maxPathNote:] // the end of a path says the most about the file
	}
	facts := harnessdecide.Facts{"d": strings.Count(p, "/"), "z": min(len(c.Text)/1024, 1_000_000)}
	verdict, out := a.service.FileSensitivity(ctx, p, facts)
	switch {
	case verdict == harnessdecide.SensitivitySensitive && out.Cautious:
		return SensitivitySensitive, nil // not remembered: it is a guess made without an answer
	case out.Status != harnessdecide.StatusApplied:
		return SensitivityPublic, errors.New("decision " + string(out.Status))
	}
	level := SensitivityPublic
	if verdict == harnessdecide.SensitivitySensitive {
		level = SensitivitySensitive
	}
	a.remember(key, level)
	return level, nil
}

func (a *ServiceSensitivityAdvisor) remember(key string, level Sensitivity) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.known[key]; !ok {
		a.order = append(a.order, key)
	}
	a.known[key] = level
	for len(a.order) > sensitivityCache {
		delete(a.known, a.order[0])
		a.order = a.order[1:]
	}
}
