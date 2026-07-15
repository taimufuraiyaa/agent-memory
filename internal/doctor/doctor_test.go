package doctor

import (
	"context"
	"errors"
	"testing"
)

type checkFunc struct {
	name string
	run  func(context.Context) Result
}

func (c checkFunc) Name() string                   { return c.name }
func (c checkFunc) Run(ctx context.Context) Result { return c.run(ctx) }

func TestRunnerKeepsIndependentResultsAndSanitizesEvidence(t *testing.T) {
	runner := NewRunner(
		checkFunc{name: "pass", run: func(context.Context) Result { return Result{Status: StatusPass, Evidence: "ready"} }},
		checkFunc{name: "fail", run: func(context.Context) Result {
			return Result{Status: StatusFail, Evidence: "token=secret-value", Err: errors.New("broken secret-value")}
		}},
		checkFunc{name: "skip", run: func(context.Context) Result { return Result{Status: StatusSkipped, Evidence: "not configured"} }},
	)

	results := runner.Run(context.Background())

	if len(results) != 3 || results[0].Name != "pass" || results[1].Name != "fail" || results[2].Name != "skip" {
		t.Fatalf("unexpected results: %+v", results)
	}
	if results[1].Evidence != "[redacted]" || results[1].Message != "broken [redacted]" {
		t.Fatalf("secret was not sanitized: %+v", results[1])
	}
}
