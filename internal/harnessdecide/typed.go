package harnessdecide

import (
	"context"
	"path"
	"strings"
)

// The methods below are the whole public surface for asking. Each returns a value that is
// bounded by construction (a subset of what the caller offered, or one of a fixed few labels)
// and an Outcome. A value is returned only when the status is applied, with one exception:
// a required kind returns its cautious value for any other status, and says so in the
// outcome. No method returns anything a caller could read as permission.

// Visibility asks which context chunks deserve more room. Items should be in the caller's own
// relevance order: more than the question can hold are cut from the end. The answer is a
// subset of the offered identifiers, in the provider's order; applying it can only promote
// chunks the assembler already found eligible.
func (s *Service) Visibility(ctx context.Context, items []Item) ([]string, Outcome) {
	spec := specs[KindVisibility]
	if len(items) > spec.MaxItems {
		items = items[:spec.MaxItems]
	}
	return s.ask(ctx, KindVisibility, request{items: items, maxSelect: min(spec.MaxSelect, max(1, len(items)/2))})
}

// Model asks which of the eligible providers to prefer. The router has already excluded every
// provider that may not serve the call and will still apply its own fallbacks.
func (s *Service) Model(ctx context.Context, providers []Item) (string, Outcome) {
	selected, out := s.ask(ctx, KindModel, request{items: providers})
	if len(selected) == 0 {
		return "", out
	}
	return selected[0], out
}

// Tools asks which tools to keep when the offered tools' schemas do not fit: keep is how many
// fit. When everything fits there is nothing to decide and nothing is asked. The answer is an
// ordered subset of the offered tools of at most keep; it can never add a tool.
func (s *Service) Tools(ctx context.Context, items []Item, keep int) ([]string, Outcome) {
	spec := specs[KindTools]
	if keep < 1 || len(items) <= keep {
		out := Outcome{Kind: KindTools, Status: StatusNotNeeded}
		s.health.record(out)
		return nil, out
	}
	if len(items) > spec.MaxItems {
		items = items[:spec.MaxItems]
	}
	return s.ask(ctx, KindTools, request{items: items, maxSelect: keep})
}

// Cache asks for the ordering to use, from facts the model meter measured about the provider's
// own cache accounting. Without enough measured turns nothing is asked.
func (s *Service) Cache(ctx context.Context, measured Facts) (CacheStrategy, Outcome) {
	selected, out := s.ask(ctx, KindCache, request{subject: &Item{Class: "cache", Facts: measured}})
	if len(selected) != 1 {
		return "", out
	}
	return CacheStrategy(selected[0]), out
}

// CommandRisk asks how careful to be about one prepared action, from its capability and fixed
// codes and counts, never from its text. The value can only be added to the deterministic
// policy's decision. A required kind returns RiskCareful when the provider does not answer.
func (s *Service) CommandRisk(ctx context.Context, capability string, facts Facts) (Risk, Outcome) {
	selected, out := s.ask(ctx, KindCommandRisk, request{subject: &Item{Class: capability, Facts: facts}})
	if len(selected) == 1 {
		return Risk(selected[0]), out
	}
	if s.required[KindCommandRisk] {
		out.Cautious = true
		return RiskCareful, out
	}
	return "", out
}

// FileSensitivity asks whether a file's name suggests it is sensitive. The path is the one
// piece of project text any kind sends, so the kind is internal and is not asked unless the
// provider may receive that class; the caller passes a redacted, bounded path. The value can
// only be added to the deterministic classification. A required kind returns
// SensitivitySensitive when the provider does not answer.
func (s *Service) FileSensitivity(ctx context.Context, redactedPath string, facts Facts) (Sensitivity, Outcome) {
	subject := Item{Class: ClassOfPath(redactedPath), Facts: facts, Note: redactedPath}
	selected, out := s.ask(ctx, KindFileSensitivity, request{subject: &subject})
	if len(selected) == 1 {
		return Sensitivity(selected[0]), out
	}
	if s.required[KindFileSensitivity] {
		out.Cautious = true
		return SensitivitySensitive, out
	}
	return "", out
}

// Subgoals asks which of the existing subgoals a new one duplicates, from short redacted
// summaries, so the kind is internal. The answer is the matching existing identifier or the
// empty string for none. It only ever names a subgoal the caller offered; the caller still
// checks exact duplicates itself, and a merge must not widen the target's authority.
func (s *Service) Subgoals(ctx context.Context, existing []Item, candidate Item) (string, Outcome) {
	spec := specs[KindSubgoals]
	if len(existing) > spec.MaxItems {
		existing = existing[:spec.MaxItems]
	}
	selected, out := s.ask(ctx, KindSubgoals, request{items: existing, subject: &candidate})
	if len(selected) != 1 || selected[0] == None {
		return "", out
	}
	return selected[0], out
}

// ClassOfPath names a path's extension as a coarse class: lower-case letters and digits, at
// most eight, or empty. It is how a file is described without its name.
func ClassOfPath(p string) string {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))
	if len(ext) == 0 || len(ext) > 8 {
		return ""
	}
	for _, r := range ext {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return ""
		}
	}
	return ext
}

// Raise returns the higher of two risks. It is how advice is combined with a deterministic
// decision: the result is never lower than the base.
func Raise(base, advice Risk) Risk {
	if advice.Rank() > base.Rank() {
		return advice
	}
	return base
}
