package readiness

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	ApprovalArtifactSchema = "agent-memory-release-approval-v1"
	ApprovalTrustSchema    = "agent-memory-approver-trust-v1"
)

var (
	approvalNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
	evidenceRefPattern  = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://[^\s\x00-\x1f]{1,240}$`)
)

type SignedApproval struct {
	Schema         string `json:"schema"`
	Gate           string `json:"gate"`
	Control        string `json:"control"`
	Decision       string `json:"decision"`
	Owner          string `json:"owner"`
	KeyID          string `json:"key_id"`
	EvidenceRef    string `json:"evidence_ref"`
	EvidenceSHA256 string `json:"evidence_sha256"`
	IssuedAt       string `json:"issued_at"`
	ExpiresAt      string `json:"expires_at"`
	Signature      string `json:"signature"`
}

type TrustedApprover struct {
	KeyID     string   `json:"key_id"`
	Owner     string   `json:"owner"`
	PublicKey string   `json:"public_key"`
	Gates     []string `json:"gates"`
	Controls  []string `json:"controls"`
}

type TrustBundle struct {
	Schema string            `json:"schema"`
	Keys   []TrustedApprover `json:"keys"`
}

type VerifiedApproval struct {
	Owner          string `json:"owner"`
	KeyID          string `json:"key_id"`
	EvidenceRef    string `json:"evidence_ref"`
	EvidenceSHA256 string `json:"evidence_sha256"`
	IssuedAt       string `json:"issued_at"`
	ExpiresAt      string `json:"expires_at"`
}

type ApprovalReport struct {
	Ready    bool                        `json:"ready"`
	Verified map[string]VerifiedApproval `json:"verified"`
	Missing  []string                    `json:"missing"`
	Rejected []string                    `json:"rejected"`
	Expired  []string                    `json:"expired"`
}

// ValidateTrustBundle verifies the schema, key material, identities, and
// authorization scopes of a separately loaded trust bundle.
func ValidateTrustBundle(bundle TrustBundle) error {
	_, err := validateTrustBundle(bundle)
	return err
}

type approvalPayload struct {
	Schema         string `json:"schema"`
	Gate           string `json:"gate"`
	Control        string `json:"control"`
	Decision       string `json:"decision"`
	Owner          string `json:"owner"`
	KeyID          string `json:"key_id"`
	EvidenceRef    string `json:"evidence_ref"`
	EvidenceSHA256 string `json:"evidence_sha256"`
	IssuedAt       string `json:"issued_at"`
	ExpiresAt      string `json:"expires_at"`
}

func CanonicalApprovalPayload(value SignedApproval) ([]byte, error) {
	if value.Schema != ApprovalArtifactSchema || !approvalNamePattern.MatchString(value.Gate) || !approvalNamePattern.MatchString(value.Control) || !approvalNamePattern.MatchString(value.Owner) || !approvalNamePattern.MatchString(value.KeyID) {
		return nil, errors.New("approval identity fields are invalid")
	}
	if value.Decision != "approved" && value.Decision != "rejected" {
		return nil, errors.New("approval decision is invalid")
	}
	if !evidenceRefPattern.MatchString(value.EvidenceRef) || !validSHA256(value.EvidenceSHA256) {
		return nil, errors.New("approval evidence reference or digest is invalid")
	}
	issued, issuedErr := time.Parse(time.RFC3339Nano, value.IssuedAt)
	expires, expiresErr := time.Parse(time.RFC3339Nano, value.ExpiresAt)
	if issuedErr != nil || expiresErr != nil || !expires.After(issued) {
		return nil, errors.New("approval validity window is invalid")
	}
	payload := approvalPayload{
		Schema: value.Schema, Gate: value.Gate, Control: value.Control, Decision: value.Decision,
		Owner: value.Owner, KeyID: value.KeyID, EvidenceRef: value.EvidenceRef,
		EvidenceSHA256: value.EvidenceSHA256, IssuedAt: value.IssuedAt, ExpiresAt: value.ExpiresAt,
	}
	return json.Marshal(payload)
}

func VerifyApprovals(gate string, required []string, bundle TrustBundle, approvals []SignedApproval, now time.Time) (ApprovalReport, error) {
	report := ApprovalReport{Verified: map[string]VerifiedApproval{}, Missing: []string{}, Rejected: []string{}, Expired: []string{}}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	requiredSet, err := requiredApprovalSet(gate, required)
	if err != nil {
		return report, err
	}
	keys, err := validateTrustBundle(bundle)
	if err != nil {
		return report, err
	}
	type decision struct {
		approval SignedApproval
		issued   time.Time
		expires  time.Time
	}
	latest := map[string]decision{}
	for _, approval := range approvals {
		if approval.Gate != gate {
			return report, errors.New("approval artifact targets the wrong release gate")
		}
		if _, ok := requiredSet[approval.Control]; !ok {
			return report, errors.New("approval artifact targets an unknown control")
		}
		payload, err := CanonicalApprovalPayload(approval)
		if err != nil {
			return report, err
		}
		trusted, ok := keys[approval.KeyID]
		if !ok || trusted.config.Owner != approval.Owner || !contains(trusted.config.Gates, gate) || !contains(trusted.config.Controls, approval.Control) {
			return report, errors.New("approval signer is not authorized for the gate and control")
		}
		signature, err := base64.StdEncoding.DecodeString(approval.Signature)
		if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(trusted.key, payload, signature) {
			return report, errors.New("approval signature is invalid")
		}
		issued, _ := time.Parse(time.RFC3339Nano, approval.IssuedAt)
		expires, _ := time.Parse(time.RFC3339Nano, approval.ExpiresAt)
		if issued.After(now.Add(5 * time.Minute)) {
			return report, errors.New("approval issue time is in the future")
		}
		if current, exists := latest[approval.Control]; exists {
			if issued.Equal(current.issued) {
				return report, errors.New("approval decisions have an ambiguous issue time")
			}
			if issued.Before(current.issued) {
				continue
			}
		}
		latest[approval.Control] = decision{approval: approval, issued: issued, expires: expires}
	}
	for _, control := range sortedKeys(requiredSet) {
		value, ok := latest[control]
		switch {
		case !ok:
			report.Missing = append(report.Missing, control)
		case !value.expires.After(now):
			report.Expired = append(report.Expired, control)
		case value.approval.Decision == "rejected":
			report.Rejected = append(report.Rejected, control)
		default:
			report.Verified[control] = VerifiedApproval{
				Owner: value.approval.Owner, KeyID: value.approval.KeyID, EvidenceRef: value.approval.EvidenceRef,
				EvidenceSHA256: value.approval.EvidenceSHA256, IssuedAt: value.approval.IssuedAt, ExpiresAt: value.approval.ExpiresAt,
			}
		}
	}
	report.Ready = len(report.Verified) == len(requiredSet) && len(report.Missing) == 0 && len(report.Rejected) == 0 && len(report.Expired) == 0
	return report, nil
}

type decodedApprover struct {
	config TrustedApprover
	key    ed25519.PublicKey
}

func validateTrustBundle(bundle TrustBundle) (map[string]decodedApprover, error) {
	if bundle.Schema != ApprovalTrustSchema || len(bundle.Keys) == 0 {
		return nil, errors.New("approval trust bundle is invalid")
	}
	result := make(map[string]decodedApprover, len(bundle.Keys))
	for _, value := range bundle.Keys {
		if !approvalNamePattern.MatchString(value.KeyID) || !approvalNamePattern.MatchString(value.Owner) || len(value.Gates) == 0 || len(value.Controls) == 0 {
			return nil, errors.New("trusted approver scope is invalid")
		}
		if _, duplicate := result[value.KeyID]; duplicate {
			return nil, errors.New("trusted approver key ID is duplicated")
		}
		for _, gate := range value.Gates {
			if !approvalNamePattern.MatchString(gate) {
				return nil, errors.New("trusted approver gate is invalid")
			}
		}
		for _, control := range value.Controls {
			if !approvalNamePattern.MatchString(control) {
				return nil, errors.New("trusted approver control is invalid")
			}
		}
		decoded, err := base64.StdEncoding.DecodeString(value.PublicKey)
		if err != nil || len(decoded) != ed25519.PublicKeySize {
			return nil, errors.New("trusted approver public key is invalid")
		}
		result[value.KeyID] = decodedApprover{config: value, key: ed25519.PublicKey(decoded)}
	}
	return result, nil
}

func requiredApprovalSet(gate string, required []string) (map[string]struct{}, error) {
	if !approvalNamePattern.MatchString(gate) || len(required) == 0 {
		return nil, errors.New("release gate approval requirements are invalid")
	}
	result := make(map[string]struct{}, len(required))
	for _, control := range required {
		if !approvalNamePattern.MatchString(control) {
			return nil, errors.New("release gate approval control is invalid")
		}
		if _, duplicate := result[control]; duplicate {
			return nil, fmt.Errorf("release gate approval control %q is duplicated", control)
		}
		result[control] = struct{}{}
	}
	return result, nil
}

func validSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
