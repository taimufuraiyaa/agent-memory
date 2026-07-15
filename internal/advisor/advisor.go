package advisor

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	minimumFeedbackSamples = 3
	minimumRecallRecords   = 3
	lowFeedbackAverage     = 3.5
	lowUsefulRatio         = 0.5
	lowRecallSavings       = 20.0
	highDecayScore         = 0.75
	staleShareWarning      = 0.25
	lowCoverageShare       = 0.30
	coverageMinimumMemory  = 20
	lowConfidence          = 0.50
	trustShareWarning      = 0.10
)

var dimensionDefinitions = []struct {
	key    DimensionKey
	label  string
	weight float64
}{
	{DimensionQuality, "Retrieval quality", 0.30},
	{DimensionEfficiency, "Context efficiency", 0.25},
	{DimensionHygiene, "Memory hygiene", 0.20},
	{DimensionCoverage, "Retrieval coverage", 0.15},
	{DimensionTrust, "Trust and provenance", 0.10},
}

type analysisMetrics struct {
	activeMemories   int
	reachedMemories  int
	staleMemories    int
	rejectedMemories int
	harmfulMemories  int
	negativeMemories int
	lowConfidence    int
	missingSource    int
	scoredRequests   int
	averageScore     float64
	usefulSamples    int
	usefulRatio      float64
	recallRecords    int
	recallBaseline   int
	recallSaved      int
	recallSavings    float64
}

func Analyze(snapshot Snapshot) Report {
	metrics := collectMetrics(snapshot)
	dimensions := scoreDimensions(metrics)
	score, neutral := compositeScore(dimensions)
	report := Report{
		Workspace:       snapshot.Workspace,
		Score:           score,
		Grade:           gradeFor(score, neutral),
		Neutral:         neutral,
		Dimensions:      dimensions,
		Recommendations: recommendations(metrics),
		Evidence: Evidence{
			MemoryCount:            len(snapshot.Memories),
			ActiveMemoryCount:      metrics.activeMemories,
			ScoredRequestCount:     metrics.scoredRequests,
			UsefulRatioSampleCount: metrics.usefulSamples,
			RecallMetricRecords:    metrics.recallRecords,
		},
	}
	return report
}

func collectMetrics(snapshot Snapshot) analysisMetrics {
	var metrics analysisMetrics
	var scoreTotal float64
	var usefulTotal float64
	for _, memory := range snapshot.Memories {
		if memory.SupersededBy != nil {
			continue
		}
		metrics.activeMemories++
		if memory.AccessCount > 0 {
			metrics.reachedMemories++
		}
		if memory.DecayScore >= highDecayScore && !memory.Pinned {
			metrics.staleMemories++
		}
		if memory.RejectedCount > 0 {
			metrics.rejectedMemories++
		}
		if memory.HarmfulCount > 0 {
			metrics.harmfulMemories++
		}
		if memory.RejectedCount > 0 || memory.HarmfulCount > 0 {
			metrics.negativeMemories++
		}
		if memory.Confidence < lowConfidence {
			metrics.lowConfidence++
		}
		if strings.TrimSpace(string(memory.Source.Type)) == "" {
			metrics.missingSource++
		}
	}
	for _, request := range snapshot.Requests {
		if request.Score < 0 || request.Score > 5 {
			continue
		}
		metrics.scoredRequests++
		scoreTotal += float64(request.Score)
		if request.TotalCount > 0 && request.UsefulCount >= 0 {
			metrics.usefulSamples++
			usefulTotal += clampFloat(float64(request.UsefulCount)/float64(request.TotalCount), 0, 1)
		}
	}
	if metrics.scoredRequests > 0 {
		metrics.averageScore = scoreTotal / float64(metrics.scoredRequests)
	}
	if metrics.usefulSamples > 0 {
		metrics.usefulRatio = usefulTotal / float64(metrics.usefulSamples)
	}
	for _, item := range snapshot.TokenMetricsByOperation {
		if !strings.EqualFold(strings.TrimSpace(item.Operation), "recall") {
			continue
		}
		metrics.recallRecords += item.Records
		metrics.recallBaseline += item.BaselineTokens
		metrics.recallSaved += item.SavedTokens
	}
	if metrics.recallBaseline > 0 {
		metrics.recallSavings = clampFloat(float64(metrics.recallSaved)/float64(metrics.recallBaseline)*100, 0, 100)
	}
	return metrics
}

func scoreDimensions(metrics analysisMetrics) []Dimension {
	dimensions := make([]Dimension, 0, len(dimensionDefinitions))
	for _, definition := range dimensionDefinitions {
		dimension := Dimension{Key: definition.key, Label: definition.label, Weight: definition.weight}
		switch definition.key {
		case DimensionQuality:
			dimension.Available = metrics.scoredRequests >= minimumFeedbackSamples
			if dimension.Available {
				score := metrics.averageScore / 5 * 100
				if metrics.usefulSamples > 0 {
					score = score*0.60 + metrics.usefulRatio*100*0.40
				}
				dimension.Score = clampScore(score)
				dimension.Detail = fmt.Sprintf("%d scored requests; %.2f/5 average", metrics.scoredRequests, metrics.averageScore)
			} else {
				dimension.Detail = fmt.Sprintf("%d of %d scored requests required", metrics.scoredRequests, minimumFeedbackSamples)
			}
		case DimensionEfficiency:
			dimension.Available = metrics.recallRecords >= minimumRecallRecords && metrics.recallBaseline > 0
			if dimension.Available {
				dimension.Score = clampScore(metrics.recallSavings)
				dimension.Detail = fmt.Sprintf("%.1f%% deterministic recall context savings across %d records", metrics.recallSavings, metrics.recallRecords)
			} else {
				dimension.Detail = fmt.Sprintf("%d of %d recall metric records required", metrics.recallRecords, minimumRecallRecords)
			}
		case DimensionHygiene:
			dimension.Available = metrics.activeMemories > 0
			if dimension.Available {
				negativeShare := float64(metrics.negativeMemories) / float64(metrics.activeMemories)
				staleShare := float64(metrics.staleMemories) / float64(metrics.activeMemories)
				dimension.Score = clampScore(100 - negativeShare*60 - staleShare*40)
				dimension.Detail = fmt.Sprintf("%d negative-feedback and %d high-decay active memories", metrics.negativeMemories, metrics.staleMemories)
			} else {
				dimension.Detail = "No active memories"
			}
		case DimensionCoverage:
			dimension.Available = metrics.activeMemories > 0
			if dimension.Available {
				coverage := float64(metrics.reachedMemories) / float64(metrics.activeMemories) * 100
				dimension.Score = clampScore(coverage)
				dimension.Detail = fmt.Sprintf("%d of %d active memories retrieved", metrics.reachedMemories, metrics.activeMemories)
			} else {
				dimension.Detail = "No active memories"
			}
		case DimensionTrust:
			dimension.Available = metrics.activeMemories > 0
			if dimension.Available {
				lowConfidenceShare := float64(metrics.lowConfidence) / float64(metrics.activeMemories)
				missingSourceShare := float64(metrics.missingSource) / float64(metrics.activeMemories)
				dimension.Score = clampScore(100 - lowConfidenceShare*60 - missingSourceShare*40)
				dimension.Detail = fmt.Sprintf("%d low-confidence and %d missing-provenance active memories", metrics.lowConfidence, metrics.missingSource)
			} else {
				dimension.Detail = "No active memories"
			}
		}
		dimensions = append(dimensions, dimension)
	}
	return dimensions
}

func compositeScore(dimensions []Dimension) (int, bool) {
	var weighted float64
	var weights float64
	for _, dimension := range dimensions {
		if !dimension.Available {
			continue
		}
		weighted += float64(dimension.Score) * dimension.Weight
		weights += dimension.Weight
	}
	if weights == 0 {
		return 0, true
	}
	return clampScore(weighted / weights), false
}

func recommendations(metrics analysisMetrics) []Recommendation {
	items := make([]Recommendation, 0, 10)
	if metrics.scoredRequests < minimumFeedbackSamples {
		items = append(items, Recommendation{
			ID: "feedback-insufficient", Severity: SeverityInfo, Category: string(DimensionQuality),
			Title:  "Collect more retrieval feedback",
			Detail: "Score search and recall requests so retrieval quality can be measured without guessing.",
			Metric: fmt.Sprintf("%d/%d scored", metrics.scoredRequests, minimumFeedbackSamples),
		})
	} else {
		if metrics.averageScore < lowFeedbackAverage {
			items = append(items, Recommendation{
				ID: "low-feedback-quality", Severity: SeverityWarn, Category: string(DimensionQuality),
				Title:  "Retrieval feedback is below target",
				Detail: "Review low-scoring requests and their reasons before widening retrieval or increasing context budgets.",
				Metric: fmt.Sprintf("%.2f/5 average", metrics.averageScore),
			})
		}
		if metrics.usefulSamples > 0 && metrics.usefulRatio < lowUsefulRatio {
			items = append(items, Recommendation{
				ID: "low-useful-ratio", Severity: SeverityWarn, Category: string(DimensionQuality),
				Title:  "Too few returned memories are useful",
				Detail: "Tighten retrieval scope or reduce top-k before adding more context to each task.",
				Metric: fmt.Sprintf("%.1f%% useful", metrics.usefulRatio*100),
			})
		}
	}
	if metrics.recallRecords < minimumRecallRecords || metrics.recallBaseline <= 0 {
		items = append(items, Recommendation{
			ID: "recall-metrics-insufficient", Severity: SeverityInfo, Category: string(DimensionEfficiency),
			Title:  "Collect more recall telemetry",
			Detail: "Run normal recall workflows before judging context efficiency; search metrics are intentionally excluded.",
			Metric: fmt.Sprintf("%d/%d recall records", metrics.recallRecords, minimumRecallRecords),
		})
	} else if metrics.recallSavings < lowRecallSavings {
		items = append(items, Recommendation{
			ID: "low-recall-savings", Severity: SeverityWarn, Category: string(DimensionEfficiency),
			Title:  "Recall is saving little context",
			Detail: "Review recall budgets and returned-memory relevance. This is a deterministic context proxy, not billed cost.",
			Metric: fmt.Sprintf("%.1f%% context saved", metrics.recallSavings),
		})
	}
	if metrics.harmfulMemories > 0 {
		items = append(items, Recommendation{
			ID: "memory-harmful-feedback", Severity: SeverityCritical, Category: string(DimensionHygiene),
			Title:  "Harmful memories remain active",
			Detail: "Review harmful-feedback memories and supersede or correct them through the existing audited feedback workflow.",
			Metric: fmt.Sprintf("%d active", metrics.harmfulMemories),
		})
	}
	if metrics.rejectedMemories > 0 {
		items = append(items, Recommendation{
			ID: "memory-rejected-feedback", Severity: SeverityWarn, Category: string(DimensionHygiene),
			Title:  "Rejected memories need review",
			Detail: "Repeatedly rejected active memories should be clarified, corrected, or superseded after inspection.",
			Metric: fmt.Sprintf("%d active", metrics.rejectedMemories),
		})
	}
	if metrics.activeMemories > 0 {
		staleShare := float64(metrics.staleMemories) / float64(metrics.activeMemories)
		if staleShare >= staleShareWarning {
			items = append(items, Recommendation{
				ID: "stale-memory-share", Severity: SeverityWarn, Category: string(DimensionHygiene),
				Title:  "High-decay memories need lifecycle review",
				Detail: "Run lifecycle maintenance or inspect stale active memories before they dilute retrieval quality.",
				Metric: fmt.Sprintf("%.1f%% high decay", staleShare*100),
			})
		}
		coverage := float64(metrics.reachedMemories) / float64(metrics.activeMemories)
		if metrics.activeMemories >= coverageMinimumMemory && coverage < lowCoverageShare {
			items = append(items, Recommendation{
				ID: "low-retrieval-coverage", Severity: SeverityInfo, Category: string(DimensionCoverage),
				Title:  "Most memories have never surfaced",
				Detail: "Inspect scope and taxonomy before adding more memories; low coverage can indicate dead zones or overly broad capture.",
				Metric: fmt.Sprintf("%.1f%% reached", coverage*100),
			})
		}
		lowConfidenceShare := float64(metrics.lowConfidence) / float64(metrics.activeMemories)
		if lowConfidenceShare >= trustShareWarning {
			items = append(items, Recommendation{
				ID: "low-confidence-memories", Severity: SeverityInfo, Category: string(DimensionTrust),
				Title:  "Low-confidence memories need validation",
				Detail: "Validate or enrich low-confidence active memories before relying on them for continuation work.",
				Metric: fmt.Sprintf("%.1f%% low confidence", lowConfidenceShare*100),
			})
		}
		missingSourceShare := float64(metrics.missingSource) / float64(metrics.activeMemories)
		if missingSourceShare >= trustShareWarning {
			items = append(items, Recommendation{
				ID: "missing-provenance", Severity: SeverityInfo, Category: string(DimensionTrust),
				Title:  "Some memories lack provenance",
				Detail: "Prefer memories with a recorded source so clients can audit where durable knowledge came from.",
				Metric: fmt.Sprintf("%.1f%% missing source", missingSourceShare*100),
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		left := severityRank(items[i].Severity)
		right := severityRank(items[j].Severity)
		if left != right {
			return left < right
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityCritical:
		return 0
	case SeverityWarn:
		return 1
	default:
		return 2
	}
}

func gradeFor(score int, neutral bool) string {
	if neutral {
		return "N/A"
	}
	switch {
	case score >= 90:
		return "A"
	case score >= 80:
		return "B"
	case score >= 70:
		return "C"
	case score >= 60:
		return "D"
	default:
		return "F"
	}
}

func clampScore(value float64) int {
	return int(math.Round(clampFloat(value, 0, 100)))
}

func clampFloat(value, minimum, maximum float64) float64 {
	return math.Max(minimum, math.Min(maximum, value))
}
