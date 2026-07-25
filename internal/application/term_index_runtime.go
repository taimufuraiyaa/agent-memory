package application

import (
	"context"
	"sync"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/locator"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type termIndexSnapshot struct {
	workspace        string
	checksum         string
	corpusGeneration int64
	filterGeneration int64
	filter           *engine.TermBloom
}

// TermIndexRuntime caches validated immutable Bloom snapshots per application runtime.
type TermIndexRuntime struct {
	mu       sync.RWMutex
	snapshot *termIndexSnapshot
}

func NewTermIndexRuntime() *TermIndexRuntime {
	return &TermIndexRuntime{}
}

// Probe evaluates Bloom membership for the configured rollout mode.
func (r *TermIndexRuntime) Probe(ctx context.Context, store *sqlite.Store, workspace string, terms []string, operator TermOperator) TermPrefilter {
	mode := engine.TermBloomMode()
	if mode == engine.TermBloomOff {
		return TermPrefilter{Decision: "bypassed", Reason: "disabled", Mode: string(mode)}
	}
	state, err := store.GetTermIndexState(ctx, workspace)
	if err != nil {
		return TermPrefilter{Decision: "bypassed", Reason: "state_read_error", Mode: string(mode)}
	}
	if state == nil {
		return TermPrefilter{Decision: "bypassed", Reason: "state_missing", Mode: string(mode)}
	}
	base := TermPrefilter{
		Decision:         "bypassed",
		CorpusGeneration: state.CorpusGeneration,
		FilterGeneration: state.FilterGeneration,
		Mode:             string(mode),
	}
	if state.State != sqlite.TermIndexReady {
		base.Reason = "state_" + string(state.State)
		return base
	}
	if state.CorpusGeneration != state.FilterGeneration {
		base.Reason = "generation_mismatch"
		return base
	}
	if state.FormatVersion != engine.TermBloomFormatVersion ||
		state.NormalizationVersion != locator.NormalizationVersion ||
		state.ExtractorVersion != locator.ExtractorVersion ||
		state.HashVersion != engine.TermBloomHashVersion {
		base.Reason = "version_mismatch"
		return base
	}
	if engine.TermBloomChecksum(state.Bitmap) != state.Checksum {
		base.Reason = "checksum_mismatch"
		return base
	}
	health := evaluateTermIndexStatus(state)
	if !health.GatingEligible {
		base.Reason = health.RebuildReason
		return base
	}

	filter, err := r.loadSnapshot(state)
	if err != nil {
		base.Reason = "bitmap_invalid"
		return base
	}
	presentCount := 0
	for _, term := range terms {
		if filter.MightContain(term) {
			presentCount++
		}
	}
	negative := false
	if operator == TermOperatorOR {
		negative = presentCount == 0
	} else {
		negative = presentCount != len(terms)
	}
	base.Consulted = true
	base.Shadow = mode == engine.TermBloomShadow
	if negative {
		base.Decision = "negative"
		if base.Shadow {
			base.Reason = "shadow_mode"
		}
	} else {
		base.Decision = "maybe"
		base.Reason = ""
	}
	return base
}

func (r *TermIndexRuntime) loadSnapshot(state *sqlite.TermIndexState) (*engine.TermBloom, error) {
	r.mu.RLock()
	cached := r.snapshot
	if cached != nil && cached.workspace == state.Workspace && cached.checksum == state.Checksum &&
		cached.corpusGeneration == state.CorpusGeneration && cached.filterGeneration == state.FilterGeneration {
		filter := cached.filter
		r.mu.RUnlock()
		return filter, nil
	}
	r.mu.RUnlock()

	filter, err := engine.LoadTermBloom(state.Bitmap, state.BitCount, state.HashCount)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.snapshot = &termIndexSnapshot{
		workspace:        state.Workspace,
		checksum:         state.Checksum,
		corpusGeneration: state.CorpusGeneration,
		filterGeneration: state.FilterGeneration,
		filter:           filter,
	}
	r.mu.Unlock()
	return filter, nil
}
