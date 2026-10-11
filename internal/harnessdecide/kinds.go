// Package harnessdecide is the harness's one way to ask for advice. A decision is a choice
// among options the deterministic runtime has already allowed, or a recommendation to be
// more careful; it is never a grant. Callers use one typed method per kind of decision and
// get back either a value that is bounded by construction or no advice at all, together
// with a status that says why. Nothing here imports a concrete provider, so the same service
// serves a fake, a local model or the TypeSafe leaf.
package harnessdecide

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Kind names one decision question and the version of its meaning. A new answer shape or a
// changed meaning is a new version, so an old record or an old provider never meets a
// question it did not agree to.
type Kind string

const (
	KindVisibility      Kind = "visibility.v1"
	KindModel           Kind = "model.v1"
	KindTools           Kind = "tools.v1"
	KindCache           Kind = "cache.v1"
	KindCommandRisk     Kind = "command_risk.v1"
	KindFileSensitivity Kind = "file_sensitivity.v1"
	KindSubgoals        Kind = "subgoal_dedup.v1"
)

// Class is the most sensitive data a question carries.
type Class int

const (
	// ClassOpaque questions carry positional aliases, counts, sizes and a fixed vocabulary
	// only: never a prompt, a goal, file content, a path, argument text or a real identifier
	// that came from the project or the run.
	ClassOpaque Class = iota
	// ClassInternal questions carry a bounded, redacted line of project-derived text.
	ClassInternal
)

func (c Class) String() string {
	if c == ClassInternal {
		return "internal"
	}
	return "opaque"
}

// Shape is the form of a valid answer.
type Shape int

const (
	// ShapeOne selects exactly one candidate.
	ShapeOne Shape = iota
	// ShapeSubset selects a bounded, ordered subset.
	ShapeSubset
)

// layout says how a request becomes a question.
type layout int

const (
	// layoutItems asks about caller-supplied items: the candidates are the items.
	layoutItems layout = iota
	// layoutLabels asks about one subject: the candidates are the kind's fixed labels.
	layoutLabels
	// layoutItemsOrNone asks which of the items the subject duplicates, with a fixed "none".
	layoutItemsOrNone
)

// None is the candidate that means "none of the others".
const None = "none"

// Spec is everything fixed about one kind. It is a copy: changing it changes nothing.
type Spec struct {
	Kind   Kind
	Egress Class
	// Aliased replaces each item's identifier with a positional alias before the question
	// leaves this package. Identifiers the operator or the harness defines (providers and
	// tools) are not aliased, because they say what is being chosen and name nothing private.
	Aliased bool
	Shape   Shape
	// Labels are the fixed candidates of a layoutLabels kind.
	Labels []string
	// MinItems and MaxItems bound the caller's items; fewer than MinItems is not a question.
	MinItems, MaxItems int
	// MinSelect and MaxSelect bound a subset answer. A zero MaxSelect means "set by the caller".
	MinSelect, MaxSelect int
	MinConfidence        float64
	Deadline             time.Duration
	MaxReplyBytes        int
	// CanRequire says whether the operator may mark the kind required. Only kinds whose
	// cautious fallback adds friction or withholds data can be.
	CanRequire bool
	// FactKeys is the vocabulary of integer facts an item or subject may carry.
	FactKeys []string
	layout   layout
	// gate, when set, must hold for the subject's facts or there is nothing to decide.
	gate func(Facts) bool
}

// Fixed limits shared by every kind.
const (
	// MaxQuestionItems matches what the TypeSafe API accepts as choices in one question.
	MaxQuestionItems = 32
	maxFacts         = 8
	maxFactValue     = 1_000_000_000
	maxNoteBytes     = 200
	maxClassBytes    = 24
	// MinCacheObservations is how many measured turns the cache decision needs.
	MinCacheObservations = 5
)

// CacheStrategy is how the assembler orders chunks: a stable order keeps the prompt prefix
// identical from turn to turn so a provider's cache can hit, a relevance order puts the most
// relevant first. Both are correct; they differ in cost.
type CacheStrategy string

const (
	CacheStable    CacheStrategy = "stable"
	CacheRelevance CacheStrategy = "relevance"
)

// Risk is a judgment about one action. It can only be added to what the deterministic
// policy decided, never subtracted from it.
type Risk string

const (
	RiskRoutine   Risk = "routine"
	RiskCareful   Risk = "careful"
	RiskHazardous Risk = "hazardous"
)

// Rank orders risks; an unknown risk ranks lowest, so it can never raise anything.
func (r Risk) Rank() int {
	switch r {
	case RiskCareful:
		return 1
	case RiskHazardous:
		return 2
	}
	return 0
}

// Sensitivity is a judgment about one file. It can only be added to what the deterministic
// classifier decided.
type Sensitivity string

const (
	SensitivityOrdinary  Sensitivity = "ordinary"
	SensitivitySensitive Sensitivity = "sensitive"
)

var specs = map[Kind]Spec{
	KindVisibility: {Kind: KindVisibility, Egress: ClassOpaque, Aliased: true, Shape: ShapeSubset, MinItems: 2, MaxItems: MaxQuestionItems, MinSelect: 0, MaxSelect: 8,
		MinConfidence: 0.6, Deadline: 2 * time.Second, MaxReplyBytes: 1024, FactKeys: []string{"k", "r", "t"}, layout: layoutItems},
	KindModel: {Kind: KindModel, Egress: ClassOpaque, Shape: ShapeOne, MinItems: 2, MaxItems: MaxQuestionItems,
		MinConfidence: 0.5, Deadline: 2 * time.Second, MaxReplyBytes: 256, FactKeys: []string{"c", "l"}, layout: layoutItems},
	KindTools: {Kind: KindTools, Egress: ClassOpaque, Shape: ShapeSubset, MinItems: 2, MaxItems: MaxQuestionItems, MinSelect: 1,
		MinConfidence: 0.6, Deadline: 2 * time.Second, MaxReplyBytes: 1024, FactKeys: []string{"k", "t", "u"}, layout: layoutItems},
	KindCache: {Kind: KindCache, Egress: ClassOpaque, Shape: ShapeOne, Labels: []string{string(CacheStable), string(CacheRelevance)},
		MinConfidence: 0.6, Deadline: 2 * time.Second, MaxReplyBytes: 256, FactKeys: []string{"h", "n", "p"}, layout: layoutLabels,
		gate: func(f Facts) bool { _, hit := f["h"]; return hit && f["n"] >= MinCacheObservations }},
	KindCommandRisk: {Kind: KindCommandRisk, Egress: ClassOpaque, Shape: ShapeOne, Labels: []string{string(RiskRoutine), string(RiskCareful), string(RiskHazardous)},
		MinConfidence: 0.6, Deadline: 1500 * time.Millisecond, MaxReplyBytes: 256, CanRequire: true,
		FactKeys: []string{"cp", "de", "ic", "lg", "n", "np", "pp", "rr"}, layout: layoutLabels},
	KindFileSensitivity: {Kind: KindFileSensitivity, Egress: ClassInternal, Shape: ShapeOne, Labels: []string{string(SensitivityOrdinary), string(SensitivitySensitive)},
		MinConfidence: 0.6, Deadline: 2 * time.Second, MaxReplyBytes: 256, CanRequire: true, FactKeys: []string{"d", "z"}, layout: layoutLabels},
	KindSubgoals: {Kind: KindSubgoals, Egress: ClassInternal, Aliased: true, Shape: ShapeOne, MinItems: 1, MaxItems: MaxQuestionItems - 1,
		MinConfidence: 0.7, Deadline: 2 * time.Second, MaxReplyBytes: 256, FactKeys: []string{"t"}, layout: layoutItemsOrNone},
}

// SpecOf returns the fixed description of a kind.
func SpecOf(kind Kind) (Spec, bool) {
	spec, ok := specs[kind]
	if !ok {
		return Spec{}, false
	}
	spec.Labels = append([]string(nil), spec.Labels...)
	spec.FactKeys = append([]string(nil), spec.FactKeys...)
	return spec, true
}

// Kinds lists every kind in a fixed order.
func Kinds() []Kind {
	out := make([]Kind, 0, len(specs))
	for kind := range specs {
		out = append(out, kind)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s Spec) allows(key string) bool {
	for _, k := range s.FactKeys {
		if k == key {
			return true
		}
	}
	return false
}

// Facts are small integer observations with names from a kind's fixed vocabulary. They are
// how a question says anything about an item without saying what the item is.
type Facts map[string]int

var factKeyRE = regexp.MustCompile(`^[a-z][a-z0-9]{0,7}$`)

// encode renders facts in a fixed order, refusing any key outside the vocabulary and any
// value outside the range.
func (f Facts) encode(spec Spec) (string, error) {
	keys := make([]string, 0, len(f))
	for key, value := range f {
		// The vocabulary is at most maxFacts well-formed names, so checking against it bounds
		// the count and the shape of the keys as well.
		if !spec.allows(key) || value < 0 || value > maxFactValue {
			return "", fmt.Errorf("fact %q is not allowed", key)
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(strconv.Itoa(f[key]))
	}
	return b.String(), nil
}

// ParseFacts reads what encode wrote. It is what a provider uses, so it is as strict as the
// writer: any other shape is an error.
func ParseFacts(text string) (Facts, error) {
	facts := Facts{}
	if text == "" {
		return facts, nil
	}
	parts := strings.Split(text, ";")
	if len(parts) > maxFacts {
		return nil, fmt.Errorf("too many facts")
	}
	last := ""
	for _, part := range parts {
		key, raw, ok := strings.Cut(part, "=")
		if !ok || !factKeyRE.MatchString(key) || key <= last {
			return nil, fmt.Errorf("malformed facts")
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > maxFactValue || strconv.Itoa(value) != raw {
			return nil, fmt.Errorf("malformed facts")
		}
		facts[key] = value
		last = key
	}
	return facts, nil
}

// Item is one thing a decision is about. Its identifier is private to the caller when the
// kind is aliased: the provider only ever sees a positional alias.
type Item struct {
	ID    string
	Class string
	Facts Facts
	// Note is a redacted line of project-derived text, allowed only for internal kinds.
	Note string
}

var (
	classRE      = regexp.MustCompile(`^[a-z0-9_]{0,24}$`)
	providerIDRE = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
)

func visibleText(text string, max int) bool {
	if text == "" || len(text) > max || !utf8.ValidString(text) {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// validateItem checks one item against a kind. The identifier rules differ: an aliased
// identifier is only a key, so any bounded text will do; an identifier that is sent as it
// is must be a plain name.
func (s Spec) validateItem(item Item) error {
	if len(item.Class) > maxClassBytes || !classRE.MatchString(item.Class) {
		return fmt.Errorf("class")
	}
	if s.Aliased {
		if !visibleText(item.ID, 128) {
			return fmt.Errorf("identifier")
		}
	} else if s.layout == layoutItems && !providerIDRE.MatchString(item.ID) {
		return fmt.Errorf("identifier")
	}
	if _, err := item.Facts.encode(s); err != nil {
		return err
	}
	return s.validateNote(item.Note)
}

// validateNote enforces that an internal kind always carries its line of text, since that
// text is the point of asking, and that an opaque kind never does.
func (s Spec) validateNote(note string) error {
	switch {
	case s.Egress != ClassInternal && note != "":
		return fmt.Errorf("note on an opaque kind")
	case s.Egress == ClassInternal && !visibleText(note, maxNoteBytes):
		return fmt.Errorf("note")
	}
	return nil
}
