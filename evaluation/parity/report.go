// Package parity evaluates user-visible retrieval invariants across local and hosted backends.
package parity

import (
	"errors"
	"math"
	"sort"
)

type Observation struct {
	Backend            string             `json:"backend"`
	Order              []string           `json:"order"`
	NormalizedScores   map[string]float64 `json:"normalized_scores"`
	ExactTop           string             `json:"exact_top"`
	FeedbackPreferred  bool               `json:"feedback_preferred"`
	DecayDemoted       bool               `json:"decay_demoted"`
	Suppressed         []string           `json:"suppressed"`
	ResolvedCitations  map[string]string  `json:"resolved_citations"`
	UnresolvedCitation int                `json:"unresolved_citation_count"`
}

type Thresholds struct {
	MinimumTopKOverlap float64 `json:"minimum_top_k_overlap"`
	MaximumScoreDelta  float64 `json:"maximum_normalized_score_delta"`
}

type Difference struct {
	Metric      string `json:"metric"`
	Explanation string `json:"explanation"`
}

type Report struct {
	Version       string       `json:"version"`
	Dataset       string       `json:"dataset"`
	Passed        bool         `json:"passed"`
	TopKOverlap   float64      `json:"top_k_overlap"`
	MaxScoreDelta float64      `json:"maximum_normalized_score_delta"`
	Differences   []Difference `json:"differences"`
	Explanations  []Difference `json:"explanations"`
}

func Compare(version, dataset string, local, hosted Observation, thresholds Thresholds) (Report, error) {
	if version == "" || dataset == "" || local.Backend == "" || hosted.Backend == "" || thresholds.MinimumTopKOverlap <= 0 || thresholds.MaximumScoreDelta < 0 {
		return Report{}, errors.New("retrieval parity input is incomplete")
	}
	report := Report{Version: version, Dataset: dataset, Passed: true, Differences: []Difference{}, Explanations: []Difference{}}
	report.TopKOverlap = overlap(local.Order, hosted.Order)
	report.MaxScoreDelta = maxScoreDelta(local.NormalizedScores, hosted.NormalizedScores)
	if report.MaxScoreDelta > 0 {
		report.Explanations = append(report.Explanations, Difference{Metric: "score", Explanation: "Local memory retrieval and hosted hybrid passage retrieval use different base feature weights; normalized ranking delta is bounded by the approved threshold while adaptive feedback, decay, and suppression tuning is shared."})
	}
	check := func(ok bool, metric, explanation string) {
		if ok {
			return
		}
		report.Passed = false
		report.Differences = append(report.Differences, Difference{Metric: metric, Explanation: explanation})
	}
	check(report.TopKOverlap >= thresholds.MinimumTopKOverlap, "ordering", "Top-k overlap is below the approved migration threshold.")
	check(report.MaxScoreDelta <= thresholds.MaximumScoreDelta, "score", "Normalized score delta exceeds the approved hybrid-versus-local tolerance.")
	check(local.ExactTop != "" && local.ExactTop == hosted.ExactTop, "exact_term", "Exact-term winner differs between backends.")
	check(local.FeedbackPreferred && hosted.FeedbackPreferred, "feedback", "Helpful feedback does not improve the expected candidate in both backends.")
	check(local.DecayDemoted && hosted.DecayDemoted, "decay", "Decay does not demote the expected candidate in both backends.")
	check(equalStrings(local.Suppressed, hosted.Suppressed), "suppression", "Suppressed candidate sets differ.")
	check(local.UnresolvedCitation == 0 && hosted.UnresolvedCitation == 0 && equalStringMap(local.ResolvedCitations, hosted.ResolvedCitations), "citation", "Citation identity or resolution differs.")
	return report, nil
}

func overlap(left, right []string) float64 {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	if limit == 0 {
		return 0
	}
	seen := make(map[string]struct{}, limit)
	for _, id := range left[:limit] {
		seen[id] = struct{}{}
	}
	matches := 0
	for _, id := range right[:limit] {
		if _, ok := seen[id]; ok {
			matches++
		}
	}
	return float64(matches) / float64(limit)
}

func maxScoreDelta(left, right map[string]float64) float64 {
	maximum := 0.0
	for id, leftScore := range left {
		if rightScore, ok := right[id]; ok {
			maximum = math.Max(maximum, math.Abs(leftScore-rightScore))
		}
	}
	return maximum
}

func equalStrings(left, right []string) bool {
	left = append([]string{}, left...)
	right = append([]string{}, right...)
	sort.Strings(left)
	sort.Strings(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
