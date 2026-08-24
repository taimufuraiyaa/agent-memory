package hooks

import (
	"os"
	"strconv"
	"strings"
)

type Policy struct {
	CaptureEnabled          bool `json:"capture_enabled"`
	SessionInjectionEnabled bool `json:"session_injection_enabled"`
	PromptInjectionEnabled  bool `json:"prompt_injection_enabled"`
	InjectionBudget         int  `json:"injection_budget"`
}

func ResolvePolicy() Policy {
	return Policy{
		CaptureEnabled:          envBool("AGENT_MEMORY_CAPTURE_ENABLED", true),
		SessionInjectionEnabled: envBool("AGENT_MEMORY_SESSION_INJECTION_ENABLED", false),
		PromptInjectionEnabled:  envBool("AGENT_MEMORY_PROMPT_INJECTION_ENABLED", false),
		InjectionBudget:         envInt("AGENT_MEMORY_INJECTION_BUDGET", 800, 1, 32000),
	}
}

func envBool(name string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envInt(name string, fallback, minimum, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}
