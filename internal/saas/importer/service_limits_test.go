package importer

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
)

func TestValidateBundleRejectsExcessiveItems(t *testing.T) {
	bundle := exportservice.Bundle{Memories: make([]map[string]any, MaxImportItems+1)}
	if _, err := validateBundle(bundle); err == nil || !strings.Contains(err.Error(), "item limit") {
		t.Fatalf("expected item-limit rejection, got %v", err)
	}
}

func TestValidateBundleDecodesSourceBytesOnceForPublication(t *testing.T) {
	body := []byte("bounded source")
	sum := sha256.Sum256(body)
	bundle := exportservice.Bundle{
		Sources: []map[string]any{{"id": "source-1"}},
		SourceObjects: []exportservice.SourceObject{{
			SourceID: "source-1", Filename: "source.txt", MediaType: "text/plain",
			SizeBytes: int64(len(body)), ChecksumSHA256: hex.EncodeToString(sum[:]),
			BytesBase64: base64.StdEncoding.EncodeToString(body),
		}},
	}
	decoded, err := validateBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded["source-1"]) != string(body) {
		t.Fatalf("decoded source mismatch: %q", decoded["source-1"])
	}
}

func TestTenantImportLockKeySerializesPerTenant(t *testing.T) {
	if tenantImportLockKey("tenant-a") != tenantImportLockKey("tenant-a") {
		t.Fatal("same tenant did not share an import lock")
	}
	if tenantImportLockKey("tenant-a") == tenantImportLockKey("tenant-b") {
		t.Fatal("different tenants unexpectedly share an import lock")
	}
}
