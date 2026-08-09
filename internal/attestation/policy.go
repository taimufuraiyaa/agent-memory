package attestation

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

const RenewalPeriod = 30 * 24 * time.Hour

type RightsBasis string

const (
	RightsAuthorOwned                RightsBasis = "author_owned"
	RightsLicensed                   RightsBasis = "licensed"
	RightsPublicDomain               RightsBasis = "public_domain"
	RightsLawfullyAcquiredPrivateUse RightsBasis = "lawfully_acquired_private_use"
)

func ValidRightsBasis(value string) bool {
	switch RightsBasis(strings.TrimSpace(value)) {
	case RightsAuthorOwned, RightsLicensed, RightsPublicDomain, RightsLawfullyAcquiredPrivateUse:
		return true
	default:
		return false
	}
}

type Statement struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type Policy struct {
	Version             string      `json:"version"`
	EffectiveAt         time.Time   `json:"effective_at"`
	RenewalDays         int         `json:"renewal_days"`
	PrimaryConfirmation string      `json:"primary_confirmation"`
	Statements          []Statement `json:"statements"`
	StatementDigest     string      `json:"statement_digest"`
}

func CurrentPolicy() Policy {
	policy := Policy{
		Version:             "rights-attestation-v1",
		EffectiveAt:         time.Date(2026, time.August, 5, 0, 0, 0, 0, time.UTC),
		RenewalDays:         30,
		PrimaryConfirmation: "By continuing, you represent that you own this material or have sufficient rights or lawful authorization to upload, store, index, and privately process it.",
		Statements: []Statement{
			{ID: "sufficient_rights", Text: "I have sufficient rights or lawful authorization to upload, store, index, and privately process the materials I provide."},
			{ID: "copy_not_copyright", Text: "I understand that purchasing or possessing a copy does not necessarily mean I own its copyright."},
			{ID: "lawfully_obtained", Text: "I will not upload unlawfully obtained material."},
			{ID: "no_circumvention", Text: "I have not circumvented DRM or other access protections to produce the uploaded file."},
			{ID: "private_processing", Text: "I authorize private technical copies, indexes, embeddings, summaries, and memories solely to provide the service."},
			{ID: "removal", Text: "I understand that material may be restricted or deleted following a valid legal notice or violation of the Terms."},
			{ID: "responsibility", Text: "I accept responsibility for the materials I upload, subject to applicable law."},
		},
	}
	policy.StatementDigest = policyDigest(policy)
	return policy
}

func policyDigest(policy Policy) string {
	parts := []string{policy.Version, policy.PrimaryConfirmation}
	for _, statement := range policy.Statements {
		parts = append(parts, statement.ID, statement.Text)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
