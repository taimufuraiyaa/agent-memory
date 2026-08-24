package engine

import (
	"os"
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
