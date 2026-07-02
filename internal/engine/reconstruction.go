package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type ReconstructionResult struct {
	Triggered       bool     `json:"triggered"`
	RequiresConfirm bool     `json:"requires_confirm"`
	Strategy        string   `json:"strategy,omitempty"`
	Confidence      float64  `json:"confidence"`
	ReconstructedID string   `json:"reconstructed_id,omitempty"`
	DerivedFrom     []string `json:"derived_from,omitempty"`
	Candidate       string   `json:"candidate,omitempty"`
	Reason          string   `json:"reason"`
}

type ReconstructionEngine struct {
	store    *sqlite.Store
	pipeline *WritePipeline
	gap      *GapDetector
}

func NewReconstructionEngine(store *sqlite.Store, pipeline *WritePipeline) *ReconstructionEngine {
	return &ReconstructionEngine{
		store:    store,
		pipeline: pipeline,
		gap:      NewGapDetector(store),
	}
}

func (r *ReconstructionEngine) Reconstruct(ctx context.Context, workspace, query string, userConfirmed bool) (*ReconstructionResult, error) {
	if r.reconstructionLoopGuard(ctx, workspace, query) {
		return &ReconstructionResult{
			Triggered:  false,
			Confidence: 0,
			Reason:     "reconstruction loop guard triggered for this query",
		}, nil
	}
	gap, err := r.gap.Detect(ctx, workspace, query)
	if err != nil {
		return nil, err
	}
	if !gap.Triggered {
		return &ReconstructionResult{
			Triggered:  false,
			Confidence: gap.Score,
			Reason:     gap.Reason,
		}, nil
	}
	content, confidence, derived, strategy := chooseReconstructionCandidate(query, gap.Tombstones)
	if confidence < 0.5 || strings.TrimSpace(content) == "" {
		return &ReconstructionResult{
			Triggered:   false,
			Confidence:  confidence,
			DerivedFrom: derived,
			Reason:      "candidate confidence below threshold",
		}, nil
	}
	if confidence < 0.7 && !userConfirmed {
		return &ReconstructionResult{
			Triggered:       false,
			RequiresConfirm: true,
			Strategy:        strategy,
			Confidence:      confidence,
			DerivedFrom:     derived,
			Candidate:       content,
			Reason:          "candidate requires user confirmation",
		}, nil
	}
	if confidence < 0.7 && userConfirmed {
		strategy = strategy + "+user-confirm"
	}
	tombstonesByMemoryID := make(map[string]core.MemoryTombstone, len(gap.Tombstones))
	for _, t := range gap.Tombstones {
		tombstonesByMemoryID[t.MemoryID] = t
	}
	tombstoneIDs := make([]string, 0, len(derived))
	for _, id := range derived {
		if ts, ok := tombstonesByMemoryID[id]; ok {
			tombstoneIDs = append(tombstoneIDs, ts.ID)
		}
	}
	tags := []string{"reconstructed", "strategy:" + strategy}
	for i, d := range derived {
		if i >= 3 {
			break
		}
		tags = append(tags, "derived_from:"+d)
	}
	write, err := r.pipeline.Write(ctx, WriteInput{
		Workspace: workspace,
		Type:      core.SemanticMemory,
		Content:   content,
		Tags:      tags,
		Source:    core.MemorySource{Type: core.SourceReconstruction},
		Mode:      ExtractFast,
	})
	if err != nil {
		return nil, err
	}
	if len(tombstoneIDs) > 0 {
		_ = r.store.AddReconstructionLineage(ctx, write.ID, tombstoneIDs)
	}
	return &ReconstructionResult{
		Triggered:       true,
		RequiresConfirm: false,
		Strategy:        strategy,
		Confidence:      confidence,
		ReconstructedID: write.ID,
		DerivedFrom:     derived,
		Reason:          "reconstructed from tombstone fragments",
	}, nil
}

func chooseReconstructionCandidate(query string, ts []core.MemoryTombstone) (string, float64, []string, string) {
	if len(ts) == 0 {
		return "", 0, nil, ""
	}
	type scored struct {
		t core.MemoryTombstone
		s float64
	}
	query = strings.ToLower(strings.TrimSpace(query))
	items := make([]scored, 0, len(ts))
	hasOutcome := false
	for _, t := range ts {
		s := 0.35
		if strings.Contains(strings.ToLower(t.FragmentSummary), query) {
			s += 0.3
		}
		if t.Type == core.OutcomeMemory {
			hasOutcome = true
			s += 0.12
		}
		if t.Type == core.SemanticMemory || t.Type == core.ProceduralMemory {
			s += 0.15
		}
		items = append(items, scored{t: t, s: s})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].s > items[j].s })
	top := items
	if len(top) > 3 {
		top = top[:3]
	}
	fragments := make([]string, 0, len(top))
	derived := make([]string, 0, len(top))
	score := 0.0
	for _, it := range top {
		if strings.TrimSpace(it.t.FragmentSummary) != "" {
			fragments = append(fragments, it.t.FragmentSummary)
		}
		derived = append(derived, it.t.MemoryID)
		score += it.s
	}
	if len(top) > 0 {
		score = score / float64(len(top))
	}
	strategy := "fragment+gap-signal"
	if hasOutcome && (strings.Contains(query, "why") || strings.Contains(query, "fail") || strings.Contains(query, "outcome")) {
		strategy = "outcome-fragment"
	}
	if strings.Contains(query, "source") || strings.Contains(query, "file") || strings.Contains(query, "where") {
		strategy = strategy + "+source-hint"
	}
	content := fmt.Sprintf("Reconstructed memory (%s) for query %q from historical fragments: %s", strategy, query, strings.Join(fragments, " | "))
	return content, score, derived, strategy
}

func (r *ReconstructionEngine) reconstructionLoopGuard(ctx context.Context, workspace, query string) bool {
	memories, err := r.store.ListMemoriesByWorkspace(ctx, workspace)
	if err != nil {
		return false
	}
	query = strings.ToLower(strings.TrimSpace(query))
	count := 0
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	for _, m := range memories {
		if m.UpdatedAt.Before(cutoff) {
			continue
		}
		if !containsTag(m.Tags, "reconstructed") {
			continue
		}
		if strings.Contains(strings.ToLower(m.Content), query) {
			count++
		}
		if count >= 3 {
			return true
		}
	}
	return false
}

func containsTag(tags []string, wanted string) bool {
	wanted = strings.ToLower(strings.TrimSpace(wanted))
	for _, t := range tags {
		if strings.ToLower(strings.TrimSpace(t)) == wanted {
			return true
		}
	}
	return false
}
