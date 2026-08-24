package locator

import (
	"reflect"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestNormalizeQueryAcceptsOneToThreeStableTokens(t *testing.T) {
	got, err := NormalizeQuery("  #Café  Orders.API  retry-policy ")
	if err != nil {
		t.Fatalf("NormalizeQuery returned error: %v", err)
	}
	want := []string{"café", "orders.api", "retry-policy"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeQuery() = %#v, want %#v", got, want)
	}
}

func TestNormalizeQueryUsesUnicodeCanonicalNormalization(t *testing.T) {
	composed, err := NormalizeQuery("CAFÉ")
	if err != nil {
		t.Fatalf("normalize composed: %v", err)
	}
	decomposed, err := NormalizeQuery("Cafe\u0301")
	if err != nil {
		t.Fatalf("normalize decomposed: %v", err)
	}
	if !reflect.DeepEqual(composed, decomposed) {
		t.Fatalf("canonically equivalent input diverged: %#v != %#v", composed, decomposed)
	}
}

func TestNormalizeQueryRejectsInvalidTokenCountsWithoutTruncating(t *testing.T) {
	for _, query := range []string{"", "one two three four", "the and system"} {
		if _, err := NormalizeQuery(query); err == nil {
			t.Fatalf("NormalizeQuery(%q) expected error", query)
		}
	}
}

func TestExtractPrefersExplicitKeywordsAndDeduplicates(t *testing.T) {
	got, err := Extract(Input{
		Explicit: []string{"#BloomFilter", "SQLite", "bloomfilter"},
		Content:  "The fallback content mentions #ignored.",
		Entities: []string{"other"},
		Tags:     []string{"procedural"},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	want := []core.MemoryTerm{
		{Term: "bloomfilter", Display: "#BloomFilter", Source: core.TermSourceExplicit, Ordinal: 0, NormalizationVersion: NormalizationVersion, ExtractorVersion: ExtractorVersion},
		{Term: "sqlite", Display: "SQLite", Source: core.TermSourceExplicit, Ordinal: 1, NormalizationVersion: NormalizationVersion, ExtractorVersion: ExtractorVersion},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Extract() = %#v, want %#v", got, want)
	}
}

func TestExtractFallbackPriorityAndControlFiltering(t *testing.T) {
	got, err := Extract(Input{
		Content:  "Remember #HotPath and the Orders.API handler",
		Entities: []string{"Payment Gateway"},
		Tags:     []string{"pinned", "diagram", "latency"},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	wantTerms := []string{"hotpath", "payment", "gateway"}
	if len(got) != len(wantTerms) {
		t.Fatalf("Extract() returned %d terms, want %d: %#v", len(got), len(wantTerms), got)
	}
	for i, want := range wantTerms {
		if got[i].Term != want {
			t.Fatalf("term %d = %q, want %q", i, got[i].Term, want)
		}
	}
	if got[0].Source != core.TermSourceHashtag || got[1].Source != core.TermSourceEntity {
		t.Fatalf("unexpected source ordering: %#v", got)
	}
}

func TestExtractRejectsTooManyExplicitTermsAndSecretLikeValues(t *testing.T) {
	if _, err := Extract(Input{Explicit: []string{"one", "two", "three", "four"}}); err == nil {
		t.Fatal("expected too many explicit terms to fail")
	}

	got, err := Extract(Input{
		Explicit: []string{"release", "0123456789abcdef0123456789abcdef"},
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(got) != 1 || got[0].Term != "release" {
		t.Fatalf("secret-like token was not excluded: %#v", got)
	}
}
