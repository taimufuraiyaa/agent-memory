package launch

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

type CountryVerifier struct {
	secret []byte
	now    func() time.Time
}

func NewCountryVerifier(secret string, now func() time.Time) *CountryVerifier {
	if now == nil {
		now = time.Now
	}
	return &CountryVerifier{secret: []byte(strings.TrimSpace(secret)), now: now}
}

func (v *CountryVerifier) Verify(country, timestamp, signature string) bool {
	if v == nil || len(v.secret) < 32 || len(country) != 2 {
		return false
	}
	unix, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return false
	}
	assertedAt := time.Unix(unix, 0)
	delta := v.now().UTC().Sub(assertedAt)
	if delta < -30*time.Second || delta > 5*time.Minute {
		return false
	}
	expected := v.Sign(strings.ToUpper(strings.TrimSpace(country)), timestamp)
	provided, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return false
	}
	want, _ := hex.DecodeString(expected)
	return hmac.Equal(provided, want)
}

// Sign is intended for the trusted edge adapter and deterministic tests.
func (v *CountryVerifier) Sign(country, timestamp string) string {
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write([]byte(strings.ToUpper(strings.TrimSpace(country)) + "\n" + strings.TrimSpace(timestamp)))
	return hex.EncodeToString(mac.Sum(nil))
}
