package cli

import (
	"errors"
	"os"
	"strings"

	amconfig "github.com/taimufuraiyaa/agent-memory/internal/config"
)

func upsertEnvFile(path string, vars map[string]string) (bool, error) {
	existing := ""
	if b, err := os.ReadFile(path); err == nil {
		existing = strings.ReplaceAll(string(b), "\r\n", "\n")
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	var sanitized bool
	existing, sanitized = sanitizeLegacyEnvShellBlock(existing)

	lines := []string{}
	if existing != "" {
		lines = strings.Split(existing, "\n")
	}

	index := map[string]int{}
	for i, ln := range lines {
		k, _, ok := parseEnvAssignmentLine(ln)
		if !ok {
			continue
		}
		if _, exists := index[k]; !exists {
			index[k] = i
		}
	}

	changed := sanitized
	for k, v := range vars {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		newLine := formatEnvAssignmentLine(k, v)
		if at, ok := index[k]; ok {
			if strings.TrimSpace(lines[at]) != strings.TrimSpace(newLine) {
				lines[at] = newLine
				changed = true
			}
			continue
		}
		lines = append(lines, newLine)
		index[k] = len(lines) - 1
		changed = true
	}

	out := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	out = amconfig.EnsureTermBloomEnvGuidance(out)
	if out == "" {
		out = ""
	}
	if !changed && existing != "" && out == existing {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func sanitizeLegacyEnvShellBlock(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	legacy := []string{
		"# Put the agent-memory binary on PATH",
		`case ":$PATH:" in`,
		`*":$HOME/.local/bin:"*) ;;`,
		`*) export PATH="$HOME/.local/bin:$PATH" ;;`,
		"esac",
	}
	for i := 0; i+len(legacy) <= len(lines); i++ {
		matched := true
		for j, want := range legacy {
			if strings.TrimSpace(lines[i+j]) != want {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		lines = append(lines[:i], lines[i+len(legacy):]...)
		return strings.Join(lines, "\n"), true
	}
	return content, false
}

func ensureAdaptiveTuningGuidance(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	existing := strings.ReplaceAll(string(b), "\r\n", "\n")
	updated := amconfig.EnsureAdaptiveTuningEnvGuidance(existing)
	if updated == existing {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func ensureTermBloomGuidance(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	existing := strings.ReplaceAll(string(b), "\r\n", "\n")
	updated := amconfig.EnsureTermBloomEnvGuidance(existing)
	if updated == existing {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
