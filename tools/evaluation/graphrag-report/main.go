package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/taimufuraiyaa/agent-memory/internal/evaluation"
)

func main() {
	corpusPath := flag.String("corpus", "internal/evaluation/testdata/graphrag_gold.json", "strict GraphRAG gold corpus")
	flag.Parse()
	file, err := os.Open(*corpusPath)
	if err != nil {
		fail(err)
	}
	defer file.Close()
	corpus, err := evaluation.LoadGraphRAGCorpus(file)
	if err != nil {
		fail(err)
	}
	report, err := evaluation.EvaluateGraphRAG(corpus)
	if err != nil {
		fail(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fail(err)
	}
	if !report.Passed {
		os.Exit(1)
	}
}

func fail(err error) { _, _ = fmt.Fprintln(os.Stderr, err); os.Exit(2) }
