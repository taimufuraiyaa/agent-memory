package engine

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// Session end constraints.
const (
	// maxTranscriptBytes is the hard cap on transcript size before extraction.
	maxTranscriptBytes = 200 * 1024 // 200KB

	// maxTranscriptLines is the hard cap on transcript line count.
	maxTranscriptLines = 5000

	// minEvidenceLength is the minimum character length for a line to be
	// considered as a candidate memory after noise stripping.
	minEvidenceLength = 40

	// maxRepeatedLines is the number of times an identical stripped line may
	// appear before subsequent copies are treated as boilerplate.
	maxRepeatedLines = 2

	// outcomeConfidenceCap is the maximum confidence for outcome memories
	// extracted from session-end transcripts.
	outcomeConfidenceCap = 0.5
)

// noise patterns for line-level stripping.
var (
	// toolOutputPrefix matches lines that start with common shell/IDE prefixes.
	toolOutputPrefix = regexp.MustCompile(`^(?i)(\$\s+|>\s+|#\s+|\./\w|npm\s|yarn\s|go\s|make\s|cargo\s|pip\s|python\s|node\s|curl\s|wget\s|docker\s|kubectl\s|git\s+|ls\s|cd\s|cat\s|echo\s|mkdir\s|rm\s|cp\s|mv\s)`)

	// timestampPrefix matches lines starting with a timestamp pattern like [2026-...
	timestampPrefix = regexp.MustCompile(`^\[\d{4}-`)

	// codeFencePattern matches the start or end of a code fence.
	codeFencePattern = regexp.MustCompile("^```")

	// evidencePatterns for classification.
	// subjectVerbSentence: starts with a capital letter word, contains a common verb.
	subjectVerbSentence = regexp.MustCompile(`^[A-Z][a-z]+\s.*\b(is|are|was|were|has|have|had|does|do|did|will|can|could|should|would|may|might|must|need|use|uses|used|using|call|calls|called|run|runs|ran|find|found|set|sets|create|created|build|built|fix|fixed|change|changed|add|added|remove|removed|update|updated|return|returns|throw|throws|fail|fails|fail|work|works|worked|help|helps|require|requires|support|supports|enable|enables|disable|disables|allow|allows|check|checks|test|tests|handle|handles|process|processes)\b`)

	// proceduralPattern: "to X, do/use/run/call Y" or numbered step.
	proceduralPattern = regexp.MustCompile(`(?i)(\bto\s+\w+.*\b(do|use|run|call|execute|perform|follow|apply)\b|^\d+[\.\)]|\bfirst\b.*\bthen\b|\bstep\s+\d+)`)

	// outcomeContextPattern: an outcome keyword preceded by at least 3 words of context.
	// Uses [^\w]* between words to allow punctuation like periods and commas.
	outcomeContextPattern = regexp.MustCompile(`(?i)\b\w+[^\w]*\s+\w+[^\w]*\s+\w+[^\w]*\s+.*\b(success|succeed|succeeded|successful|fail|failed|failure|partial|work|worked|broke|broken|fix|fixed|resolve|resolved|complete|completed)\b`)
)

type SessionEndResult struct {
	TotalExtracted int      `json:"total_extracted"`
	WrittenIDs     []string `json:"written_ids"`
	TotalSkipped   int      `json:"total_skipped"`
	TotalFailed    int      `json:"total_failed"`
}

type SessionEndExtractor struct {
	pipeline *WritePipeline
}

func NewSessionEndExtractor(pipeline *WritePipeline) *SessionEndExtractor {
	return &SessionEndExtractor{pipeline: pipeline}
}

// ExtractAndStore parses transcript text and writes extracted memories.
// Returns partial results even when individual items fail.
func (s *SessionEndExtractor) ExtractAndStore(ctx context.Context, workspace, transcript string) (*SessionEndResult, error) {
	// Caps: reject oversized transcripts upfront.
	if len(transcript) > maxTranscriptBytes {
		return nil, fmt.Errorf("transcript too large: %d bytes (max %d bytes / %dKB)", len(transcript), maxTranscriptBytes, maxTranscriptBytes/1024)
	}
	lines := splitLines(transcript)
	if len(lines) > maxTranscriptLines {
		return nil, fmt.Errorf("transcript too many lines: %d (max %d)", len(lines), maxTranscriptLines)
	}

	items := extractTranscriptMemories(transcript)
	out := &SessionEndResult{
		WrittenIDs: make([]string, 0, len(items)),
	}
	allFailed := true

	for _, it := range items {
		maxConf := outcomeConfidenceCap
		res, err := s.pipeline.Write(ctx, WriteInput{
			Workspace:     workspace,
			Type:          it.Type,
			Content:       it.Content,
			Diagram:       it.Diagram,
			Outcome:       it.Outcome,
			Tags:          it.Tags,
			Source:        core.MemorySource{Type: core.SourceAgentObservation},
			Mode:          ExtractFast,
			MaxConfidence: &maxConf,
		})
		if err != nil {
			out.TotalFailed++
			continue
		}
		allFailed = false
		if res.Rejected {
			out.TotalSkipped++
			continue
		}
		out.TotalExtracted++
		out.WrittenIDs = append(out.WrittenIDs, res.ID)
	}

	if allFailed && len(items) > 0 {
		return out, fmt.Errorf("all %d extracted items failed to write", len(items))
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

	// --- Stage 1: extract diagram fences and record their line ranges ---
	diagramBlocks := make(map[int]extractedMemory)
	// diagramRanges maps opening fence line → closing fence line (inclusive).
	diagramRanges := make(map[int]int)

	for i := 0; i < len(lines); i++ {
		text := strings.TrimSpace(lines[i])
		if !codeFencePattern.MatchString(text) {
			continue
		}
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
				diagramRanges[start] = i // inclusive end
				diagramBlocks[start] = extractedMemory{
					Type:    core.SemanticMemory,
					Content: "Diagram (" + lang + ")",
					Tags:    []string{"diagram", lang},
					Diagram: &core.Diagram{Lang: lang, Code: code},
				}
			}
		}
	}

	// Helper: is a line index inside any diagram block?
	inDiagramBlock := func(idx int) bool {
		for start, end := range diagramRanges {
			if idx >= start && idx <= end {
				return true
			}
		}
		return false
	}

	// --- Stage 2: strip noise from remaining lines ---
	lineFreq := make(map[string]int)

	// First pass: compute frequency of eligible lines.
	for i := 0; i < len(lines); i++ {
		clean := strings.TrimSpace(lines[i])
		if clean == "" {
			continue
		}
		// Skip all lines inside diagram blocks.
		if inDiagramBlock(i) {
			continue
		}
		// Skip non-diagram code fences completely (content + fences).
		if codeFencePattern.MatchString(clean) {
			continue
		}
		lineFreq[clean]++
	}

	// Second pass: build stripped line list, filtering noise.
	skipUntilClose := false
	stripped := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		clean := strings.TrimSpace(lines[i])

		// Skip blank lines.
		if clean == "" {
			continue
		}

		// Skip all lines inside diagram blocks.
		if inDiagramBlock(i) {
			continue
		}

		// Handle non-diagram code fences: skip the fences and their content.
		if codeFencePattern.MatchString(clean) {
			skipUntilClose = !skipUntilClose
			continue
		}
		if skipUntilClose {
			continue
		}

		// Skip tool output prefixes.
		if toolOutputPrefix.MatchString(clean) {
			continue
		}

		// Skip timestamp-prefixed lines.
		if timestampPrefix.MatchString(clean) {
			continue
		}

		// Skip boilerplate (lines repeated > maxRepeatedLines times).
		if lineFreq[clean] > maxRepeatedLines {
			continue
		}

		stripped = append(stripped, clean)
	}

	// --- Stage 3: classify with evidence ---
	out := make([]extractedMemory, 0)

	// Add extracted diagram blocks.
	for _, dm := range diagramBlocks {
		out = append(out, dm)
	}

	for _, text := range stripped {
		// Length check.
		if len(text) < minEvidenceLength {
			continue
		}

		// Try classification based on evidence patterns.
		switch {
		case matchesEvidence(text, outcomeContextPattern):
			out = append(out, extractedMemory{
				Type:    core.OutcomeMemory,
				Content: text,
				Outcome: &core.Outcome{
					Result:   core.OutcomePartial,
					Approach: "session-end-heuristic",
					Reason:   "transcript signal",
				},
			})
		case matchesEvidence(text, proceduralPattern):
			out = append(out, extractedMemory{
				Type:    core.ProceduralMemory,
				Content: text,
			})
		case matchesEvidence(text, subjectVerbSentence):
			out = append(out, extractedMemory{
				Type:    core.SemanticMemory,
				Content: text,
			})
		default:
			// Line has sufficient length but no evidence structure — skip.
		}
	}

	// --- Stage 4: dedup by keyword overlap ---
	out = dedupByKeywordOverlap(out)

	return out
}

// matchesEvidence returns true if the text matches the given evidence regex.
func matchesEvidence(text string, re *regexp.Regexp) bool {
	return re.MatchString(text)
}

// dedupByKeywordOverlap removes items whose lowercase words overlap >80%
// with an earlier item already in the list.
func dedupByKeywordOverlap(items []extractedMemory) []extractedMemory {
	if len(items) <= 1 {
		return items
	}

	seen := make([]map[string]struct{}, 0, len(items))
	result := make([]extractedMemory, 0, len(items))

	for _, item := range items {
		words := extractWords(item.Content)
		isDup := false
		for _, prev := range seen {
			overlap := wordOverlapRatio(words, prev)
			if overlap > 0.8 {
				isDup = true
				break
			}
		}
		if !isDup {
			result = append(result, item)
			seen = append(seen, words)
		}
	}
	return result
}

// extractWords returns the set of lowercase words from content.
func extractWords(content string) map[string]struct{} {
	words := make(map[string]struct{})
	for _, w := range strings.Fields(strings.ToLower(content)) {
		// Strip common punctuation from word boundaries.
		w = strings.Trim(w, ".,;:!?\"'()[]{}<>")
		if len(w) > 1 {
			words[w] = struct{}{}
		}
	}
	return words
}

// wordOverlapRatio returns the fraction of words in target that appear in source.
func wordOverlapRatio(target, source map[string]struct{}) float64 {
	if len(target) == 0 {
		return 0
	}
	common := 0
	for w := range target {
		if _, ok := source[w]; ok {
			common++
		}
	}
	return float64(common) / float64(len(target))
}

func splitLines(s string) []string {
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}
