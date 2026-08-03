package application

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/locator"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

const maximumHealthyTermBloomFPP = 0.01

type TermIndexStatusReport struct {
	Workspace           string                 `json:"workspace"`
	State               sqlite.TermIndexStatus `json:"state"`
	Ready               bool                   `json:"ready"`
	GatingEligible      bool                   `json:"gating_eligible"`
	RebuildRequired     bool                   `json:"rebuild_required"`
	RebuildReason       string                 `json:"rebuild_reason,omitempty"`
	BitmapBytes         int                    `json:"bitmap_bytes"`
	BitCount            int64                  `json:"bit_count"`
	HashCount           int                    `json:"hash_count"`
	DistinctItemCount   int64                  `json:"distinct_item_count"`
	PlannedCapacity     int64                  `json:"planned_capacity"`
	CapacityUtilization float64                `json:"capacity_utilization"`
	EstimatedFPP        float64                `json:"estimated_fpp"`
	StaleDeleteCount    int64                  `json:"stale_delete_count"`
	CorpusGeneration    int64                  `json:"corpus_generation"`
	FilterGeneration    int64                  `json:"filter_generation"`
	ChecksumValid       bool                   `json:"checksum_valid"`
	VersionsValid       bool                   `json:"versions_valid"`
	LastRebuildAt       time.Time              `json:"last_rebuild_at,omitempty"`
	UpdatedAt           time.Time              `json:"updated_at,omitempty"`
}

func (s *MemoryService) TermIndexStatus(ctx context.Context, workspace string) (*TermIndexStatusReport, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("term index store is not available")
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, errors.New("workspace is required")
	}
	state, err := s.store.GetTermIndexState(ctx, workspace)
	if err != nil {
		return nil, err
	}
	report := evaluateTermIndexStatus(state)
	report.Workspace = workspace
	return &report, nil
}

func evaluateTermIndexStatus(state *sqlite.TermIndexState) TermIndexStatusReport {
	if state == nil {
		return TermIndexStatusReport{RebuildRequired: true, RebuildReason: "state_missing"}
	}
	report := TermIndexStatusReport{
		Workspace: state.Workspace, State: state.State, Ready: state.State == sqlite.TermIndexReady,
		BitmapBytes: len(state.Bitmap), BitCount: state.BitCount, HashCount: state.HashCount,
		DistinctItemCount: state.DistinctItemCount, PlannedCapacity: state.PlannedCapacity,
		EstimatedFPP: state.EstimatedFPP, StaleDeleteCount: state.StaleDeleteCount,
		CorpusGeneration: state.CorpusGeneration, FilterGeneration: state.FilterGeneration,
		LastRebuildAt: state.BuiltAt, UpdatedAt: state.UpdatedAt,
	}
	if state.PlannedCapacity > 0 {
		report.CapacityUtilization = float64(state.DistinctItemCount) / float64(state.PlannedCapacity)
	}
	report.ChecksumValid = engine.TermBloomChecksum(state.Bitmap) == state.Checksum
	report.VersionsValid = state.FormatVersion == engine.TermBloomFormatVersion &&
		state.NormalizationVersion == locator.NormalizationVersion &&
		state.ExtractorVersion == locator.ExtractorVersion &&
		state.HashVersion == engine.TermBloomHashVersion

	switch {
	case state.State != sqlite.TermIndexReady:
		report.RebuildReason = "state_" + string(state.State)
	case !report.VersionsValid:
		report.RebuildReason = "version_mismatch"
	case !report.ChecksumValid:
		report.RebuildReason = "checksum_mismatch"
	case state.CorpusGeneration != state.FilterGeneration:
		report.RebuildReason = "generation_mismatch"
	case state.PlannedCapacity > 0 && state.DistinctItemCount >= state.PlannedCapacity:
		report.RebuildReason = "capacity_exhausted"
	case state.EstimatedFPP > maximumHealthyTermBloomFPP:
		report.RebuildReason = "fpp_exceeded"
	case staleDeletePressureExceeded(state):
		report.RebuildReason = "stale_delete_pressure"
	default:
		report.GatingEligible = true
	}
	report.RebuildRequired = !report.GatingEligible
	return report
}

func staleDeletePressureExceeded(state *sqlite.TermIndexState) bool {
	threshold := int64(100)
	if proportional := state.PlannedCapacity / 5; proportional > threshold {
		threshold = proportional
	}
	return state.StaleDeleteCount > threshold
}
