package engine

import (
	"context"
	"regexp"
	"strings"

	"github.com/time/timebooks/agent-memory/internal/core"
)

type SessionEndResult struct {
	TotalExtracted int      `json:"total_extracted"`
	WrittenIDs     []string `json:"written_ids"`
}

type SessionEndExtractor struct {
	pipeline *WritePipeline
}

func NewSessionEndExtractor(pipeline *WritePipeline) *SessionEndExtractor {
	return &SessionEndExtractor{pipeline: pipeline}
}

// ExtractAndStore parses transcript text and writes extracted memories.
func (s *SessionEndExtractor) ExtractAndStore(ctx context.Context, workspace, transcript string) (*SessionEndResult, error) {
	items := extractTranscriptMemories(transcript)
	out := &SessionEndResult{WrittenIDs: make([]string, 0, len(items))}
	for _, it := range items {
		res, err := s.pipeline.Write(ctx, WriteInput{
			Workspace: workspace,
			Type:      it.Type,
			Content:   it.Content,
			Outcome:   it.Outcome,
			Source:    core.MemorySource{Type: core.SourceAgentObservation},
			Mode:      ExtractFast,
		})
		if err != nil {
			return nil, err
		}
		if !res.Rejected {
			out.TotalExtracted++
			out.WrittenIDs = append(out.WrittenIDs, res.ID)
		}
	}
	return out, nil
}

type extractedMemory struct {
	Type    core.MemoryType
	Content string
	Outcome *core.Outcome
}

func extractTranscriptMemories(transcript string) []extractedMemory {
	lines := splitLines(transcript)
	out := make([]extractedMemory, 0)
	outcomeRe := regexp.MustCompile(`(?i)\b(success|failed|failure|partial)\b`)
	procRe := regexp.MustCompile(`(?i)\b(always|never|must|should)\b`)
	for _, ln := range lines {
		text := strings.TrimSpace(ln)
		if text == "" {
			continue
		}
		switch {
		case outcomeRe.MatchString(text):
			res := core.OutcomePartial
			lower := strings.ToLower(text)
			if strings.Contains(lower, "success") {
				res = core.OutcomeSuccess
			} else if strings.Contains(lower, "fail") {
				res = core.OutcomeFailure
			}
			out = append(out, extractedMemory{
				Type:    core.OutcomeMemory,
				Content: text,
				Outcome: &core.Outcome{Result: res, Approach: "session-end extraction", Reason: "transcript signal"},
			})
		case procRe.MatchString(text):
			out = append(out, extractedMemory{Type: core.ProceduralMemory, Content: text})
		default:
			out = append(out, extractedMemory{Type: core.SemanticMemory, Content: text})
		}
	}
	return out
}

func splitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

