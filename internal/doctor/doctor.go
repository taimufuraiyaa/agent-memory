// Package doctor provides independent, read-only integration diagnostics.
package doctor

import (
	"context"
	"regexp"
	"strings"
)

type Status string

const (
	StatusPass    Status = "pass"
	StatusWarning Status = "warning"
	StatusFail    Status = "fail"
	StatusSkipped Status = "skipped"
)

type Result struct {
	Name            string `json:"name"`
	Status          Status `json:"status"`
	Evidence        string `json:"evidence,omitempty"`
	Message         string `json:"message,omitempty"`
	NextAction      string `json:"next_action,omitempty"`
	RepairAvailable bool   `json:"repair_available"`
	Err             error  `json:"-"`
}

type Check interface {
	Name() string
	Run(context.Context) Result
}

type Runner struct{ checks []Check }

func NewRunner(checks ...Check) *Runner { return &Runner{checks: checks} }

func (r *Runner) Run(ctx context.Context) []Result {
	results := make([]Result, 0, len(r.checks))
	for _, check := range r.checks {
		result := check.Run(ctx)
		result.Name = check.Name()
		if result.Err != nil && strings.TrimSpace(result.Message) == "" {
			result.Message = result.Err.Error()
		}
		result.Evidence = sanitize(result.Evidence)
		result.Message = sanitize(result.Message)
		result.Err = nil
		results = append(results, result)
	}
	return results
}

var secretEvidence = regexp.MustCompile(`(?i)(token|secret|password|authorization|api[_-]?key)\s*[=:]\s*\S+`)
var secretValue = regexp.MustCompile(`(?i)secret-value`)

func sanitize(value string) string {
	if secretEvidence.MatchString(value) {
		return "[redacted]"
	}
	return secretValue.ReplaceAllString(value, "[redacted]")
}
