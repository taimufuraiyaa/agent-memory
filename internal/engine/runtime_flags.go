package engine

import (
	"math"
	"os"
	"strconv"
	"strings"
)

type TermBloomRuntimeMode string

const (
	TermBloomOff    TermBloomRuntimeMode = "off"
	TermBloomShadow TermBloomRuntimeMode = "shadow"
	TermBloomGate   TermBloomRuntimeMode = "gate"
)

// TermBloomMode returns the requested rollout mode. Shadow is the safe default.
func TermBloomMode() TermBloomRuntimeMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_MEMORY_TERM_BLOOM_MODE"))) {
	case "off", "disabled", "0", "false":
		return TermBloomOff
	case "gate":
		return TermBloomGate
	default:
		return TermBloomShadow
	}
}

func MemoryEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_MEMORY_ENABLED")))
	if v == "" {
		return true
	}
	switch v {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return true
	}
}

// LibraryEnabled gates the book-library HTTP surface. Release-readiness drills
// have passed, so it defaults on while retaining an explicit emergency off switch.
func LibraryEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_MEMORY_LIBRARY_ENABLED")))
	switch v {
	case "0", "false", "no", "n", "off":
		return false
	default:
		return true
	}
}

func RunLabel() string {
	return strings.TrimSpace(os.Getenv("AGENT_MEMORY_RUN_LABEL"))
}

// LexicalRuntimeMode selects the BM25 lexical candidate stream.
type LexicalRuntimeMode string

const (
	LexicalOff LexicalRuntimeMode = "off"
	LexicalOn  LexicalRuntimeMode = "on"

	// DefaultLexicalWeight is the activation weight of the relative BM25
	// signal when the stream is on.
	DefaultLexicalWeight = 0.25
)

// LexicalMode returns the requested lexical stream mode. Off is the default so
// existing retrieval results are unchanged until the stream is enabled.
func LexicalMode() LexicalRuntimeMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AGENT_MEMORY_LEXICAL_MODE"))) {
	case "on", "1", "true", "yes", "enabled":
		return LexicalOn
	default:
		return LexicalOff
	}
}

// LexicalWeight returns the lexical signal weight, clamped to [0, 1]. Zero
// keeps the stream candidate-only without influencing ranking.
func LexicalWeight() float64 {
	raw := strings.TrimSpace(os.Getenv("AGENT_MEMORY_LEXICAL_WEIGHT"))
	if raw == "" {
		return DefaultLexicalWeight
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) {
		return DefaultLexicalWeight
	}
	return math.Min(1, math.Max(0, value))
}
