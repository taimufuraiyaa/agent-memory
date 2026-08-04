package core

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8EdgeCases(t *testing.T) {
	cases := []struct {
		name     string
		s        string
		maxBytes int
		want     string
	}{
		{"zero max", "hello", 0, ""},
		{"negative max", "hello", -5, ""},
		{"empty string", "", 10, ""},
		{"short string unchanged", "hi", 10, "hi"},
		{"exact fit", "hello", 5, "hello"},
		{"ascii cut", "hello world", 5, "hello"},
		{"cut before emoji", "a😀b", 1, "a"},
		{"emoji boundary", "😀😀😀", 4, "😀"},
		{"emoji at 5 bytes", "😀😀😀", 5, "😀"},
		{"emoji at 3 bytes", "😀😀😀", 3, ""},
		{"emoji at 8 bytes", "😀😀😀", 8, "😀😀"},
		{"cjk at 5 bytes", "你好世界", 5, "你"},
		{"cjk at 6 bytes", "你好世界", 6, "你好"},
		{"cjk at 11 bytes", "你好世界", 11, "你好世"},
		{"combining at 2 bytes", "e\u0301", 2, "e"},
		{"combining at 1 byte", "e\u0301", 1, "e"},
		{"combining exact", "e\u0301", 3, "e\u0301"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateUTF8(tc.s, tc.maxBytes)
			if got != tc.want {
				t.Fatalf("TruncateUTF8(%q, %d) = %q, want %q", tc.s, tc.maxBytes, got, tc.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("TruncateUTF8(%q, %d) emitted invalid UTF-8: %q", tc.s, tc.maxBytes, got)
			}
		})
	}
}

// TestTruncateUTF8EveryBoundary truncates multi-byte strings at every byte
// boundary and asserts the result is always valid UTF-8, never longer than the
// limit, and always a prefix of the source.
func TestTruncateUTF8EveryBoundary(t *testing.T) {
	samples := []string{
		"hello world",
		"😀😀😀",
		"你好世界",
		"e\u0301\u0301\u0301", // combining-accent-heavy
		"a😀b你c世d\u0301e",
		"🚀 Go 1.26 release 🎉",
	}
	for _, s := range samples {
		t.Run(s, func(t *testing.T) {
			for i := 0; i <= len(s); i++ {
				got := TruncateUTF8(s, i)
				if !utf8.ValidString(got) {
					t.Fatalf("boundary %d: invalid UTF-8 output %q", i, got)
				}
				if len(got) > i {
					t.Fatalf("boundary %d: len(%q)=%d exceeds maxBytes %d", i, got, len(got), i)
				}
				if !strings.HasPrefix(s, got) {
					t.Fatalf("boundary %d: %q is not a prefix of %q", i, got, s)
				}
				if i >= len(s) && got != s {
					t.Fatalf("boundary %d: expected unchanged input, got %q", i, got)
				}
			}
		})
	}
}

// TestTruncateUTF8NeverEmitsInvalidUTF8ForMalformedInput verifies the defensive
// walk-back: even when the source contains malformed bytes, the truncation path
// never emits invalid UTF-8.
func TestTruncateUTF8NeverEmitsInvalidUTF8ForMalformedInput(t *testing.T) {
	cases := []struct {
		s        string
		maxBytes int
	}{
		{"a\xff\xfeb", 3},
		{"\xff\xfe\xff\xfe", 3},
		{"ok\xc3\x28", 3}, // truncated multi-byte sequence at the boundary
		{"\xe4\xb8\xad\xffx", 4},
	}
	for _, tc := range cases {
		got := TruncateUTF8(tc.s, tc.maxBytes)
		if !utf8.ValidString(got) {
			t.Fatalf("TruncateUTF8(%q, %d) emitted invalid UTF-8: %q", tc.s, tc.maxBytes, got)
		}
	}
}
