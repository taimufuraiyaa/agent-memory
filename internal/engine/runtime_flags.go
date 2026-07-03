package engine

import (
	"os"
	"strings"
)

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

func RunLabel() string {
	return strings.TrimSpace(os.Getenv("AGENT_MEMORY_RUN_LABEL"))
}
