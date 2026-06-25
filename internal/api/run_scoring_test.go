package api

import (
	"os"
	"os/exec"
	"testing"
)

func TestRunScorer(t *testing.T) {
	cmd := exec.Command("python3", "benchmark/score.py", "--run-dir", "benchmark/results/continuation-full-10000", "--db", "benchmark/results/continuation-full-10000/benchmark.db", "--ingest", "--format", "raw")
	cmd.Dir = "../.."
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run failed: %v", err)
	}
}
