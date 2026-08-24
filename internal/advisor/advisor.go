package advisor

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	// Recommendation thresholds — lenient triggers for surfacing issues early.
	// These are intentionally lower than evidence floors so that users receive
	// actionable guidance before they have enough data for a reliable grade.
	// Provisional; will be calibrated against benchmark-validity.
	minimumFeedbackSamples = 3
	minimumRecallRecords   = 3

	// Evidence floors per dimension. A dimension is marked insufficient
	// and contributes 0 to the composite score when its evidence count
	// falls below this floor.
	// Provisional; will be calibrated against benchmark-validity.
	qualityEvidenceFloor    = 10  // scored feedback samples required for reliable quality measurement
	trustEvidenceFloor      = 5   // active memories required for trust assessment
	coverageEvidenceFloor   = 10  // retrieved (reached) memories required for coverage measurement
	hygieneEvidenceFloor    = 100 // total memory count required for hygiene assessment
	efficiencyEvidenceFloor = 10  // recall sessions required for context efficiency measurement

	// Anti-gaming: retention baseline for hygiene scoring.
	// When active memory count falls below this baseline, the hygiene score
	// is scaled down to prevent rewarding deletion of healthy memories.
	// Provisional; will be calibrated against benchmark-validity.
	hygieneRetentionBaseline = 50

	// Score / warning thresholds.
	// Provisional; will be calibrated against benchmark-validity.
	lowFeedbackAverage    = 3.5
	lowUsefulRatio        = 0.5
	lowRecallSavings      = 20.0
	highDecayScore        = 0.75
	staleShareWarning     = 0.25
	lowCoverageShare      = 0.30
	coverageMinimumMemory = 20
	lowConfidence         = 0.50
	trustShareWarning     = 0.10
)

// Dimension weights sum to 1.0. Each weight reflects the relative importance
// of that dimension in the composite health score.
// Provisional; will be calibrated against benchmark-validity.
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
	totalMemories    int
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
	score, allInsufficient := compositeScore(dimensions)
	report := Report{
		Workspace:       snapshot.Workspace,
		Score:           score,
		Grade:           gradeFor(score, allInsufficient),
		Neutral:         allInsufficient,
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
	metrics.totalMemories = len(snapshot.Memories)
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
		dimension := Dimension{
			Key:    definition.key,
			Label:  definition.label,
			Weight: definition.weight,
		}
		switch definition.key {
		case DimensionQuality:
			dimension.EvidenceCount = metrics.scoredRequests
			dimension.Sufficient = metrics.scoredRequests >= qualityEvidenceFloor
			dimension.Available = metrics.scoredRequests > 0
			if dimension.Available {
				score := metrics.averageScore / 5 * 100
				if metrics.usefulSamples > 0 {
					score = score*0.60 + metrics.usefulRatio*100*0.40
				}
				// Anti-gaming: down-weight self-ratings by sample size factor.
				// log(N)/log(floor) ensures few samples contribute proportionally less.
				// At the floor (10), factor = 1.0; at 3 samples, factor ≈ 0.48.
				// Provisional; will be calibrated against benchmark-validity.
				if metrics.scoredRequests < qualityEvidenceFloor {
					sampleFactor := math.Log(float64(metrics.scoredRequests)) / math.Log(float64(qualityEvidenceFloor))
					score *= sampleFactor
				}
				dimension.Score = clampScore(score)
			}
			if dimension.Sufficient {
				dimension.Reason = "sufficient evidence"
				dimension.Detail = fmt.Sprintf("%d scored requests; %.2f/5 average; meets floor of %d",
					metrics.scoredRequests, metrics.averageScore, qualityEvidenceFloor)
			} else if dimension.Available {
				dimension.Reason = fmt.Sprintf("insufficient_evidence: need %d scored requests, have %d",
					qualityEvidenceFloor, metrics.scoredRequests)
				dimension.Detail = fmt.Sprintf("%d scored requests; %.2f/5 average; below floor of %d",
					metrics.scoredRequests, metrics.averageScore, qualityEvidenceFloor)
			} else {
				dimension.Reason = fmt.Sprintf("insufficient_evidence: need %d scored requests, have 0",
					qualityEvidenceFloor)
				dimension.Detail = fmt.Sprintf("0 scored requests; need %d for quality measurement",
					qualityEvidenceFloor)
			}

		case DimensionEfficiency:
			dimension.EvidenceCount = metrics.recallRecords
			dimension.Sufficient = metrics.recallRecords >= efficiencyEvidenceFloor && metrics.recallBaseline > 0
			dimension.Available = metrics.recallRecords > 0 && metrics.recallBaseline > 0
			if dimension.Available {
				dimension.Score = clampScore(metrics.recallSavings)
			}
			if dimension.Sufficient {
				dimension.Reason = "sufficient evidence"
				dimension.Detail = fmt.Sprintf("%.1f%% deterministic recall context savings across %d records; meets floor of %d",
					metrics.recallSavings, metrics.recallRecords, efficiencyEvidenceFloor)
			} else if dimension.Available {
				dimension.Reason = fmt.Sprintf("insufficient_evidence: need %d recall records, have %d",
					efficiencyEvidenceFloor, metrics.recallRecords)
				dimension.Detail = fmt.Sprintf("%.1f%% savings across %d records; below floor of %d",
					metrics.recallSavings, metrics.recallRecords, efficiencyEvidenceFloor)
			} else {
				dimension.Reason = fmt.Sprintf("insufficient_evidence: need %d recall records, have 0",
					efficiencyEvidenceFloor)
				dimension.Detail = fmt.Sprintf("0 recall records; need %d for efficiency measurement",
					efficiencyEvidenceFloor)
			}

		case DimensionHygiene:
			dimension.EvidenceCount = metrics.totalMemories
			dimension.Sufficient = metrics.totalMemories >= hygieneEvidenceFloor
			dimension.Available = metrics.activeMemories > 0
			if dimension.Available {
				negativeShare := float64(metrics.negativeMemories) / float64(metrics.activeMemories)
				staleShare := float64(metrics.staleMemories) / float64(metrics.activeMemories)
				rawScore := 100 - negativeShare*60 - staleShare*40
				// Anti-gaming: cap hygiene when memory count is below the retention
				// baseline. Deleting healthy memories to achieve a clean slate must
				// not improve the hygiene score.
				// Provisional; will be calibrated against benchmark-validity.
				if metrics.activeMemories < hygieneRetentionBaseline {
					capFactor := float64(metrics.activeMemories) / float64(hygieneRetentionBaseline)
					rawScore *= capFactor
				}
				dimension.Score = clampScore(rawScore)
			}
			if dimension.Sufficient {
				dimension.Reason = "sufficient evidence"
				dimension.Detail = fmt.Sprintf("%d negative-feedback and %d high-decay active memories; %d total memories meets floor of %d",
					metrics.negativeMemories, metrics.staleMemories, metrics.totalMemories, hygieneEvidenceFloor)
			} else if dimension.Available {
				dimension.Reason = fmt.Sprintf("insufficient_evidence: need %d total memories, have %d",
					hygieneEvidenceFloor, metrics.totalMemories)
				dimension.Detail = fmt.Sprintf("%d negative-feedback and %d high-decay active memories; %d total memories below floor of %d",
					metrics.negativeMemories, metrics.staleMemories, metrics.totalMemories, hygieneEvidenceFloor)
			} else {
				dimension.Reason = fmt.Sprintf("insufficient_evidence: need %d total memories, have 0",
					hygieneEvidenceFloor)
				dimension.Detail = "No active memories"
			}

		case DimensionCoverage:
			dimension.EvidenceCount = metrics.reachedMemories
			dimension.Sufficient = metrics.reachedMemories >= coverageEvidenceFloor
			dimension.Available = metrics.activeMemories > 0
			if dimension.Available {
				coverage := float64(metrics.reachedMemories) / float64(metrics.activeMemories) * 100
				dimension.Score = clampScore(coverage)
			}
			if dimension.Sufficient {
				dimension.Reason = "sufficient evidence"
				dimension.Detail = fmt.Sprintf("%d of %d active memories retrieved; %d retrieved meets floor of %d",
					metrics.reachedMemories, metrics.activeMemories, metrics.reachedMemories, coverageEvidenceFloor)
			} else if dimension.Available {
				dimension.Reason = fmt.Sprintf("insufficient_evidence: need %d retrieved memories, have %d",
					coverageEvidenceFloor, metrics.reachedMemories)
				dimension.Detail = fmt.Sprintf("%d of %d active memories retrieved; %d retrieved below floor of %d",
					metrics.reachedMemories, metrics.activeMemories, metrics.reachedMemories, coverageEvidenceFloor)
			} else {
				dimension.Reason = fmt.Sprintf("insufficient_evidence: need %d retrieved memories, have 0",
					coverageEvidenceFloor)
				dimension.Detail = "No active memories"
			}

		case DimensionTrust:
			dimension.EvidenceCount = metrics.activeMemories
			dimension.Sufficient = metrics.activeMemories >= trustEvidenceFloor
			dimension.Available = metrics.activeMemories > 0
			if dimension.Available {
				lowConfidenceShare := float64(metrics.lowConfidence) / float64(metrics.activeMemories)
				missingSourceShare := float64(metrics.missingSource) / float64(metrics.activeMemories)
				dimension.Score = clampScore(100 - lowConfidenceShare*60 - missingSourceShare*40)
			}
			if dimension.Sufficient {
				dimension.Reason = "sufficient evidence"
				dimension.Detail = fmt.Sprintf("%d low-confidence and %d missing-provenance active memories; %d active meets floor of %d",
					metrics.lowConfidence, metrics.missingSource, metrics.activeMemories, trustEvidenceFloor)
			} else if dimension.Available {
				dimension.Reason = fmt.Sprintf("insufficient_evidence: need %d active memories, have %d",
					trustEvidenceFloor, metrics.activeMemories)
				dimension.Detail = fmt.Sprintf("%d low-confidence and %d missing-provenance active memories; %d active below floor of %d",
					metrics.lowConfidence, metrics.missingSource, metrics.activeMemories, trustEvidenceFloor)
			} else {
				dimension.Reason = fmt.Sprintf("insufficient_evidence: need %d active memories, have 0",
					trustEvidenceFloor)
				dimension.Detail = "No active memories"
			}
		}
		dimensions = append(dimensions, dimension)
	}
	return dimensions
}

// compositeScore computes the overall workspace health score.
// Only dimensions that meet their evidence floor (Sufficient=true) contribute.
// The weighted average of sufficient dimensions is multiplied by an evidence
// completeness factor (sufficient_count / total_dimensions) so that a
// workspace with only a few measured dimensions cannot reach the top grade.
// Returns (score, allInsufficient) where allInsufficient is true when no
// dimension meets its evidence floor.
// Provisional calibration; will be tuned against benchmark-validity.
func compositeScore(dimensions []Dimension) (int, bool) {
	var weighted float64
	var weights float64
	sufficientCount := 0
	totalDimensions := len(dimensions)
	for _, dimension := range dimensions {
		if !dimension.Sufficient {
			continue
		}
		weighted += float64(dimension.Score) * dimension.Weight
		weights += dimension.Weight
		sufficientCount++
	}
	if sufficientCount == 0 {
		return 0, true
	}
	// Weighted average over sufficient dimensions, scaled by completeness.
	completenessFactor := float64(sufficientCount) / float64(totalDimensions)
	return clampScore((weighted / weights) * completenessFactor), false
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

// gradeFor maps a composite score to a letter grade.
// When allInsufficient is true, returns "U" (insufficient data) to
// communicate that no dimension meets its evidence floor.
// Grade thresholds are provisional; will be calibrated against benchmark-validity.
func gradeFor(score int, allInsufficient bool) string {
	if allInsufficient {
		return "U"
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
