package harnessdecide

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestTheKindTableIsClosedVersionedAndInternallyConsistent(t *testing.T) {
	want := []Kind{KindCache, KindCommandRisk, KindFileSensitivity, KindModel, KindSubgoals, KindTools, KindVisibility}
	if got := Kinds(); !reflect.DeepEqual(got, want) || !sort.SliceIsSorted(got, func(i, j int) bool { return got[i] < got[j] }) {
		t.Fatalf("kinds = %v", got)
	}
	for _, kind := range Kinds() {
		spec, ok := SpecOf(kind)
		if !ok || spec.Kind != kind || !strings.HasSuffix(string(kind), ".v1") {
			t.Errorf("%s: %+v", kind, spec)
			continue
		}
		if spec.Deadline <= 0 || spec.Deadline > 3*time.Second || spec.MaxReplyBytes <= 0 || spec.MinConfidence < 0.5 || spec.MinConfidence >= 1 {
			t.Errorf("%s: unreasonable bounds %+v", kind, spec)
		}
		switch spec.layout {
		case layoutLabels:
			if len(spec.Labels) < 2 || spec.MinItems != 0 || spec.MaxItems != 0 || spec.Shape != ShapeOne {
				t.Errorf("%s: a labels kind is malformed: %+v", kind, spec)
			}
		default:
			if spec.MinItems < 1 || spec.MaxItems < spec.MinItems || spec.MaxItems > MaxQuestionItems || len(spec.Labels) != 0 {
				t.Errorf("%s: an items kind is malformed: %+v", kind, spec)
			}
		}
		if spec.Shape == ShapeSubset && spec.MaxSelect < 0 {
			t.Errorf("%s: negative selection cap", kind)
		}
		for _, key := range spec.FactKeys {
			if !factKeyRE.MatchString(key) {
				t.Errorf("%s: bad fact key %q", kind, key)
			}
		}
		if !sort.StringsAreSorted(spec.FactKeys) {
			t.Errorf("%s: fact keys are not in order", kind)
		}
	}
	// Exactly the two kinds that carry project text are internal, and exactly the two
	// raise-only kinds may be required: a required fallback must only ever add caution.
	internal, requirable := []Kind{}, []Kind{}
	for _, kind := range Kinds() {
		spec, _ := SpecOf(kind)
		if spec.Egress == ClassInternal {
			internal = append(internal, kind)
		}
		if spec.CanRequire {
			requirable = append(requirable, kind)
		}
	}
	if !reflect.DeepEqual(internal, []Kind{KindFileSensitivity, KindSubgoals}) || !reflect.DeepEqual(requirable, []Kind{KindCommandRisk, KindFileSensitivity}) {
		t.Fatalf("internal %v, requirable %v", internal, requirable)
	}
	if _, ok := SpecOf("visibility.v2"); ok {
		t.Fatal("an unknown version has a spec")
	}
	if _, ok := SpecOf(""); ok {
		t.Fatal("the empty kind has a spec")
	}
}

func TestSpecOfReturnsACopy(t *testing.T) {
	spec, _ := SpecOf(KindCommandRisk)
	spec.Labels[0] = "tampered"
	spec.FactKeys[0] = "tampered"
	fresh, _ := SpecOf(KindCommandRisk)
	if fresh.Labels[0] == "tampered" || fresh.FactKeys[0] == "tampered" {
		t.Fatal("a caller changed the table")
	}
}

func TestOnlyTheFixedLabelsAndTheCacheGateAreWhatTheTableSays(t *testing.T) {
	risk, _ := SpecOf(KindCommandRisk)
	if !reflect.DeepEqual(risk.Labels, []string{"routine", "careful", "hazardous"}) {
		t.Fatalf("%v", risk.Labels)
	}
	cache, _ := SpecOf(KindCache)
	if !reflect.DeepEqual(cache.Labels, []string{"stable", "relevance"}) {
		t.Fatalf("%v", cache.Labels)
	}
	file, _ := SpecOf(KindFileSensitivity)
	if !reflect.DeepEqual(file.Labels, []string{"ordinary", "sensitive"}) {
		t.Fatalf("%v", file.Labels)
	}
	if cache.gate(Facts{"h": 80, "n": MinCacheObservations - 1}) || !cache.gate(Facts{"h": 80, "n": MinCacheObservations}) || cache.gate(Facts{"n": 99}) || cache.gate(nil) {
		t.Fatal("the cache gate does not require a measured hit rate over enough turns")
	}
	if !cache.gate(Facts{"h": 0, "n": 50}) {
		t.Fatal("a measured hit rate of zero is a measurement")
	}
}

func TestFactsAreEncodedInFixedOrderAndOnlyFromTheKindsVocabulary(t *testing.T) {
	spec, _ := SpecOf(KindVisibility)
	got, err := Facts{"t": 120, "r": 7}.encode(spec)
	if err != nil || got != "r=7;t=120" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err := (Facts{}).encode(spec); err != nil || got != "" {
		t.Fatalf("%q %v", got, err)
	}
	for name, facts := range map[string]Facts{
		"a key outside the vocabulary": {"x": 1},
		"a negative value":             {"t": -1},
		"a huge value":                 {"t": maxFactValue + 1},
		"a malformed key":              {"T": 1},
		"too many":                     {"a": 1, "b": 1, "c": 1, "d": 1, "e": 1, "f": 1, "g": 1, "h": 1, "i": 1},
	} {
		if _, err := facts.encode(spec); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := (Facts{"t": maxFactValue}).encode(spec); err != nil {
		t.Errorf("the largest value was refused: %v", err)
	}
	if _, err := (Facts{"t": 0}).encode(spec); err != nil {
		t.Errorf("zero was refused: %v", err)
	}
}

func TestParseFactsReadsExactlyWhatEncodeWritesAndNothingElse(t *testing.T) {
	spec, _ := SpecOf(KindCommandRisk)
	original := Facts{"cp": 1, "n": 3, "np": 0, "rr": 1}
	text, err := original.encode(spec)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseFacts(text)
	if err != nil || !reflect.DeepEqual(back, original) {
		t.Fatalf("%v %v", back, err)
	}
	if got, err := ParseFacts(""); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"t", "t=", "=1", "t=1;", ";t=1", "t=-1", "t=01", "t=1.5", "t=1e3", "t= 1", " t=1", "T=1", "t=1;t=2", "t=2;r=1", "t=1;;r=2", "1t=1", "t=1;r",
		"a=1;b=1;c=1;d=1;e=1;f=1;g=1;h=1;i=1", "t=" + "9999999999", "toolongkey1=1", "t=1\n"} {
		if _, err := ParseFacts(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := ParseFacts("a=1;b=2"); err != nil {
		t.Errorf("sorted keys were refused: %v", err)
	}
	if _, err := ParseFacts("t=" + "1000000000"); err != nil {
		t.Errorf("the largest value was refused: %v", err)
	}
	if _, err := ParseFacts("t=" + "1000000001"); err == nil {
		t.Error("one over the largest value was accepted")
	}
}

func TestItemsAreCheckedAgainstTheirKind(t *testing.T) {
	visibility, _ := SpecOf(KindVisibility)
	model, _ := SpecOf(KindModel)
	subgoals, _ := SpecOf(KindSubgoals)
	for name, tc := range map[string]struct {
		spec Spec
		item Item
		ok   bool
	}{
		"an aliased id may be anything bounded":    {visibility, Item{ID: "src/Some File.go#rev:12", Class: "memory"}, true},
		"an aliased id may not be empty":           {visibility, Item{ID: ""}, false},
		"an aliased id may not be huge":            {visibility, Item{ID: strings.Repeat("a", 129)}, false},
		"an aliased id may not hold a control":     {visibility, Item{ID: "a\nb"}, false},
		"an aliased id must be text":               {visibility, Item{ID: "\xff\xfe"}, false},
		"a provider id is a plain name":            {model, Item{ID: "openai-text"}, true},
		"a provider id may not be a path":          {model, Item{ID: "../etc/passwd"}, false},
		"a provider id may not start with a digit": {model, Item{ID: "1abc"}, false},
		"a provider id may not be upper case":      {model, Item{ID: "OpenAI"}, false},
		"a class is lower case letters and digits": {visibility, Item{ID: "a", Class: "memory_2"}, true},
		"a class may not hold a path":              {visibility, Item{ID: "a", Class: "src/x"}, false},
		"a class may not be long":                  {visibility, Item{ID: "a", Class: strings.Repeat("a", 25)}, false},
		"facts must be in the vocabulary":          {visibility, Item{ID: "a", Facts: Facts{"zz": 1}}, false},
		"an opaque kind refuses a note":            {visibility, Item{ID: "a", Note: "text"}, false},
		"an internal kind accepts a bounded note":  {subgoals, Item{ID: "g1", Note: "tidy the parser"}, true},
		"a note may not be long":                   {subgoals, Item{ID: "g1", Note: strings.Repeat("n", maxNoteBytes+1)}, false},
		"a note may not hold a control character":  {subgoals, Item{ID: "g1", Note: "a\x1b[31mb"}, false},
		"a note at the limit is fine":              {subgoals, Item{ID: "g1", Note: strings.Repeat("n", maxNoteBytes)}, true},
	} {
		if err := tc.spec.validateItem(tc.item); (err == nil) != tc.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestClassOfPathDescribesAFileWithoutItsName(t *testing.T) {
	for path, want := range map[string]string{"main.go": "go", "a/b/C.TXT": "txt", ".env": "env", "Makefile": "", "x.": "", "archive.tar.gz": "gz", "weird.é": "",
		"long.extensionxx": "", "x.a-b": "", "dir.d/file": "", "x.mp4": "mp4"} {
		if got := ClassOfPath(path); got != want {
			t.Errorf("ClassOfPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRaiseNeverLowersAndNothingUnknownRaises(t *testing.T) {
	for _, base := range []Risk{"", RiskRoutine, RiskCareful, RiskHazardous} {
		for _, advice := range []Risk{"", RiskRoutine, RiskCareful, RiskHazardous, "bogus"} {
			got := Raise(base, advice)
			if got.Rank() < base.Rank() {
				t.Errorf("Raise(%q, %q) = %q lowered the base", base, advice, got)
			}
			if advice.Rank() <= base.Rank() && got != base {
				t.Errorf("Raise(%q, %q) = %q: advice that is not higher changed the base", base, advice, got)
			}
			if advice.Rank() > base.Rank() && got != advice {
				t.Errorf("Raise(%q, %q) = %q: higher advice was ignored", base, advice, got)
			}
		}
	}
	if Risk("bogus").Rank() != 0 || RiskRoutine.Rank() != 0 || RiskCareful.Rank() != 1 || RiskHazardous.Rank() != 2 {
		t.Fatal("ranks are wrong")
	}
}
