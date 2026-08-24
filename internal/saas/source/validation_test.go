package source

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestValidateContentChecksSizeChecksumSignatureTextAndMalware(t *testing.T) {
	tests := []struct {
		name, media    string
		value          []byte
		sizeDelta      int64
		checksum, want string
	}{
		{"pdf", "application/pdf", []byte("%PDF-1.7\nbody"), 0, "", ""},
		{"epub", "application/epub+zip", []byte{'P', 'K', 3, 4, 'x'}, 0, "", ""},
		{"markdown", "text/markdown", []byte("# valid"), 0, "", ""},
		{"size", "text/plain", []byte("text"), 1, "", "size_mismatch"},
		{"checksum", "text/plain", []byte("text"), 0, "bad", "checksum_mismatch"},
		{"signature", "application/pdf", []byte("plain"), 0, "", "signature_mismatch"},
		{"utf8", "text/plain", []byte{0xff}, 0, "", "text_invalid"},
		{"malware", "text/plain", []byte("EICAR-STANDARD-ANTIVIRUS-TEST-FILE"), 0, "", "malware_detected"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sum := sha256.Sum256(test.value)
			checksum := fmt.Sprintf("%x", sum)
			if test.checksum != "" {
				checksum = test.checksum
			}
			claim := ValidationClaim{UploadClaim: UploadClaim{MediaType: test.media, ExpectedSize: int64(len(test.value)) + test.sizeDelta, Checksum: checksum}}
			if got := validateContent(claim, test.value); got != test.want {
				t.Fatalf("validateContent()=%q want=%q", got, test.want)
			}
		})
	}
}

type reconcileRepo struct{}

func (reconcileRepo) ActiveTenantIDs(context.Context) ([]string, error) {
	return []string{"tenant"}, nil
}
func (reconcileRepo) VaultReferences(context.Context, string) (map[string]int, error) {
	return map[string]int{"vault/tenant/missing": 1, "vault/tenant/shared": 2}, nil
}

type reconcileObjects struct{}

func (reconcileObjects) ListVault(context.Context, string) ([]string, error) {
	return []string{"vault/tenant/orphan", "vault/tenant/shared"}, nil
}
func TestVaultReconcilerDetectsOrphanMissingAndMultipleReferences(t *testing.T) {
	findings, err := NewVaultReconciler(reconcileRepo{}, reconcileObjects{}).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, finding := range findings {
		kinds[finding.Kind] = true
	}
	for _, kind := range []string{"orphan", "missing", "multiply_referenced"} {
		if !kinds[kind] {
			t.Fatalf("missing %s finding: %+v", kind, findings)
		}
	}
}
