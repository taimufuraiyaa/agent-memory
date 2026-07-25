package config

import "strings"

const termBloomEnvGuidanceHeader = "# agent-memory exact term Bloom filter (managed)"

func TermBloomEnvGuidanceHeader() string {
	return termBloomEnvGuidanceHeader
}

func TermBloomEnvGuidanceBlock() string {
	return strings.Join([]string{
		termBloomEnvGuidanceHeader,
		"# shadow is the safe install default; gate enables definite-miss short-circuiting.",
		"# Set off for immediate fail-open rollback to canonical exact-term lookup.",
		"# Inspect project health with: agent-memory reindex-terms --status",
	}, "\n") + "\n"
}

func EnsureTermBloomEnvGuidance(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if strings.Contains(content, termBloomEnvGuidanceHeader) {
		return content
	}
	out := strings.TrimRight(content, "\n")
	if strings.TrimSpace(out) != "" {
		out += "\n\n"
	}
	return out + TermBloomEnvGuidanceBlock()
}
