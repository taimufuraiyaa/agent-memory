package tui

import (
	"strings"
	"testing"
)

func TestSanitizeTerminalTextRemovesControlSequences(t *testing.T) {
	input := "safe\x1b[2Jhidden\x1b]8;;https://example.com\aopen\x1b]8;;\a\rreplace\x00end"
	got := sanitizeTerminalText(input, true)
	for _, forbidden := range []string{"\x1b", "\a", "\r", "\x00", "https://example.com"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("sanitized output contains %q: %q", forbidden, got)
		}
	}
	for _, want := range []string{"safe", "hidden", "open", "replace", "end"} {
		if !strings.Contains(got, want) {
			t.Fatalf("sanitized output lost %q: %q", want, got)
		}
	}
}

func TestSanitizeTerminalTextPreservesAllowedWhitespace(t *testing.T) {
	if got := sanitizeTerminalText("first\nsecond\tvalue", true); got != "first\nsecond\tvalue" {
		t.Fatalf("multiline sanitization=%q", got)
	}
	if got := sanitizeTerminalText("first\nsecond\tvalue", false); got != "first second value" {
		t.Fatalf("single-line sanitization=%q", got)
	}
}
