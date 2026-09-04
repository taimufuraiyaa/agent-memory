package evaluation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"
)

const GraphRAGCorpusSchemaV1 = "agent-memory-graphrag-evaluation/v1"

type GraphRAGCorpus struct {
	Schema               string         `json:"schema"`
	ApprovedCostMicroUSD int64          `json:"approved_cost_microusd"`
	Cases                []GraphRAGCase `json:"cases"`
}

type GraphRAGCase struct {
	ID                          string          `json:"id"`
	Topology                    string          `json:"topology"`
	Tenant                      string          `json:"tenant"`
	Language                    string          `json:"language"`
	Scenario                    string          `json:"scenario"`
	Episode                     string          `json:"episode"`
	Category                    string          `json:"category"`
	GoldIDs                     []string        `json:"gold_ids"`
	BasicIDs                    []string        `json:"basic_ids"`
	GraphIDs                    []string        `json:"graph_ids"`
	AuthorizedEvidenceIDs       []string        `json:"authorized_evidence_ids"`
	DeletedIDs                  []string        `json:"deleted_ids,omitempty"`
	Claims                      []GraphRAGClaim `json:"claims"`
	BasicAvailable              bool            `json:"basic_available"`
	ShadowOnly                  bool            `json:"shadow_only"`
	BasicLatencyMicroseconds    int64           `json:"basic_latency_us"`
	ShadowBasicLatencyMicros    int64           `json:"shadow_basic_latency_us"`
	GraphSelectionLatencyMicros int64           `json:"graph_selection_latency_us"`
	CostMicroUSD                int64           `json:"cost_microusd"`
}

type GraphRAGClaim struct {
	ID          string   `json:"id"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type GraphRAGReport struct {
	Schema                    string        `json:"schema"`
	CaseCount                 int           `json:"case_count"`
	GroundedClaimRate         float64       `json:"grounded_claim_rate"`
	BasicRelationalRecall     float64       `json:"basic_relational_recall"`
	GraphRelationalRecall     float64       `json:"graph_relational_recall"`
	RelationalImprovement     float64       `json:"relational_improvement"`
	BasicGlobalRecall         float64       `json:"basic_global_recall"`
	GraphGlobalRecall         float64       `json:"graph_global_recall"`
	GlobalImprovement         float64       `json:"global_improvement"`
	BasicDirectPrecision      float64       `json:"basic_direct_precision"`
	GraphDirectPrecision      float64       `json:"graph_direct_precision"`
	DirectPrecisionRegression float64       `json:"direct_precision_regression"`
	BasicLatencyRegression    float64       `json:"basic_latency_regression"`
	BasicP95                  time.Duration `json:"basic_p95"`
	ShadowBasicP95            time.Duration `json:"shadow_basic_p95"`
	LocalGraphSelectionP95    time.Duration `json:"local_graph_selection_p95"`
	GlobalGraphSelectionP95   time.Duration `json:"global_graph_selection_p95"`
	CostMicroUSD              int64         `json:"cost_microusd"`
	ApprovedCostMicroUSD      int64         `json:"approved_cost_microusd"`
	Failures                  []string      `json:"failures"`
	Passed                    bool          `json:"passed"`
}

func LoadGraphRAGCorpus(reader io.Reader) (GraphRAGCorpus, error) {
	if reader == nil {
		return GraphRAGCorpus{}, errors.New("GraphRAG evaluation corpus is required")
	}
	decoder := json.NewDecoder(io.LimitReader(reader, 8<<20))
	decoder.DisallowUnknownFields()
	var corpus GraphRAGCorpus
	if err := decoder.Decode(&corpus); err != nil {
		return GraphRAGCorpus{}, fmt.Errorf("decode GraphRAG evaluation corpus: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return GraphRAGCorpus{}, errors.New("GraphRAG evaluation corpus must contain one JSON object")
	}
	if err := validateGraphRAGCorpus(corpus); err != nil {
		return GraphRAGCorpus{}, err
	}
	return corpus, nil
}

func EvaluateGraphRAG(corpus GraphRAGCorpus) (GraphRAGReport, error) {
	if err := validateGraphRAGCorpus(corpus); err != nil {
		return GraphRAGReport{}, err
	}
	report := GraphRAGReport{Schema: GraphRAGCorpusSchemaV1, CaseCount: len(corpus.Cases), ApprovedCostMicroUSD: corpus.ApprovedCostMicroUSD}
	var grounded, claims int
	var relationalBasic, relationalGraph, relationalGold int
	var globalBasic, globalGraph, globalGold int
	var directBasicRelevant, directBasicReturned, directGraphRelevant, directGraphReturned int
	var basicLatencies, shadowLatencies, localLatencies, globalLatencies []time.Duration
	for _, evaluationCase := range corpus.Cases {
		authorized := stringSet(evaluationCase.AuthorizedEvidenceIDs)
		for _, claim := range evaluationCase.Claims {
			claims++
			if len(claim.EvidenceIDs) > 0 && allInSet(claim.EvidenceIDs, authorized) {
				grounded++
			}
		}
		gold := stringSet(evaluationCase.GoldIDs)
		basicRelevant := countInSet(evaluationCase.BasicIDs, gold)
		graphRelevant := countInSet(evaluationCase.GraphIDs, gold)
		switch evaluationCase.Category {
		case "relational":
			relationalBasic, relationalGraph, relationalGold = relationalBasic+basicRelevant, relationalGraph+graphRelevant, relationalGold+len(gold)
			localLatencies = append(localLatencies, time.Duration(evaluationCase.GraphSelectionLatencyMicros)*time.Microsecond)
		case "global":
			globalBasic, globalGraph, globalGold = globalBasic+basicRelevant, globalGraph+graphRelevant, globalGold+len(gold)
			globalLatencies = append(globalLatencies, time.Duration(evaluationCase.GraphSelectionLatencyMicros)*time.Microsecond)
		case "direct":
			directBasicRelevant, directBasicReturned = directBasicRelevant+basicRelevant, directBasicReturned+len(evaluationCase.BasicIDs)
			directGraphRelevant, directGraphReturned = directGraphRelevant+graphRelevant, directGraphReturned+len(evaluationCase.GraphIDs)
		}
		basicLatencies = append(basicLatencies, time.Duration(evaluationCase.BasicLatencyMicroseconds)*time.Microsecond)
		shadowLatencies = append(shadowLatencies, time.Duration(evaluationCase.ShadowBasicLatencyMicros)*time.Microsecond)
		report.CostMicroUSD += evaluationCase.CostMicroUSD
	}
	report.GroundedClaimRate = ratio(grounded, claims)
	report.BasicRelationalRecall, report.GraphRelationalRecall = ratio(relationalBasic, relationalGold), ratio(relationalGraph, relationalGold)
	report.RelationalImprovement = report.GraphRelationalRecall - report.BasicRelationalRecall
	report.BasicGlobalRecall, report.GraphGlobalRecall = ratio(globalBasic, globalGold), ratio(globalGraph, globalGold)
	report.GlobalImprovement = report.GraphGlobalRecall - report.BasicGlobalRecall
	report.BasicDirectPrecision, report.GraphDirectPrecision = ratio(directBasicRelevant, directBasicReturned), ratio(directGraphRelevant, directGraphReturned)
	report.DirectPrecisionRegression = report.BasicDirectPrecision - report.GraphDirectPrecision
	report.BasicP95, report.ShadowBasicP95 = percentile95(basicLatencies), percentile95(shadowLatencies)
	if report.BasicP95 > 0 {
		report.BasicLatencyRegression = float64(report.ShadowBasicP95-report.BasicP95) / float64(report.BasicP95)
	}
	report.LocalGraphSelectionP95, report.GlobalGraphSelectionP95 = percentile95(localLatencies), percentile95(globalLatencies)
	report.Failures = graphRAGThresholdFailures(report)
	report.Passed = len(report.Failures) == 0
	return report, nil
}

func validateGraphRAGCorpus(corpus GraphRAGCorpus) error {
	if corpus.Schema != GraphRAGCorpusSchemaV1 || corpus.ApprovedCostMicroUSD < 1 || len(corpus.Cases) < 11 {
		return errors.New("GraphRAG evaluation corpus identity, budget, or coverage is invalid")
	}
	requiredTopologies := stringSet([]string{"standalone", "self-managed-a", "self-managed-b", "hosted-a", "hosted-b"})
	requiredScenarios := stringSet([]string{"direct", "relational", "global", "contradiction", "ambiguity", "deletion", "cache_hit", "provider_failure", "multilingual", "adversarial", "large_corpus"})
	seenIDs, topologies, scenarios, languages := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	episodesByTopology := map[string]map[string]struct{}{}
	for _, evaluationCase := range corpus.Cases {
		if strings.TrimSpace(evaluationCase.ID) == "" || strings.TrimSpace(evaluationCase.Tenant) == "" || !slices.Contains([]string{"day1", "day10"}, evaluationCase.Episode) || !slices.Contains([]string{"direct", "relational", "global"}, evaluationCase.Category) || len(evaluationCase.GoldIDs) == 0 || !evaluationCase.BasicAvailable || !evaluationCase.ShadowOnly || evaluationCase.BasicLatencyMicroseconds < 1 || evaluationCase.ShadowBasicLatencyMicros < 1 || evaluationCase.GraphSelectionLatencyMicros < 0 || evaluationCase.CostMicroUSD < 0 {
			return fmt.Errorf("GraphRAG evaluation case %q is incomplete", evaluationCase.ID)
		}
		if _, duplicate := seenIDs[evaluationCase.ID]; duplicate {
			return fmt.Errorf("duplicate GraphRAG evaluation case %q", evaluationCase.ID)
		}
		seenIDs[evaluationCase.ID] = struct{}{}
		topologies[evaluationCase.Topology], scenarios[evaluationCase.Scenario], languages[evaluationCase.Language] = struct{}{}, struct{}{}, struct{}{}
		if episodesByTopology[evaluationCase.Topology] == nil {
			episodesByTopology[evaluationCase.Topology] = map[string]struct{}{}
		}
		episodesByTopology[evaluationCase.Topology][evaluationCase.Episode] = struct{}{}
		prefix := evaluationCase.Tenant + ":"
		for _, id := range append(append(append([]string{}, evaluationCase.GoldIDs...), evaluationCase.BasicIDs...), append(evaluationCase.GraphIDs, evaluationCase.AuthorizedEvidenceIDs...)...) {
			if !strings.HasPrefix(id, prefix) {
				return fmt.Errorf("GraphRAG case %q crosses tenant scope", evaluationCase.ID)
			}
		}
		for _, deleted := range evaluationCase.DeletedIDs {
			if slices.Contains(evaluationCase.GraphIDs, deleted) || slices.Contains(evaluationCase.BasicIDs, deleted) {
				return fmt.Errorf("GraphRAG case %q returned deleted evidence", evaluationCase.ID)
			}
		}
		for _, claim := range evaluationCase.Claims {
			if strings.TrimSpace(claim.ID) == "" {
				return fmt.Errorf("GraphRAG case %q has an unidentified claim", evaluationCase.ID)
			}
		}
	}
	if !allInSet(mapKeys(requiredTopologies), topologies) || !allInSet(mapKeys(requiredScenarios), scenarios) || len(languages) < 3 {
		return errors.New("GraphRAG evaluation corpus lacks topology, scenario, or multilingual coverage")
	}
	for topology := range requiredTopologies {
		if !allInSet([]string{"day1", "day10"}, episodesByTopology[topology]) {
			return fmt.Errorf("GraphRAG evaluation topology %q lacks the Day-1/Day-10 journey", topology)
		}
	}
	return nil
}

func graphRAGThresholdFailures(report GraphRAGReport) []string {
	var failures []string
	checks := []struct {
		failed bool
		name   string
	}{
		{report.GroundedClaimRate < 1, "grounded_claim_rate_below_100_percent"},
		{report.RelationalImprovement < .10, "relational_improvement_below_10_points"},
		{report.GlobalImprovement < .15, "global_improvement_below_15_points"},
		{report.DirectPrecisionRegression > .01, "direct_precision_regressed_over_1_point"},
		{report.BasicLatencyRegression >= .02, "basic_p95_regressed_2_percent_or_more"},
		{report.LocalGraphSelectionP95 > 75*time.Millisecond, "local_graph_selection_p95_over_75ms"},
		{report.GlobalGraphSelectionP95 > 250*time.Millisecond, "global_graph_selection_p95_over_250ms"},
		{report.CostMicroUSD > report.ApprovedCostMicroUSD, "approved_cost_budget_exceeded"},
	}
	for _, check := range checks {
		if check.failed {
			failures = append(failures, check.name)
		}
	}
	sort.Strings(failures)
	return failures
}

func percentile95(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	values = append([]time.Duration(nil), values...)
	slices.Sort(values)
	return values[(len(values)-1)*95/100]
}
func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
func allInSet(values []string, set map[string]struct{}) bool {
	for _, value := range values {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}
func countInSet(values []string, set map[string]struct{}) int {
	count := 0
	for _, value := range values {
		if _, ok := set[value]; ok {
			count++
		}
	}
	return count
}
func mapKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result
}
