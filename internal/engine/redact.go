package engine

import (
	"regexp"
	"strings"
)

var privateTagRE = regexp.MustCompile(`(?is)<private>.*?</private>`)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`(?i)-----BEGIN (RSA|EC|OPENSSH|PRIVATE) KEY-----`),
	regexp.MustCompile(`(?i)\b(password|passwd|pwd)\s*[:=]\s*['"]?[^'"\s]+`),
	regexp.MustCompile(`(?i)\b(secret|api[_-]?key|token)\s*[:=]\s*['"]?[A-Za-z0-9_\-/.+]{16,}`),
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._\-+/=]{20,}`),
	regexp.MustCompile(`sk-ant-[A-Za-z0-9\-_]{20,}`),
	regexp.MustCompile(`sk-proj-[A-Za-z0-9\-_]{20,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9\-_]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),
	regexp.MustCompile(`gh[pus]_[A-Za-z0-9]{36,}`),
	regexp.MustCompile(`xoxb-[A-Za-z0-9\-]+`),
	regexp.MustCompile(`AIza[A-Za-z0-9\-_]{35}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`),
	regexp.MustCompile(`npm_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9\-_]{20,}`),
	regexp.MustCompile(`dop_v1_[A-Za-z0-9]{64}`),
}

func RedactPrivateAndSecrets(input string) string {
	s := input
	if s == "" {
		return s
	}
	s = privateTagRE.ReplaceAllString(s, "[REDACTED]")
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "[REDACTED_SECRET]")
	}
	return s
}

func ClipString(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	out := s[:max]
	out = strings.TrimRight(out, " \n\r\t")
	return out
}
