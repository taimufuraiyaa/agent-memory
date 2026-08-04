package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

// DeepConsolidationResult summarises a cross-session consolidation run.
type DeepConsolidationResult struct {
	SessionsScanned    int   `json:"sessions_scanned"`
	MemoriesMerged     int   `json:"memories_merged"`
	ProceduralPromoted int   `json:"procedural_promoted"`
	ConflictsResolved  int   `json:"conflicts_resolved"`
	DurationMs         int64 `json:"duration_ms"`
	DryRun             bool  `json:"dry_run"`
}

// DeepConsolidationOptions controls the deep consolidation pass.
type DeepConsolidationOptions struct {
	Workspace string
	DaysBack  int  // lookback window in days (default 30)
	DryRun    bool // print what would happen without writing
	Mode      MergeMode
}

// DeepConsolidationEngine performs cross-session consolidation.
type DeepConsolidationEngine struct {
	store    *sqlite.Store
	pipeline *WritePipeline
}

// NewDeepConsolidationEngine constructs the engine.
func NewDeepConsolidationEngine(store *sqlite.Store, pipeline *WritePipeline) *DeepConsolidationEngine {
	return &DeepConsolidationEngine{store: store, pipeline: pipeline}
}

// Run executes a deep consolidation pass across sessions.
func (e *DeepConsolidationEngine) Run(ctx context.Context, opts DeepConsolidationOptions) (*DeepConsolidationResult, error) {
	if opts.DaysBack <= 0 {
		opts.DaysBack = 30
	}
	if opts.Mode == "" {
		opts.Mode = MergeFast
	}

	start := time.Now()
	result := &DeepConsolidationResult{DryRun: opts.DryRun}

	cutoff := time.Now().AddDate(0, 0, -opts.DaysBack)

	all, err := e.store.ListMemoriesByWorkspace(ctx, opts.Workspace)
	if err != nil {
		return nil, fmt.Errorf("deep consolidation: list memories: %w", err)
	}

	// Filter to memories within the lookback window that are not superseded.
	recent := make([]core.MemoryEntry, 0, len(all))
	sessionSet := map[string]struct{}{}
	for _, m := range all {
		if m.SupersededBy != nil {
			continue
		}
		if m.CreatedAt.After(cutoff) {
			recent = append(recent, m)
			// Session provenance lives in the source payload (source_json).
			if sid := m.Source.SessionID; sid != "" {
				sessionSet[sid] = struct{}{}
			}
		}
	}
	result.SessionsScanned = len(sessionSet)

	// --- Pass 1: Cross-session episodic clustering → semantic merge ---
	episodics := filterByType(recent, core.EpisodicMemory)
	clusters := clusterEpisodes(episodics)
	for _, cluster := range clusters {
		if len(cluster) < 5 {
			// Cross-session merge requires a stronger signal than within-session.
			continue
		}
		if !spansMultipleSessions(cluster) {
			// Pass 1 merges across sessions by design; a single-session cluster
			// belongs to the within-session consolidation engine.
			continue
		}
		if opts.DryRun {
			result.MemoriesMerged += len(cluster)
			continue
		}
		summary := mergeCluster(cluster, opts.Mode)
		wr, err := e.pipeline.Write(ctx, WriteInput{
			Workspace: opts.Workspace,
			Type:      core.SemanticMemory,
			Content:   summary,
			Source:    core.MemorySource{Type: core.SourceConsolidation},
			Mode:      ExtractFast,
		})
		if err != nil {
			return nil, err
		}
		ids := memoryIDs(cluster)
		_ = e.store.MarkSuperseded(ctx, ids, wr.ID)
		result.MemoriesMerged += len(cluster)
	}

	// --- Pass 2: Repeated failure pattern → procedural rule ---
	outcomes := filterByType(recent, core.OutcomeMemory)
	failureGroups := groupFailuresByApproach(outcomes)
	for approach, group := range failureGroups {
		if len(group) < 3 {
			continue
		}
		reason := commonReason(group)
		content := fmt.Sprintf("Avoid approach: %s. Reason: %s (failed %d times across sessions)", approach, reason, len(group))
		if opts.DryRun {
			result.ProceduralPromoted++
			continue
		}
		// Version instead of duplicating: only insert when no active rule with
		// the same normalized essence exists; otherwise the freshly written rule
		// supersedes the prior one so re-runs over the same failure pattern do
		// not accumulate near-duplicate rules with varying reason strings.
		essence := ruleEssenceHash(content)
		wr, err := e.pipeline.Write(ctx, WriteInput{
			Workspace: opts.Workspace,
			Type:      core.ProceduralMemory,
			Content:   content,
			Source:    core.MemorySource{Type: core.SourceConsolidation},
			Tags:      []string{"promoted-from-failures"},
			Mode:      ExtractFast,
		})
		if err != nil {
			return nil, err
		}
		if err := e.supersedeEquivalentRules(ctx, all, essence, wr.ID); err != nil {
			return nil, err
		}
		result.ProceduralPromoted++
	}

	result.DurationMs = time.Since(start).Milliseconds()
	return result, nil
}

// filterByType returns memories of a specific type.
func filterByType(memories []core.MemoryEntry, t core.MemoryType) []core.MemoryEntry {
	out := make([]core.MemoryEntry, 0)
	for _, m := range memories {
		if m.Type == t {
			out = append(out, m)
		}
	}
	return out
}

// memoryIDs extracts IDs from a slice of memories.
func memoryIDs(memories []core.MemoryEntry) []string {
	ids := make([]string, 0, len(memories))
	for _, m := range memories {
		ids = append(ids, m.ID)
	}
	return ids
}

// groupFailuresByApproach groups outcome memories by their approach field.
func groupFailuresByApproach(outcomes []core.MemoryEntry) map[string][]core.MemoryEntry {
	groups := map[string][]core.MemoryEntry{}
	for _, m := range outcomes {
		if m.Outcome == nil || m.Outcome.Result != core.OutcomeFailure {
			continue
		}
		approach := strings.TrimSpace(m.Outcome.Approach)
		if approach == "" {
			approach = truncate(m.Content, 60)
		}
		groups[approach] = append(groups[approach], m)
	}
	return groups
}

// commonReason picks the most frequent reason string from a group.
func commonReason(group []core.MemoryEntry) string {
	freq := map[string]int{}
	for _, m := range group {
		if m.Outcome != nil && m.Outcome.Reason != "" {
			freq[m.Outcome.Reason]++
		}
	}
	best, bestCount := "", 0
	for r, c := range freq {
		if c > bestCount {
			best, bestCount = r, c
		}
	}
	if best == "" {
		return "repeated failures"
	}
	return best
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return core.TruncateUTF8(s, n) + "..."
}

// spansMultipleSessions reports whether a cluster draws from more than one
// distinct session, the pre-condition for a cross-session merge. Session
// provenance is read from the memory source payload, which is what the store
// persists in source_json.
func spansMultipleSessions(cluster []core.MemoryEntry) bool {
	seen := make(map[string]struct{}, len(cluster))
	for _, m := range cluster {
		if sid := m.Source.SessionID; sid != "" {
			seen[sid] = struct{}{}
		}
	}
	return len(seen) > 1
}

// supersedeEquivalentRules marks every active procedural rule in the workspace
// whose normalized essence matches the freshly written rule as superseded by
// it, recording a supersedes relation as evidence lineage. The write's own ID
// is excluded so an exact-content dedup never supersedes itself.
func (e *DeepConsolidationEngine) supersedeEquivalentRules(ctx context.Context, all []core.MemoryEntry, essence, newID string) error {
	if newID == "" {
		return nil
	}
	oldIDs := make([]string, 0, 1)
	for i := range all {
		m := &all[i]
		if m.Type != core.ProceduralMemory || m.SupersededBy != nil || m.ID == newID {
			continue
		}
		if ruleEssenceHash(m.Content) != essence {
			continue
		}
		oldIDs = append(oldIDs, m.ID)
	}
	if len(oldIDs) == 0 {
		return nil
	}
	if err := e.store.MarkSuperseded(ctx, oldIDs, newID); err != nil {
		return err
	}
	for _, id := range oldIDs {
		_ = e.store.AddRelation(ctx, newID, id, core.RelSupersedes, 1, map[string]string{"reason": "deep-consolidation-rule-refresh"})
	}
	return nil
}

// ruleEssenceHash returns a stable hash over the normalized essence of a
// generated procedural rule. Re-runs over the same failure pattern therefore
// produce the same essence even when the evidence text embedded timestamps,
// session ids, or a changed failure count.
func ruleEssenceHash(s string) string {
	sum := sha256.Sum256([]byte(normalizeRuleEssence(s)))
	return hex.EncodeToString(sum[:])
}

var (
	// ruleCountRE normalizes the generated evidence-count clause so a rule
	// refresh that only grows the failure count still matches its predecessor.
	ruleCountRE = regexp.MustCompile(`\(failed\s+\d+\s+times across sessions\)`)
	// ruleTimestampRE matches ISO-8601-like timestamps (with and without time).
	ruleTimestampRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?|\d{4}-\d{2}-\d{2}`)
	// ruleUUIDRE matches canonical UUID-form session/record ids.
	ruleUUIDRE = regexp.MustCompile(`\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	// ruleHexRunRE matches bare hex-ish session ids (e.g. "0000000a", commit shas).
	ruleHexRunRE = regexp.MustCompile(`\b[0-9a-fA-F]{8,}\b`)
	// ruleSessionRE matches explicit "session <id>" markers.
	ruleSessionRE = regexp.MustCompile(`(?i)\bsession\s*[-_ :]?\s*[0-9a-z][0-9a-z_-]{0,63}\b`)
)

// normalizeRuleEssence strips timestamps, session-id tokens, and the failure
// count clause, then collapses whitespace variance, so semantically equivalent
// generated rules hash identically.
func normalizeRuleEssence(s string) string {
	s = ruleCountRE.ReplaceAllString(s, "(failed N times across sessions)")
	s = ruleTimestampRE.ReplaceAllString(s, " ")
	s = ruleUUIDRE.ReplaceAllString(s, " ")
	// Session markers first: a hex session id is consumed together with the
	// "session" keyword before bare hex runs are stripped on their own.
	s = ruleSessionRE.ReplaceAllString(s, " ")
	s = ruleHexRunRE.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}
