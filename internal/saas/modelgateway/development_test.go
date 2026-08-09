package modelgateway

import (
	"context"
	"strings"
	"testing"
)

func TestDevelopmentProviderIsDeterministicAndContentRedactorRemovesSecrets(t *testing.T) {
	provider := DevelopmentProvider{}
	first, err := provider.Embed(context.Background(), "deterministic source text")
	if err != nil {
		t.Fatal(err)
	}
	second, err := provider.Embed(context.Background(), "deterministic source text")
	if err != nil || len(first) != DevelopmentDimensions || len(second) != DevelopmentDimensions {
		t.Fatalf("dimensions=%d/%d err=%v", len(first), len(second), err)
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("vector differs at %d", index)
		}
	}
	redacted := (ContentRedactor{}).Redact("password=private-value Bearer abcdefghijklmnop")
	if strings.Contains(redacted, "private-value") || strings.Contains(redacted, "abcdefghijklmnop") {
		t.Fatalf("secret survived redaction: %q", redacted)
	}
}
