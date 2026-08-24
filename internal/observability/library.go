package observability

import (
	"errors"
	"strings"
	"time"
)

type LibraryOperationMetric struct {
	Format       string        `json:"format"`
	Workflow     string        `json:"workflow"`
	Outcome      string        `json:"outcome"`
	Latency      time.Duration `json:"latency"`
	InputTokens  int           `json:"input_tokens"`
	OutputTokens int           `json:"output_tokens"`
	SourceText   string        `json:"-"`
	Quote        string        `json:"-"`
}

func (m LibraryOperationMetric) Validate() error {
	if strings.TrimSpace(m.Format) == "" || strings.TrimSpace(m.Workflow) == "" || strings.TrimSpace(m.Outcome) == "" || m.Latency < 0 || m.InputTokens < 0 || m.OutputTokens < 0 {
		return errors.New("library metric requires labels, non-negative latency, and token counts")
	}
	return nil
}
func (m LibraryOperationMetric) SafeLabels() map[string]string {
	return map[string]string{"format": m.Format, "workflow": m.Workflow, "outcome": m.Outcome}
}
