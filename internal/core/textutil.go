package core

import "unicode/utf8"

// TruncateUTF8 truncates s to at most maxBytes bytes without ever splitting a
// UTF-8 rune, so the result is always valid UTF-8 when s is valid UTF-8.
//
// Semantics:
//   - maxBytes <= 0 → "" (empty string)
//   - len(s) <= maxBytes → s (returned unchanged)
//   - otherwise → the longest prefix of s whose byte length is <= maxBytes
//     and which ends on a rune boundary
//
// If the source itself contains malformed bytes, the result is walked back to
// the nearest valid UTF-8 prefix so the helper never emits broken output.
func TruncateUTF8(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	// Walk back from the cut point to the start of the rune it lands in.
	end := maxBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	// Defensive guard for malformed input: a slice ending on a rune start can
	// still be invalid UTF-8 when the source was already broken.
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end]
}
