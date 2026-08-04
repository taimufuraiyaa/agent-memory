package engine

import (
	"regexp"
	"strings"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

var privateTagRE = regexp.MustCompile(`(?is)<private>.*?</private>`)

// SecretPatterns is the canonical, single-owner set of patterns that
// RedactPrivateAndSecrets and RegexSecurityFilter use to detect and redact
// secrets. Do not duplicate these — import and reference this slice instead.
var SecretPatterns = []*regexp.Regexp{
	// AWS access key IDs.
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),

	// PEM-encoded private keys.
	regexp.MustCompile(`(?i)-----BEGIN (RSA|EC|OPENSSH|PRIVATE) KEY-----`),

	// password/passwd/pwd with quoted or unquoted values.
	// Captures single-quoted, double-quoted, and bare-word values.
	regexp.MustCompile(`(?i)\b(password|passwd|pwd)\s*[:=]\s*(?:'[^']*'|"[^"]*"|[^\s]+)`),

	// secret/api_key/token with a value of at least 16 characters.
	regexp.MustCompile(`(?i)\b(secret|api[_-]?key|token)\s*[:=]\s*(?:'[^']*'|"[^"]*"|[A-Za-z0-9_\-/.+]{16,})`),

	// Bearer tokens.
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._\-+/=]{20,}`),

	// Anthropic API keys.
	regexp.MustCompile(`sk-ant-[A-Za-z0-9\-_]{20,}`),

	// OpenAI project keys.
	regexp.MustCompile(`sk-proj-[A-Za-z0-9\-_]{20,}`),

	// Generic OpenAI-style keys.
	regexp.MustCompile(`\bsk-[A-Za-z0-9\-_]{20,}`),

	// GitHub personal access tokens.
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),

	// GitHub fine-grained tokens.
	regexp.MustCompile(`gh[pus]_[A-Za-z0-9]{36,}`),

	// Slack bot tokens.
	regexp.MustCompile(`xoxb-[A-Za-z0-9\-]+`),

	// Google API keys.
	regexp.MustCompile(`AIza[A-Za-z0-9\-_]{35}`),

	// JWT tokens (three base64url segments separated by dots).
	regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`),

	// npm access tokens.
	regexp.MustCompile(`npm_[A-Za-z0-9]{36}`),

	// GitLab personal access tokens.
	regexp.MustCompile(`glpat-[A-Za-z0-9\-_]{20,}`),

	// DigitalOcean personal access tokens.
	regexp.MustCompile(`dop_v1_[A-Za-z0-9]{64}`),
}

// PIISecretPatterns are patterns that detect PII for use by the security
// filter's redaction/validation. These are kept separate from SecretPatterns
// so that callers can choose whether to redact PII vs. just detect it.
var PIISecretPatterns = []*regexp.Regexp{
	// Email addresses.
	regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`),

	// Phone numbers that include at least one separator (dash, space, paren)
	// OR are preceded by a phone/tel context keyword. Bare 10-digit runs
	// without separators or context are NOT matched (avoids false-positives
	// on order IDs, timestamps like 2026031512, etc.).
	regexp.MustCompile(`(?i)(?:\b(?:phone|tel)[:\s]*\d{10,}\b|(?:\+?1[\s\-]?)?(?:\(\d{3}\)[\s\-]?|\d{3}[\s\-])\d{3}[\s\-]\d{4}\b)`),

	// US SSN.
	regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
}

func RedactPrivateAndSecrets(input string) string {
	s := input
	if s == "" {
		return s
	}
	s = privateTagRE.ReplaceAllString(s, "[REDACTED]")
	for _, re := range SecretPatterns {
		s = re.ReplaceAllString(s, "[REDACTED_SECRET]")
	}
	return s
}

// RedactSecretsAndPII redacts both secret patterns and PII patterns. Use this
// when you want to strip PII in addition to secrets before storing content.
func RedactSecretsAndPII(input string) string {
	s := RedactPrivateAndSecrets(input)
	for _, re := range PIISecretPatterns {
		s = re.ReplaceAllString(s, "[REDACTED_PII]")
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
	out := core.TruncateUTF8(s, max)
	out = strings.TrimRight(out, " \n\r\t")
	return out
}
