package application

import (
	"context"
	"errors"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/locator"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

const (
	defaultTermRebuildPageSize = 200
	minimumTermBloomCapacity   = 1024
)

type RebuildTermIndexOptions struct {
	Workspace string
	TargetFPP float64
	PageSize  int
}

type RebuildTermIndexReport struct {
	Workspace     string  `json:"workspace"`
	Scanned       int     `json:"scanned"`
	Indexed       int     `json:"indexed"`
	Skipped       int     `json:"skipped"`
	Failed        int     `json:"failed"`
	DistinctTerms int64   `json:"distinct_terms"`
	BitCount      int64   `json:"bit_count"`
	HashCount     int     `json:"hash_count"`
	EstimatedFPP  float64 `json:"estimated_fpp"`
	Generation    int64   `json:"generation"`
}

// RebuildTermIndex backfills missing canonical terms and atomically publishes a Bloom generation.
func (s *MemoryService) RebuildTermIndex(ctx context.Context, options RebuildTermIndexOptions) (*RebuildTermIndexReport, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("term index store is not available")
	}
	if options.Workspace == "" {
		return nil, errors.New("workspace is required")
	}
	if options.TargetFPP == 0 {
		options.TargetFPP = 0.01
	}
	if options.PageSize <= 0 {
		options.PageSize = defaultTermRebuildPageSize
	}
	report := &RebuildTermIndexReport{Workspace: options.Workspace}

	afterID := ""
	for {
		page, err := s.store.ListMemoriesForTermBackfill(ctx, options.Workspace, afterID, options.PageSize)
		if err != nil {
			return report, err
		}
		if len(page) == 0 {
			break
		}
		for _, memory := range page {
			report.Scanned++
			existing, err := s.store.ListMemoryTerms(ctx, options.Workspace, memory.ID)
			if err != nil {
				return report, err
			}
			if len(existing) > 0 {
				report.Skipped++
				continue
			}
			terms, err := locator.Extract(locator.Input{Content: memory.Content, Entities: memory.Entities, Tags: memory.Tags})
			if err != nil {
				report.Failed++
				continue
			}
			if len(terms) == 0 {
				report.Skipped++
				continue
			}
			if err := s.store.ReplaceMemoryTerms(ctx, options.Workspace, memory.ID, terms); err != nil {
				return report, err
			}
			report.Indexed++
		}
		afterID = page[len(page)-1].ID
	}

	state, err := s.store.GetTermIndexState(ctx, options.Workspace)
	if err != nil {
		return report, err
	}
	corpusGeneration := int64(0)
	if state != nil {
		corpusGeneration = state.CorpusGeneration
	}
	now := time.Now().UTC()
	building := sqlite.TermIndexState{
		Workspace:            options.Workspace,
		State:                sqlite.TermIndexBuilding,
		FormatVersion:        engine.TermBloomFormatVersion,
		NormalizationVersion: locator.NormalizationVersion,
		ExtractorVersion:     locator.ExtractorVersion,
		HashVersion:          engine.TermBloomHashVersion,
		CorpusGeneration:     corpusGeneration,
		FilterGeneration:     0,
		RebuildReason:        "requested",
		UpdatedAt:            now,
	}
	if err := s.store.UpsertTermIndexState(ctx, building); err != nil {
		return report, err
	}

	distinct, err := s.store.CountDistinctMemoryTerms(ctx, options.Workspace, locator.NormalizationVersion)
	if err != nil {
		return report, err
	}
	capacity := plannedTermBloomCapacity(distinct)
	filter, err := engine.NewTermBloom(capacity, options.TargetFPP)
	if err != nil {
		return report, err
	}
	afterTerm := ""
	for {
		page, err := s.store.ListDistinctMemoryTerms(ctx, options.Workspace, locator.NormalizationVersion, afterTerm, options.PageSize)
		if err != nil {
			return report, err
		}
		if len(page) == 0 {
			break
		}
		for _, term := range page {
			filter.Add(term)
		}
		afterTerm = page[len(page)-1]
	}

	latest, err := s.store.GetTermIndexState(ctx, options.Workspace)
	if err != nil {
		return report, err
	}
	if latest == nil || latest.CorpusGeneration != corpusGeneration {
		return report, errors.New("term corpus changed during rebuild; retry required")
	}
	bitmap := filter.Bitmap()
	ready := building
	ready.Bitmap = bitmap
	ready.State = sqlite.TermIndexReady
	ready.BitCount = filter.BitCount()
	ready.HashCount = filter.HashCount()
	ready.DistinctItemCount = distinct
	ready.PlannedCapacity = capacity
	ready.EstimatedFPP = filter.EstimatedFalsePositiveProbability(distinct)
	ready.StaleDeleteCount = 0
	ready.FilterGeneration = corpusGeneration
	ready.Checksum = engine.TermBloomChecksum(bitmap)
	ready.RebuildReason = ""
	ready.BuiltAt = now
	ready.UpdatedAt = time.Now().UTC()
	if err := s.store.UpsertTermIndexState(ctx, ready); err != nil {
		return report, err
	}
	metrics := observability.GetRegistry()
	metrics.TermBloomSize.WithLabelValues(options.Workspace).Set(float64(len(bitmap)))
	metrics.TermBloomFPP.WithLabelValues(options.Workspace).Set(ready.EstimatedFPP)
	report.DistinctTerms = distinct
	report.BitCount = ready.BitCount
	report.HashCount = ready.HashCount
	report.EstimatedFPP = ready.EstimatedFPP
	report.Generation = corpusGeneration
	return report, nil
}

func plannedTermBloomCapacity(distinct int64) int64 {
	if distinct <= minimumTermBloomCapacity/2 {
		return minimumTermBloomCapacity
	}
	capacity := distinct * 2
	if capacity < distinct { // int64 overflow guard for pathological metadata.
		return distinct
	}
	return capacity
}
