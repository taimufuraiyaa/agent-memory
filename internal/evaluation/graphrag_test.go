package evaluation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGraphRAGProductionGoldCorpusMeetsFailClosedThresholds(t *testing.T) {
	corpus := loadGraphRAGGold(t)
	report, err := EvaluateGraphRAG(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || report.GroundedClaimRate != 1 || report.RelationalImprovement < .10 || report.GlobalImprovement < .15 || report.DirectPrecisionRegression > .01 {
		t.Fatalf("GraphRAG evaluation did not certify: %+v", report)
	}
}

func TestGraphRAGProductionGateRejectsUngroundedTenantCrossingAndRegression(t *testing.T) {
	corpus := loadGraphRAGGold(t)
	corpus.Cases[0].Claims[0].EvidenceIDs = []string{"local:not-authorized"}
	report, err := EvaluateGraphRAG(corpus)
	if err != nil || report.Passed {
		t.Fatalf("ungrounded claim passed: report=%+v err=%v", report, err)
	}

	corpus = loadGraphRAGGold(t)
	corpus.Cases[0].GraphIDs = []string{"other-tenant:m1"}
	if _, err := EvaluateGraphRAG(corpus); err == nil {
		t.Fatal("cross-tenant result passed corpus validation")
	}

	corpus = loadGraphRAGGold(t)
	for index := range corpus.Cases {
		corpus.Cases[index].ShadowBasicLatencyMicros = corpus.Cases[index].BasicLatencyMicroseconds * 2
	}
	report, err = EvaluateGraphRAG(corpus)
	if err != nil || report.Passed {
		t.Fatalf("latency regression passed: report=%+v err=%v", report, err)
	}
}

func loadGraphRAGGold(t *testing.T) GraphRAGCorpus {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", "graphrag_gold.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	corpus, err := LoadGraphRAGCorpus(file)
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}
