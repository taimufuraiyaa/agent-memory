// Package cliutil contains small dependency-free helpers shared by command
// packages.
package cliutil

import "strings"

// AnyBlank reports whether any value is empty or contains only whitespace.
func AnyBlank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}
