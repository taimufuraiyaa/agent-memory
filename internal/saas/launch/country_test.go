package launch

import (
	"strconv"
	"testing"
	"time"
)

func TestCountryVerifierRejectsSpoofedAndExpiredAssertions(t *testing.T) {
	now := time.Date(2026, 8, 5, 16, 0, 0, 0, time.UTC)
	verifier := NewCountryVerifier("country-signing-secret-with-32-characters", func() time.Time { return now })
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := verifier.Sign("VN", timestamp)
	if !verifier.Verify("VN", timestamp, signature) {
		t.Fatal("valid edge assertion was rejected")
	}
	if verifier.Verify("US", timestamp, signature) || verifier.Verify("VN", timestamp, signature[:len(signature)-2]+"00") {
		t.Fatal("tampered country assertion was accepted")
	}
	expired := strconv.FormatInt(now.Add(-6*time.Minute).Unix(), 10)
	if verifier.Verify("VN", expired, verifier.Sign("VN", expired)) {
		t.Fatal("expired country assertion was accepted")
	}
}
