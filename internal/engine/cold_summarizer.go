package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/observability"
)

const summarizeMethodExtractive = "extractive"

// SummarizeResult holds a summary and byte counts for compression metrics.
type SummarizeResult struct {
	Summary       string
	Method        string
	OriginalBytes int
	SummaryBytes  int
}

// CompressionRatio returns summary/original (lower = better compression).
func (r SummarizeResult) CompressionRatio() float64 {
	if r.OriginalBytes == 0 {
		return 1.0
	}
	return float64(r.SummaryBytes) / float64(r.OriginalBytes)
}

// ColdTierSummarizer condenses aging memories into compact cold-tier summaries
// using local extractive summarization — no external API required.
//
// Strategy: take the first ~80 words of content (preserving the most important
// context that appears early), then append a structured metadata footer with
// type, confidence, entities, and outcome. This keeps summaries local-first and
// deterministic while ensuring key facts survive even after the narrative is
// truncated.
type ColdTierSummarizer struct{}

// NewColdTierSummarizer creates a ColdTierSummarizer.
func NewColdTierSummarizer() *ColdTierSummarizer {
	return &ColdTierSummarizer{}
}

// Summarize produces a condensed representation of a MemoryEntry and emits
// compression metrics.
func (s *ColdTierSummarizer) Summarize(ctx context.Context, mem core.MemoryEntry) (SummarizeResult, error) {
	start := time.Now()
	result, err := summarizeExtractive(mem)

	m := observability.GetRegistry()
	workspace := mem.Workspace
	status := "success"
	if err != nil {
		status = "error"
	}
	m.ColdSummarizationTotal.WithLabelValues(workspace, summarizeMethodExtractive, status).Inc()
	if err == nil {
		m.ColdSummarizationDuration.WithLabelValues(workspace, summarizeMethodExtractive).Observe(time.Since(start).Seconds())
		m.ColdCompressionOrigBytes.WithLabelValues(workspace).Observe(float64(result.OriginalBytes))
		m.ColdCompressionRatio.WithLabelValues(workspace, summarizeMethodExtractive).Observe(result.CompressionRatio())
	}
	return result, err
}

// summarizeExtractive produces a local summary without any external call.
// It takes the first ~80 words of content and appends a metadata footer with
// type, confidence, entities, and outcome — preserving key facts even when the
// narrative is truncated.
func summarizeExtractive(mem core.MemoryEntry) (SummarizeResult, error) {
	words := strings.Fields(mem.Content)
	const maxWords = 80
	var body string
	if len(words) <= maxWords {
		body = mem.Content
	} else {
		body = strings.Join(words[:maxWords], " ") + "…"
	}

	var parts []string
	parts = append(parts, body)

	// Metadata footer preserves structured facts even when narrative is truncated.
	var meta []string
	meta = append(meta, fmt.Sprintf("type:%s", mem.Type))
	if mem.Confidence > 0 {
		meta = append(meta, fmt.Sprintf("confidence:%.2f", mem.Confidence))
	}
	if len(mem.Entities) > 0 {
		meta = append(meta, "entities:"+strings.Join(mem.Entities, ","))
	}
	if mem.Outcome != nil {
		meta = append(meta, fmt.Sprintf("outcome:%s", mem.Outcome.Result))
		if mem.Outcome.Approach != "" {
			meta = append(meta, "approach:"+mem.Outcome.Approach)
		}
	}
	if len(meta) > 0 {
		parts = append(parts, "["+strings.Join(meta, " ")+"]")
	}

	summary := strings.Join(parts, "\n")
	origBytes := len(mem.Content)
	return SummarizeResult{
		Summary:       summary,
		Method:        summarizeMethodExtractive,
		OriginalBytes: origBytes,
		SummaryBytes:  len(summary),
	}, nil
}
