package libraryevaluation

import (
	"fmt"
	"sort"
)

type MetricDirection string

const (
	HigherIsBetter MetricDirection = "higher"
	LowerIsBetter  MetricDirection = "lower"
)

type MetricDefinition struct {
	Name      string          `json:"name"`
	Version   string          `json:"version"`
	Threshold float64         `json:"threshold"`
	Direction MetricDirection `json:"direction"`
}

var LibraryMetricDefinitions = []MetricDefinition{{"citation_precision", "v1", .95, HigherIsBetter}, {"quote_exact_match", "v1", 1, HigherIsBetter}, {"entailment_accuracy", "v1", .9, HigherIsBetter}, {"attribution_accuracy", "v1", .98, HigherIsBetter}, {"unanswerable_accuracy", "v1", .95, HigherIsBetter}, {"p95_latency_ms", "v1", 5000, LowerIsBetter}, {"average_tokens", "v1", 8000, LowerIsBetter}, {"average_role_revisions", "v1", 1.5, LowerIsBetter}}

type LibrarySample struct {
	Format, Workflow                                                                        string
	CitationCorrect, QuoteExact, EntailmentCorrect, AttributionCorrect, UnanswerableCorrect bool
	LatencyMS, Tokens, RoleRevisions                                                        float64
}
type MetricReport struct {
	Values      map[string]float64            `json:"values"`
	Failures    []string                      `json:"failures"`
	Diagnostics map[string]map[string]float64 `json:"diagnostics"`
}

func EvaluateLibraryMetrics(samples []LibrarySample) MetricReport {
	report := MetricReport{Values: map[string]float64{}, Failures: []string{}, Diagnostics: map[string]map[string]float64{}}
	if len(samples) == 0 {
		report.Failures = append(report.Failures, "no samples")
		return report
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].LatencyMS < samples[j].LatencyMS })
	truth := func(selectValue func(LibrarySample) bool) float64 {
		count := 0
		for _, s := range samples {
			if selectValue(s) {
				count++
			}
		}
		return float64(count) / float64(len(samples))
	}
	report.Values["citation_precision"] = truth(func(s LibrarySample) bool { return s.CitationCorrect })
	report.Values["quote_exact_match"] = truth(func(s LibrarySample) bool { return s.QuoteExact })
	report.Values["entailment_accuracy"] = truth(func(s LibrarySample) bool { return s.EntailmentCorrect })
	report.Values["attribution_accuracy"] = truth(func(s LibrarySample) bool { return s.AttributionCorrect })
	report.Values["unanswerable_accuracy"] = truth(func(s LibrarySample) bool { return s.UnanswerableCorrect })
	report.Values["p95_latency_ms"] = samples[(len(samples)-1)*95/100].LatencyMS
	for _, s := range samples {
		report.Values["average_tokens"] += s.Tokens / float64(len(samples))
		report.Values["average_role_revisions"] += s.RoleRevisions / float64(len(samples))
		key := s.Format + ":" + s.Workflow
		if report.Diagnostics[key] == nil {
			report.Diagnostics[key] = map[string]float64{}
		}
		report.Diagnostics[key]["samples"]++
	}
	for _, definition := range LibraryMetricDefinitions {
		value := report.Values[definition.Name]
		failed := (definition.Direction == HigherIsBetter && value < definition.Threshold) || (definition.Direction == LowerIsBetter && value > definition.Threshold)
		if failed {
			report.Failures = append(report.Failures, fmt.Sprintf("%s=%g threshold=%g", definition.Name, value, definition.Threshold))
		}
	}
	return report
}
