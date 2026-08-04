package engine

import (
	"strings"
	"testing"
)

func TestRedactQuotedMultiWordSecret(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		wantRedacted  bool // true if [REDACTED_SECRET] should appear
		wantPreserved bool // true if the original secret should NOT appear
	}{
		{
			name:          "single-quoted multi-word secret",
			input:         "password: 'my super secret passphrase'",
			wantRedacted:  true,
			wantPreserved: true,
		},
		{
			name:          "double-quoted multi-word secret",
			input:         `password: "my super secret passphrase"`,
			wantRedacted:  true,
			wantPreserved: true,
		},
		{
			name:          "Bearer token quoted",
			input:         "Authorization: Bearer abc123def456ghi789jkl012mno345pqr",
			wantRedacted:  true,
			wantPreserved: true,
		},
		{
			name:          "API key with quoted value containing spaces",
			input:         `api_key: "sk-abc def ghi jkl mno pqr stu"`,
			wantRedacted:  true,
			wantPreserved: true,
		},
		{
			name:          "SK key bare",
			input:         "use key sk-12345678901234567890 for auth",
			wantRedacted:  true,
			wantPreserved: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RedactPrivateAndSecrets(tt.input)
			if tt.wantRedacted && !strings.Contains(result, "[REDACTED_SECRET]") {
				t.Errorf("expected [REDACTED_SECRET] in output, got: %s", result)
			}
			// The original quoted secret value should be gone.
			if tt.wantPreserved {
				if strings.Contains(result, "my super secret passphrase") {
					t.Errorf("expected multi-word secret to be redacted, got: %s", result)
				}
				if strings.Contains(result, "abc123def456") {
					t.Errorf("expected Bearer token to be redacted, got: %s", result)
				}
			}
		})
	}
}

func TestPhoneRegexSeparatorsRequired(t *testing.T) {
	// We test the PII patterns used by the security filter.
	phonePattern := PIISecretPatterns[1] // Phone pattern is index 1.

	shouldMatch := []string{
		"(555) 123-4567",
		"555-123-4567",
		"555 123 4567",
		"phone: 5551234567",
		"tel:5551234567",
		"+1 (555) 123-4567",
		"+1-555-123-4567",
	}

	shouldNotMatch := []string{
		"5551234567",              // bare 10-digit, no separator, no context
		"2026031512",              // looks like a timestamp
		"ORD1234567890",           // order number
		"123456789012",            // 12 digits
		"order number 5551234567", // no phone/tel context
	}

	for _, s := range shouldMatch {
		if !phonePattern.MatchString(s) {
			t.Errorf("expected phone pattern to match %q", s)
		}
	}
	for _, s := range shouldNotMatch {
		if phonePattern.MatchString(s) {
			t.Errorf("expected phone pattern to NOT match %q (false positive)", s)
		}
	}
}

func TestBare10DigitOrderNumberNotRedacted(t *testing.T) {
	// A bare 10-digit order number should NOT trigger the phone PII pattern.
	input := "Order #5551234567 has been shipped."
	result := RedactPrivateAndSecrets(input)
	if strings.Contains(result, "[REDACTED_PII]") {
		t.Errorf("bare 10-digit order number should not be redacted as PII, got: %s", result)
	}
	// But RedactSecretsAndPII only redacts PIISecretPatterns...
	// Actually RedactPrivateAndSecrets only uses SecretPatterns, not PIISecretPatterns.
	// The phone pattern is in PIISecretPatterns. So RedactPrivateAndSecrets wouldn't touch it anyway.
	// Let me test with the full redaction.
	resultFull := RedactSecretsAndPII(input)
	if strings.Contains(resultFull, "[REDACTED_PII]") {
		t.Errorf("bare 10-digit order number should not be redacted as PII, got: %s", resultFull)
	}
}

func TestEmailAndSSNInTextAreRedactedByFullRedaction(t *testing.T) {
	// Emails and SSNs should be redacted by RedactSecretsAndPII.
	input := "Contact user@example.com for details. SSN: 123-45-6789."
	result := RedactSecretsAndPII(input)
	if !strings.Contains(result, "[REDACTED_PII]") {
		t.Errorf("expected PII to be redacted, got: %s", result)
	}
	if strings.Contains(result, "user@example.com") {
		t.Errorf("expected email to be redacted, got: %s", result)
	}
	if strings.Contains(result, "123-45-6789") {
		t.Errorf("expected SSN to be redacted, got: %s", result)
	}
}

func TestRedactSecretsDoesNotAffectPII(t *testing.T) {
	// RedactPrivateAndSecrets should NOT redact PII (email, phone, SSN).
	input := "Contact user@example.com or call 555-123-4567. SSN: 123-45-6789 password: secret123"
	result := RedactPrivateAndSecrets(input)
	if strings.Contains(result, "[REDACTED_PII]") {
		t.Errorf("RedactPrivateAndSecrets should not redact PII, got: %s", result)
	}
	// password should be redacted as a secret.
	if !strings.Contains(result, "[REDACTED_SECRET]") {
		t.Errorf("expected secret to be redacted, got: %s", result)
	}
	// email should remain.
	if !strings.Contains(result, "user@example.com") {
		t.Errorf("expected email to remain after secret-only redaction, got: %s", result)
	}
}

func TestPromotionSummaryRedactsSecretsBeforeWrite(t *testing.T) {
	// Simulate the BuildPromotionText + full redaction path.
	// A summary line containing a quoted multi-word secret should be redacted.
	content := "Session observations: s1\n- 2024-01-15T00:00:00Z password: 'my secret passphrase'\n"
	result := RedactPrivateAndSecrets(content)
	if !strings.Contains(result, "[REDACTED_SECRET]") {
		t.Errorf("expected secret to be redacted in promotion summary, got: %s", result)
	}
	if strings.Contains(result, "my secret passphrase") {
		t.Errorf("expected quoted password value to be redacted, got: %s", result)
	}
}
