package harnessjev

import (
	"fmt"
	"sort"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
)

// Everything sent to the service is built here from fixed text, numbers and the bounded
// fields of a decision question. No prompt, goal, file content, argument text or credential
// can reach this file, and the only project text is the one Note an internal kind carries.

const (
	maxInstructions = 300
	maxDescription  = 250
	maxState        = 4000
	maxNote         = 200
	questionID      = "decision"
)

// rendered is a question ready for the service and what is needed to read its answer.
type rendered struct {
	instructions string
	criteria     map[string]string
	state        string
	// candidates[i] is the harness candidate behind the criterion "a<i>".
	candidates []string
	// limit is how many candidates a subset answer may select.
	limit int
}

func alias(i int) string { return fmt.Sprintf("a%d", i) }

var instructions = map[harnessdecide.Kind]string{
	harnessdecide.KindVisibility:      "Pick the context chunk that would most help a coding assistant if it were shown in more detail. Chunks are described only by source type, size and relevance.",
	harnessdecide.KindModel:           "Pick the model best suited to a coding task. Models are described only by their configured names and coarse cost and speed tiers.",
	harnessdecide.KindTools:           "Pick the tool a coding assistant most needs next when only a few tool descriptions fit. Tools are described only by name, size and use so far.",
	harnessdecide.KindCache:           "Pick how to order prompt context. Stable keeps the start of the prompt identical between turns so the provider's cache can hit; relevance puts the most relevant first and may break the cache.",
	harnessdecide.KindCommandRisk:     "Pick how careful a developer should be about approving the described action: routine, careful or hazardous. Only its kind and fixed flags are described.",
	harnessdecide.KindFileSensitivity: "Pick whether a file with the described redacted path is likely to hold secrets or personal data: ordinary or sensitive.",
	harnessdecide.KindSubgoals:        "Pick the existing subgoal that the new subgoal duplicates, or none if it is new work.",
}

var labels = map[string]string{
	string(harnessdecide.CacheStable):          "Keep the start of the prompt identical between turns so a cache can hit.",
	string(harnessdecide.CacheRelevance):       "Order by relevance, even if the start of the prompt changes.",
	string(harnessdecide.RiskRoutine):          "A routine action with an ordinary, bounded effect.",
	string(harnessdecide.RiskCareful):          "An action that deserves a closer look before approval.",
	string(harnessdecide.RiskHazardous):        "An action that could do serious or hard-to-undo harm.",
	string(harnessdecide.SensitivityOrdinary):  "Ordinary project content.",
	string(harnessdecide.SensitivitySensitive): "Likely to hold secrets, credentials or personal data.",
	harnessdecide.None:                         "None of the existing subgoals: this is new work.",
}

// factNames says, per fact key, how to put it in words. A fact whose key is not listed for
// the kind is ignored, so nothing unexpected is forwarded.
var factNames = map[harnessdecide.Kind]map[string]string{
	harnessdecide.KindVisibility:      {"r": "relevance %d of 100", "t": "about %d tokens"},
	harnessdecide.KindModel:           {"c": "cost tier %d", "l": "latency tier %d"},
	harnessdecide.KindTools:           {"t": "schema about %d tokens", "u": "used %d times so far"},
	harnessdecide.KindCache:           {"h": "observed cache hit rate %d percent", "n": "over %d turns", "p": "prefix about %d tokens"},
	harnessdecide.KindCommandRisk:     {"n": "touches %d paths", "np": "not a catalogued command (%d)", "pp": "runs a project program (%d)", "rr": "runs a project recipe (%d)", "ic": "inline code (%d)", "cp": "touches control-plane files (%d)", "lg": "a large change (%d)", "de": "deletes files (%d)"},
	harnessdecide.KindFileSensitivity: {"d": "directory depth %d", "z": "size bucket %d"},
	harnessdecide.KindSubgoals:        {"t": "about %d tokens"},
}

func describeFacts(kind harnessdecide.Kind, revision string) string {
	facts, _ := harnessdecide.ParseFacts(revision) // malformed facts parse to none, and so say nothing
	names := factNames[kind]
	keys := make([]string, 0, len(facts))
	for key := range facts {
		if _, known := names[key]; known {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, key := range keys {
		parts[i] = fmt.Sprintf(names[key], facts[key])
	}
	return strings.Join(parts, ", ")
}

// text makes untrusted text safe to forward: printable, one line, bounded.
func text(s string, max int) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case b.Len()+4 > max:
			return strings.TrimSpace(b.String())
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x2028 || r == 0x2029:
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func join(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, ", ")
}

// render builds the service question for one decision question. It refuses anything that is
// not a question the table describes, so the provider never forwards a shape it was not
// written for.
func render(q harness.DecisionQuestion) (rendered, error) {
	kind := harnessdecide.Kind(q.Kind)
	spec, ok := harnessdecide.SpecOf(kind)
	if !ok {
		return rendered{}, errUnsupported
	}
	if len(q.Candidates) < 2 || len(q.Candidates) > harnessdecide.MaxQuestionItems+1 {
		return rendered{}, errMalformed
	}
	byID := map[string]harness.EvidenceRef{}
	var subject harness.EvidenceRef
	for _, ref := range q.Evidence {
		if ref.ID == "subject" {
			subject = ref
			continue
		}
		byID[ref.ID] = ref
	}
	r := rendered{instructions: instructions[kind], criteria: map[string]string{}, candidates: append([]string(nil), q.Candidates...), limit: 1}
	// Project text is used only by the two internal kinds below (a subgoal's summary and a
	// file's path); no branch of an opaque kind reads a note, so one that arrives is ignored.
	for i, candidate := range q.Candidates {
		var description string
		switch {
		case labels[candidate] != "" && (len(spec.Labels) > 0 || kind == harnessdecide.KindSubgoals && candidate == harnessdecide.None):
			description = labels[candidate]
		default:
			ref := byID[candidate]
			var named string
			if plainName(candidate) {
				named = candidate
			}
			switch kind {
			case harnessdecide.KindSubgoals:
				description = join("subgoal: "+ref.Note, describeFacts(kind, ref.Revision))
			case harnessdecide.KindModel, harnessdecide.KindTools:
				description = join(label(kind, named), describeFacts(kind, ref.Revision))
			default:
				description = join(label(kind, ref.Class), describeFacts(kind, ref.Revision))
			}
		}
		if description == "" {
			description = "option " + alias(i)
		}
		r.criteria[alias(i)] = text(description, maxDescription)
	}
	r.state = state(kind, subject)
	if k := subjectLimit(subject); k > 0 {
		r.limit = k
	}
	return r, nil
}

func label(kind harnessdecide.Kind, name string) string {
	switch kind {
	case harnessdecide.KindVisibility:
		if name == "" {
			return "context chunk"
		}
		return "context chunk from a " + text(name, 24) + " source"
	case harnessdecide.KindModel:
		if name == "" {
			return ""
		}
		return "model " + name
	case harnessdecide.KindTools:
		if name == "" {
			return ""
		}
		return "tool " + name
	}
	return ""
}

func state(kind harnessdecide.Kind, subject harness.EvidenceRef) string {
	b := "Harness decision " + string(kind) + "."
	class := text(subject.Class, 24)
	switch kind {
	case harnessdecide.KindCommandRisk:
		if class != "" {
			b += " Action: " + class + "."
		}
	case harnessdecide.KindFileSensitivity:
		if class != "" {
			b += " Extension class: " + class + "."
		}
	}
	switch kind {
	case harnessdecide.KindCache, harnessdecide.KindCommandRisk, harnessdecide.KindFileSensitivity:
		if facts := describeFacts(kind, subject.Revision); facts != "" {
			b += " " + strings.ToUpper(facts[:1]) + facts[1:] + "."
		}
	}
	if subject.Note != "" {
		switch kind {
		case harnessdecide.KindSubgoals:
			b += " New subgoal: " + text(subject.Note, maxNote) + "."
		case harnessdecide.KindFileSensitivity:
			b += " Redacted path: " + text(subject.Note, maxNote) + "."
		}
	}
	return b // bounded by construction: fixed text, a class of at most 24 bytes and one note of at most maxNote
}

func plainName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// subjectLimit reads the selection cap a subset question carries; zero when there is none.
func subjectLimit(subject harness.EvidenceRef) int {
	facts, _ := harnessdecide.ParseFacts(subject.Revision)
	return facts["k"]
}
