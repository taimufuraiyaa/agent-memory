package engine

import (
	"context"
	"regexp"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
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
			Diagram:   it.Diagram,
			Outcome:   it.Outcome,
			Tags:      it.Tags,
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
	Tags    []string
	Diagram *core.Diagram
}

func extractTranscriptMemories(transcript string) []extractedMemory {
	lines := splitLines(transcript)
	out := make([]extractedMemory, 0)
	outcomeRe := regexp.MustCompile(`(?i)\b(success|failed|failure|partial)\b`)
	procRe := regexp.MustCompile(`(?i)\b(always|never|must|should)\b`)
	for i := 0; i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "```") {
			lang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(text, "```")))
			switch lang {
			case "mermaid", "plantuml", "dot", "graphviz":
				start := i
				for i+1 < len(lines) {
					if strings.TrimSpace(lines[i+1]) == "```" {
						i++
						break
					}
					i++
				}
				code := strings.TrimRight(strings.Join(lines[start+1:i], "\n"), "\n")
				if strings.TrimSpace(code) != "" {
					tags := []string{"diagram", lang}
					out = append(out, extractedMemory{
						Type:    core.SemanticMemory,
						Content: "Diagram (" + lang + ")",
						Tags:    tags,
						Diagram: &core.Diagram{Lang: lang, Code: code},
					})
				}
				continue
			}
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
