package api

import (
	"os/exec"
	"testing"
)

func TestRunScorer(t *testing.T) {
	// Smoke-test the scorer without depending on a developer's local benchmark
	// results directory. Full runner/scorer fixture integration lives in
	// benchmark/test_benchmark.py.
	cmd := exec.Command("python3", "benchmark/score.py", "--help")
	cmd.Dir = "../.."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scorer CLI failed: %v\n%s", err, output)
	}
}
