package cli

import (
	"fmt"
	"os"
	"strings"
)

// Execute runs root command and returns deterministic exit code.
func Execute() int {
	cmd := NewRootCommand()
	if err := cmd.Execute(); err != nil {
		msg := err.Error()
		code := mapExitCode(msg)
		if currentOutputFormat() == formatJSON {
			_ = writeErrorEnvelope(os.Stdout, commandFromArgs(os.Args[1:]), msg)
		} else {
			fmt.Fprintln(os.Stderr, msg)
		}
		return code
	}
	return 0
}

func mapExitCode(msg string) int {
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "required flag"), strings.Contains(l, "unknown command"), strings.Contains(l, "flag needs an argument"):
		return 2 // usage
	case strings.Contains(l, "validation"), strings.Contains(l, "invalid"):
		return 3 // validation
	case strings.Contains(l, "not found"):
		return 4
	case strings.Contains(l, "conflict"), strings.Contains(l, "duplicate"):
		return 5
	default:
		return 1
	}
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
