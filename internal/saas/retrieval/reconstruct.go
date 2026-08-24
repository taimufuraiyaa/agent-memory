package retrieval

import (
	"context"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	reconstructionStrategy = "structural-neighbors-v1"
	contextStrategy        = "reconstructive-structural-v1"
	windowBeforeAnchor     = 1
	windowAfterAnchor      = 8
	maxSentenceExtension   = 4
)

func reconstructEvidence(ctx context.Context, repository Repository, tenant string, authorizedSourceIDs []string, ranked []Evidence, query string, limit int) ([]Evidence, error) {
	if len(ranked) == 0 {
		return []Evidence{}, nil
	}
	anchorLimit := limit * 10
	if anchorLimit > 150 {
		anchorLimit = 150
	}
	if anchorLimit <= 0 || anchorLimit > len(ranked) {
		anchorLimit = len(ranked)
	}
	anchors := make([]ContextAnchor, 0, anchorLimit)
	for _, evidence := range ranked[:anchorLimit] {
		anchors = append(anchors, ContextAnchor{
			SourceID: evidence.SourceID, SourceVersion: evidence.SourceVersion,
			StructuralNodeID: evidence.StructuralNodeID, PassageID: evidence.PassageID,
		})
	}
	expanded, err := repository.ContextByAnchors(ctx, tenant, authorizedSourceIDs, anchors)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(authorizedSourceIDs))
	for _, sourceID := range authorizedSourceIDs {
		allowed[sourceID] = struct{}{}
	}
	regions := make(map[string][]Candidate)
	for _, candidate := range expanded {
		if _, ok := allowed[candidate.SourceID]; !ok {
			continue
		}
		key := structuralRegionKey(candidate.SourceID, candidate.SourceVersion, candidate.StructuralNodeID)
		regions[key] = append(regions[key], candidate)
	}
	bestByRegion := make(map[string]Evidence)
	for _, anchor := range ranked[:anchorLimit] {
		key := structuralRegionKey(anchor.SourceID, anchor.SourceVersion, anchor.StructuralNodeID)
		members := regions[key]
		index := candidateIndex(members, anchor.PassageID)
		if index < 0 {
			continue
		}
		start := maxInt(0, index-windowBeforeAnchor)
		minimumEnd := minInt(len(members), index+windowAfterAnchor+1)
		end, complete := semanticWindowEnd(members, minimumEnd)
		window := assembleWindow(anchor, members[start:end], query, !complete)
		current, exists := bestByRegion[key]
		if !exists || window.Score > current.Score || window.Score == current.Score && window.PassageID < current.PassageID {
			bestByRegion[key] = window
		}
	}
	windows := make([]Evidence, 0, len(bestByRegion))
	for _, evidence := range bestByRegion {
		windows = append(windows, evidence)
	}
	sort.SliceStable(windows, func(i, j int) bool {
		if windows[i].Score == windows[j].Score {
			if windows[i].SourceID == windows[j].SourceID {
				return windows[i].PassageID < windows[j].PassageID
			}
			return windows[i].SourceID < windows[j].SourceID
		}
		return windows[i].Score > windows[j].Score
	})
	if len(windows) > limit {
		windows = windows[:limit]
	}
	return windows, nil
}

func supportedWindows(windows []Evidence) []Evidence {
	supported := make([]Evidence, 0, len(windows))
	for _, evidence := range windows {
		if evidence.AnswerSupport {
			supported = append(supported, evidence)
		}
	}
	if len(supported) > 0 {
		return supported
	}
	return windows
}

func assembleWindow(anchor Evidence, members []Candidate, query string, clipped bool) Evidence {
	texts := make([]string, 0, len(members))
	passageIDs := make([]string, 0, len(members))
	citationIDs := make([]string, 0, len(members))
	proseMembers := 0
	for _, member := range members {
		text := strings.TrimSpace(member.Text)
		if text == "" {
			continue
		}
		texts = append(texts, text)
		passageIDs = append(passageIDs, member.PassageID)
		citationIDs = append(citationIDs, member.CitationID)
		if !headingLike(text) {
			proseMembers++
		}
	}
	anchor.Text = strings.Join(texts, " ")
	anchor.ReconstructionStrategy = reconstructionStrategy
	anchor.IncludedPassageIDs = passageIDs
	anchor.IncludedCitationIDs = citationIDs
	anchor.WindowClipped = clipped
	terms := meaningfulTerms(query)
	coverage := termCoverage(anchor.Text, terms)
	tokens := len(strings.Fields(anchor.Text))
	explanation := hasExplanatoryLanguage(anchor.Text)
	definition := definitionQuestion(query)
	definitionSupport := definitionRelation(anchor.Text, terms)
	definitionHeading := hasDefinitionHeading(anchor.Text, terms)
	depth := math.Min(float64(tokens)/40, 1)
	anchor.Score += .4*coverage + .25*depth + .15*math.Min(float64(proseMembers), 2)
	if definition && definitionSupport {
		anchor.Score += .8
		if definitionHeading {
			anchor.Score += .4
		}
	} else if explanation {
		anchor.Score += .4
	}
	if proseMembers == 0 {
		anchor.Score -= .8
	}
	anchor.Breakdown.Total = anchor.Score
	anchor.AnswerSupport = sufficientContext(query, coverage, tokens, proseMembers, explanation, definitionSupport)
	return anchor
}

func semanticWindowEnd(members []Candidate, minimumEnd int) (int, bool) {
	end := minInt(len(members), maxInt(0, minimumEnd))
	if end == 0 {
		return 0, true
	}
	if endsSentence(members[end-1].Text) {
		return end, true
	}
	maximumEnd := minInt(len(members), end+maxSentenceExtension)
	for end < maximumEnd {
		end++
		if endsSentence(members[end-1].Text) {
			return end, true
		}
	}
	return end, false
}

func endsSentence(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	last, _ := lastRune(trimmed)
	return strings.ContainsRune(".!?。！？", last)
}

func sufficientContext(query string, coverage float64, tokens, proseMembers int, explanation, definitionSupport bool) bool {
	if coverage < 1 || tokens < 8 || proseMembers == 0 {
		return false
	}
	if definitionQuestion(query) {
		return tokens >= 12 && definitionSupport
	}
	return tokens >= 8 && explanation
}

func meaningfulTerms(value string) []string {
	stop := map[string]struct{}{
		"a": {}, "about": {}, "an": {}, "are": {}, "define": {}, "do": {}, "does": {},
		"is": {}, "of": {}, "say": {}, "source": {}, "sources": {}, "the": {}, "these": {}, "this": {}, "to": {}, "what": {},
	}
	seen := map[string]struct{}{}
	terms := []string{}
	for _, field := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if len([]rune(field)) < 2 {
			continue
		}
		if _, ignored := stop[field]; ignored {
			continue
		}
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
	}
	return terms
}

func retrievalCue(query string) string {
	terms := meaningfulTerms(query)
	if len(terms) == 0 {
		return strings.TrimSpace(query)
	}
	return strings.Join(terms, " ")
}

func termCoverage(text string, terms []string) float64 {
	if len(terms) == 0 {
		return 0
	}
	lower := strings.ToLower(text)
	matched := 0
	for _, term := range terms {
		if strings.Contains(lower, term) {
			matched++
		}
	}
	return float64(matched) / float64(len(terms))
}

func definitionQuestion(query string) bool {
	lower := strings.ToLower(strings.TrimSpace(query))
	return strings.HasPrefix(lower, "what is ") || strings.HasPrefix(lower, "what are ") ||
		strings.Contains(lower, "definition of") || strings.HasPrefix(lower, "define ")
}

func definitionRelation(text string, terms []string) bool {
	lower := strings.ToLower(text)
	if strings.TrimSpace(lower) == "" || len(terms) == 0 {
		return false
	}
	markers := []string{" is ", " are ", " means ", " includes ", " là ", " bao gồm ", " refers to ", " consists of "}
	for _, term := range terms {
		if strings.Contains(lower, term+":") || strings.Contains(lower, "("+term+"):") {
			return true
		}
		for _, marker := range markers {
			needle := term + marker
			from := 0
			for {
				relative := strings.Index(lower[from:], needle)
				if relative < 0 {
					break
				}
				index := from + relative
				prefix := strings.TrimRightFunc(lower[:index], unicode.IsSpace)
				if prefix == "" {
					return true
				}
				boundary, _ := lastRune(prefix)
				if strings.ContainsRune(".,!?;:\n。！？", boundary) {
					return true
				}
				from = index + len(term)
			}
		}
	}
	return false
}

func hasDefinitionHeading(text string, terms []string) bool {
	lower := strings.ToLower(text)
	for _, term := range terms {
		for _, prefix := range []string{"definition of ", "definition ", "định nghĩa "} {
			if strings.Contains(lower, prefix+term) {
				return true
			}
		}
	}
	return false
}

func hasExplanatoryLanguage(text string) bool {
	lower := " " + strings.ToLower(text) + " "
	for _, marker := range []string{" is ", " are ", " means ", " refers to ", " includes ", " consists of ", " là ", " bao gồm "} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func headingLike(text string) bool {
	trimmed := strings.TrimSpace(text)
	words := len(strings.Fields(trimmed))
	if words == 0 {
		return true
	}
	last, _ := lastRune(trimmed)
	endsSentence := strings.ContainsRune(".!?。！？", last)
	return words <= 7 && !endsSentence
}

func lastRune(value string) (rune, int) {
	var last rune
	count := 0
	for _, r := range value {
		last = r
		count++
	}
	return last, count
}

func candidateIndex(values []Candidate, passageID string) int {
	for index, value := range values {
		if value.PassageID == passageID {
			return index
		}
	}
	return -1
}

func structuralRegionKey(sourceID string, sourceVersion int64, nodeID string) string {
	return sourceID + "\x00" + strconv.FormatInt(sourceVersion, 10) + "\x00" + nodeID
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
