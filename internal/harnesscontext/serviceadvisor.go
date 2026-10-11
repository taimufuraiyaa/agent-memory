package harnesscontext

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
)

// ServiceAdvisor routes visibility advice through the decision service, which bounds,
// validates, journals and pauses it. The assembler offers only the chunks the advisor's data
// class allows; the service replaces their identifiers with positional aliases before
// anything leaves the process and describes each by source, size and relevance only. The
// assembler still validates the answer against what it offered and can only promote.
type ServiceAdvisor struct{ Service *harnessdecide.Service }

var _ Advisor = ServiceAdvisor{}

func (a ServiceAdvisor) Promote(ctx context.Context, candidates []Candidate) ([]string, float64, error) {
	if a.Service == nil {
		return nil, 0, errors.New("no decision service")
	}
	items := make([]harnessdecide.Item, len(candidates))
	for i, c := range candidates {
		items[i] = harnessdecide.Item{ID: c.ID, Class: string(c.Source), Facts: harnessdecide.Facts{"t": max(0, c.Tokens), "r": percent(c.Relevance)}}
	}
	ids, out := a.Service.Visibility(ctx, items)
	if out.Status != harnessdecide.StatusApplied {
		return nil, 0, fmt.Errorf("decision %s", out.Status)
	}
	return ids, out.Confidence, nil
}

// percent is a relevance in [0, 1] as a whole number of percent; anything else is zero.
func percent(relevance float64) int {
	if math.IsNaN(relevance) || relevance < 0 {
		return 0
	}
	return int(min(relevance, 1) * 100)
}

// CacheAdvisor advises how to order the evidence of a run's next prompt.
type CacheAdvisor interface {
	Order(ctx context.Context, runID string) (Order, error)
}

// CacheStats is what the model meter has measured about a provider's prompt cache for a run.
// Without enough measured turns there is nothing to decide, and nothing is asked.
type CacheStats struct {
	Measured                        bool
	HitPercent, Turns, PrefixTokens int
}

// ServiceCacheAdvisor routes the ordering decision through the decision service, from
// measured cache numbers only. Both orders select the same evidence; they differ in cost.
type ServiceCacheAdvisor struct {
	Service *harnessdecide.Service
	Stats   func(runID string) CacheStats
}

var _ CacheAdvisor = ServiceCacheAdvisor{}

func (a ServiceCacheAdvisor) Order(ctx context.Context, runID string) (Order, error) {
	if a.Service == nil || a.Stats == nil {
		return OrderRelevance, errors.New("no decision service")
	}
	stats := a.Stats(runID)
	if !stats.Measured {
		return OrderRelevance, errors.New("the cache has not been measured")
	}
	strategy, out := a.Service.Cache(ctx, harnessdecide.Facts{"h": min(max(stats.HitPercent, 0), 100), "n": max(stats.Turns, 0), "p": max(stats.PrefixTokens, 0)})
	if out.Status != harnessdecide.StatusApplied {
		return OrderRelevance, fmt.Errorf("decision %s", out.Status)
	}
	if strategy == harnessdecide.CacheStable {
		return OrderStable, nil
	}
	return OrderRelevance, nil
}
