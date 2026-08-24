package ingestion

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

type MarkdownAdapter struct {
	ParserVersion        string
	NormalizationVersion string
}

type markdownHeading struct {
	level int
	title string
	start int
	end   int
	id    string
}

func (a MarkdownAdapter) Extract(editionID string, source []byte) (ExtractedDocument, error) {
	if strings.TrimSpace(editionID) == "" {
		return ExtractedDocument{}, errors.New("edition id is required")
	}
	if strings.TrimSpace(a.ParserVersion) == "" || strings.TrimSpace(a.NormalizationVersion) == "" {
		return ExtractedDocument{}, errors.New("parser and normalization versions are required")
	}
	raw := string(source)
	headings := parseMarkdownHeadings(editionID, raw)
	if len(headings) == 0 && strings.TrimSpace(raw) != "" {
		headings = []markdownHeading{{level: 1, title: "Document", start: 0, end: len(raw), id: stableNodeID(editionID, 0, "Document")}}
	}
	for i := range headings {
		headings[i].end = len(raw)
		for j := i + 1; j < len(headings); j++ {
			if headings[j].level <= headings[i].level {
				headings[i].end = headings[j].start
				break
			}
		}
	}

	normalized := normalizeText(raw)
	nodes := make([]library.StructuralNode, 0, len(headings))
	sections := make([]ExtractedSection, 0, len(headings))
	stack := map[int]string{}
	ordinals := map[string]int{}
	for i, heading := range headings {
		var parentID *string
		for level := heading.level - 1; level >= 1; level-- {
			if parent, ok := stack[level]; ok {
				parentCopy := parent
				parentID = &parentCopy
				break
			}
		}
		for level := heading.level; level <= 6; level++ {
			delete(stack, level)
		}
		stack[heading.level] = heading.id
		parentKey := "root"
		if parentID != nil {
			parentKey = *parentID
		}
		kind := markdownNodeKind(heading.level)
		ordinalKey := parentKey + ":" + string(kind)
		ordinal := ordinals[ordinalKey]
		ordinals[ordinalKey]++
		node := library.StructuralNode{
			ID: heading.id, EditionID: editionID, ParentID: parentID, Kind: kind,
			Ordinal: ordinal, Title: heading.title, StartOffset: heading.start, EndOffset: heading.end, Explicit: true,
		}
		nodes = append(nodes, node)

		localEnd := len(raw)
		if i+1 < len(headings) {
			localEnd = headings[i+1].start
		}
		sourceText := raw[heading.start:localEnd]
		sections = append(sections, ExtractedSection{
			NodeID:         heading.id,
			SourceText:     sourceText,
			NormalizedText: strings.TrimSpace(normalizeText(sourceText)),
			Span: SourceSpan{
				SourceStart: heading.start, SourceEnd: localEnd,
				NormalizedStart: normalizedOffset(raw, heading.start), NormalizedEnd: normalizedOffset(raw, localEnd),
			},
		})
	}
	if err := library.ValidateStructure(nodes); err != nil {
		return ExtractedDocument{}, fmt.Errorf("validate markdown structure: %w", err)
	}
	return ExtractedDocument{
		EditionID: editionID, ParserVersion: a.ParserVersion, NormalizationVersion: a.NormalizationVersion,
		NormalizedText: normalized, Nodes: nodes, Sections: sections,
	}, nil
}

func parseMarkdownHeadings(editionID, raw string) []markdownHeading {
	headings := []markdownHeading{}
	for start := 0; start < len(raw); {
		end := strings.IndexByte(raw[start:], '\n')
		if end < 0 {
			end = len(raw)
		} else {
			end += start
		}
		line := strings.TrimSuffix(raw[start:end], "\r")
		level := 0
		for level < len(line) && level < 6 && line[level] == '#' {
			level++
		}
		if level > 0 && level < len(line) && line[level] == ' ' {
			title := strings.TrimSpace(line[level+1:])
			if title != "" {
				headings = append(headings, markdownHeading{level: level, title: title, start: start, id: stableNodeID(editionID, start, title)})
			}
		}
		if end == len(raw) {
			break
		}
		start = end + 1
	}
	return headings
}

func markdownNodeKind(level int) library.StructuralNodeKind {
	switch level {
	case 1:
		return library.NodeChapter
	case 2:
		return library.NodeSection
	default:
		return library.NodeSubsection
	}
}

func stableNodeID(editionID string, offset int, title string) string {
	digest := sha256.Sum256([]byte(editionID + "\x00" + strconv.Itoa(offset) + "\x00" + title))
	return "node_" + hex.EncodeToString(digest[:12])
}

func normalizeText(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func normalizedOffset(raw string, sourceOffset int) int {
	return len(normalizeText(raw[:sourceOffset]))
}
