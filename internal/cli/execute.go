package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// Execute runs root command and returns deterministic exit code.
func Execute() int {
	cmd := NewRootCommand()
	if err := cmd.Execute(); err != nil {
		code := mapExitCode(err)
		if currentOutputFormat() == formatJSON {
			_ = writeErrorEnvelope(os.Stdout, commandFromArgs(os.Args[1:]), err)
		} else {
			fmt.Fprintln(os.Stderr, err.Error())
		}
		return code
	}
	return 0
}

// mapExitCode maps typed sentinel errors to deterministic exit codes.
// Falls back to string matching for cobra usage errors (which don't wrap sentinels).
func mapExitCode(err error) int {
	// cobra usage errors (e.g., "required flag", "unknown command") → code 2
	if isUsageError(err) {
		return 2
	}
	switch {
	case errors.Is(err, core.ErrInvalidInput):
		return 3
	case errors.Is(err, core.ErrNotFound):
		return 4
	case errors.Is(err, core.ErrAlreadyExists):
		return 5
	default:
		return 1
	}
}

// isUsageError detects cobra-level usage errors via string matching since cobra
// errors are plain strings, not typed sentinels.
func isUsageError(err error) bool {
	msg := err.Error()
	switch {
	case containsFold(msg, "required flag"),
		containsFold(msg, "unknown command"),
		containsFold(msg, "flag needs an argument"),
		containsFold(msg, "unknown flag"):
		return true
	}
	return false
}

func containsFold(s, substr string) bool {
	return len(s) >= len(substr) && containsLower(s, substr)
}

func containsLower(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			sc := s[i+j]
			if sc >= 'A' && sc <= 'Z' {
				sc += 32
			}
			cc := substr[j]
			if cc >= 'A' && cc <= 'Z' {
				cc += 32
			}
			if sc != cc {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func exitCodeName(code int) string {
	switch code {
	case 2:
		return "USAGE_ERROR"
	case 3:
		return "VALIDATION_ERROR"
	case 4:
		return "NOT_FOUND"
	case 5:
		return "CONFLICT"
	default:
		return "INTERNAL_ERROR"
	}
}
