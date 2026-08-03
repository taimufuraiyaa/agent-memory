// Package locator normalizes and selects short exact-search terms for memories.
package locator

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

const (
	NormalizationVersion = "locator-v1"
	ExtractorVersion     = "deterministic-v1"
	MaxTerms             = 3
)

var (
	hashtagPattern = regexp.MustCompile(`#[\p{L}\p{N}][\p{L}\p{N}._:/-]*`)
	hexSecret      = regexp.MustCompile(`^[[:xdigit:]]{24,}$`)
	jwtLike        = regexp.MustCompile(`^[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}\.[A-Za-z0-9_-]{12,}$`)
	fold           = cases.Fold()

	stopTerms = map[string]struct{}{
		"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {},
		"by": {}, "for": {}, "from": {}, "in": {}, "is": {}, "it": {}, "of": {},
		"on": {}, "or": {}, "system": {}, "that": {}, "the": {}, "this": {}, "thing": {},
		"to": {}, "update": {}, "work": {}, "with": {},
	}
	controlTags = map[string]struct{}{
		"allow-sensitive": {}, "diagram": {}, "low-confidence": {}, "pinned": {},
	}
)

// Input contains explicit terms and deterministic fallback sources.
type Input struct {
	Explicit []string
	Content  string
	Entities []string
	Tags     []string
}

// NormalizeQuery returns one to three normalized exact-search tokens.
func NormalizeQuery(query string) ([]string, error) {
	tokens := strings.Fields(query)
	normalized := make([]string, 0, len(tokens))
	seen := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		term, ok := normalizeToken(token)
		if !ok {
			continue
		}
		if _, duplicate := seen[term]; duplicate {
			continue
		}
		seen[term] = struct{}{}
		normalized = append(normalized, term)
	}
	if len(normalized) == 0 {
		return nil, errors.New("term query must contain at least one searchable token")
	}
	if len(normalized) > MaxTerms {
		return nil, fmt.Errorf("term query contains %d tokens; maximum is %d", len(normalized), MaxTerms)
	}
	return normalized, nil
}

// Extract selects at most three terms, preferring explicit input and then
// hashtags, entities, non-control tags, and identifier-like content tokens.
func Extract(in Input) ([]core.MemoryTerm, error) {
	terms := make([]core.MemoryTerm, 0, MaxTerms)
	seen := make(map[string]struct{}, MaxTerms)

	add := func(raw string, source core.TermSource) {
		if len(terms) >= MaxTerms {
			return
		}
		term, ok := normalizeToken(raw)
		if !ok || isSecretLike(term) {
			return
		}
		if source == core.TermSourceTag {
			if _, control := controlTags[term]; control {
				return
			}
		}
		if _, duplicate := seen[term]; duplicate {
			return
		}
		seen[term] = struct{}{}
		terms = append(terms, core.MemoryTerm{
			Term:                 term,
			Display:              strings.TrimSpace(raw),
			Source:               source,
			Ordinal:              len(terms),
			NormalizationVersion: NormalizationVersion,
			ExtractorVersion:     ExtractorVersion,
		})
	}

	explicitTokens := flatten(in.Explicit)
	validExplicit := 0
	for _, raw := range explicitTokens {
		if term, ok := normalizeToken(raw); ok && !isSecretLike(term) {
			validExplicit++
		}
	}
	if validExplicit > MaxTerms {
		return nil, fmt.Errorf("keywords contain %d normalized tokens; maximum is %d", validExplicit, MaxTerms)
	}
	for _, raw := range explicitTokens {
		add(raw, core.TermSourceExplicit)
	}
	if len(in.Explicit) > 0 {
		return terms, nil
	}

	for _, raw := range hashtagPattern.FindAllString(in.Content, -1) {
		add(raw, core.TermSourceHashtag)
	}
	for _, raw := range flatten(in.Entities) {
		add(raw, core.TermSourceEntity)
	}
	for _, raw := range flatten(in.Tags) {
		add(raw, core.TermSourceTag)
	}
	for _, raw := range identifierCandidates(in.Content) {
		add(raw, core.TermSourceIdentifier)
	}
	return terms, nil
}

func flatten(values []string) []string {
	var out []string
	for _, value := range values {
		out = append(out, strings.Fields(value)...)
	}
	return out
}

func normalizeToken(raw string) (string, bool) {
	raw = strings.TrimSpace(norm.NFKC.String(raw))
	raw = strings.TrimPrefix(raw, "#")
	raw = strings.TrimFunc(raw, func(r rune) bool {
		return unicode.IsPunct(r) && !isIdentifierSeparator(r)
	})
	if raw == "" {
		return "", false
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", false
		}
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !isIdentifierSeparator(r) {
			return "", false
		}
	}
	term := fold.String(raw)
	term = strings.Trim(term, "-_.:/")
	if term == "" || len(term) > 128 {
		return "", false
	}
	if _, stop := stopTerms[term]; stop {
		return "", false
	}
	return term, true
}

func isIdentifierSeparator(r rune) bool {
	switch r {
	case '-', '_', '.', ':', '/':
		return true
	default:
		return false
	}
}

func isSecretLike(term string) bool {
	return hexSecret.MatchString(term) || jwtLike.MatchString(term)
}

func identifierCandidates(content string) []string {
	fields := strings.Fields(content)
	out := make([]string, 0, len(fields))
	for _, raw := range fields {
		candidate := strings.TrimFunc(raw, func(r rune) bool {
			return unicode.IsPunct(r) && !isIdentifierSeparator(r)
		})
		if candidate == "" || strings.HasPrefix(candidate, "#") {
			continue
		}
		hasSeparator := strings.ContainsAny(candidate, "-_.:/")
		hasDigit := strings.IndexFunc(candidate, unicode.IsDigit) >= 0
		hasInternalUpper := false
		for i, r := range candidate {
			if i > 0 && unicode.IsUpper(r) {
				hasInternalUpper = true
				break
			}
		}
		if hasSeparator || hasDigit || hasInternalUpper {
			out = append(out, candidate)
		}
	}
	return out
}
