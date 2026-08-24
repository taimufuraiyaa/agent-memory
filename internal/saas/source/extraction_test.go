package source

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
)

func TestVaultPromotionCannotOverwriteImmutableVersionObject(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TEST_OBJECT_ENDPOINT"))
	if endpoint == "" {
		t.Skip("AGENT_MEMORY_TEST_OBJECT_ENDPOINT is not configured")
	}
	store, err := NewMinIOQuarantine(endpoint, os.Getenv("AGENT_MEMORY_TEST_OBJECT_ACCESS_KEY"), os.Getenv("AGENT_MEMORY_TEST_OBJECT_SECRET_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	key := "vault/00000000-0000-0000-0000-000000000001/immutable-" + uuid.NewString() + "/1.aesgcm"
	if err := store.PutVault(context.Background(), key, []byte("first ciphertext")); err != nil {
		t.Fatal(err)
	}
	defer store.DeleteVault(context.Background(), key)
	if err := store.PutVault(context.Background(), key, []byte("replacement ciphertext")); !errors.Is(err, ErrVaultObjectExists) {
		t.Fatalf("overwrite error=%v", err)
	}
	got, err := store.GetVault(context.Background(), "00000000-0000-0000-0000-000000000001", key)
	if err != nil || string(got) != "first ciphertext" {
		t.Fatalf("immutable value=%q err=%v", got, err)
	}
}

type extractionRepoFixture struct {
	claim      *ExtractionClaim
	published  []ingestion.BookExtraction
	failedCode string
}

func (r *extractionRepoFixture) ActiveTenantIDs(context.Context) ([]string, error) {
	return []string{"tenant-a"}, nil
}
func (r *extractionRepoFixture) ClaimExtraction(context.Context, string, time.Time, time.Duration) (*ExtractionClaim, error) {
	claim := r.claim
	r.claim = nil
	return claim, nil
}
func (r *extractionRepoFixture) PublishExtraction(_ context.Context, _ ExtractionClaim, value ingestion.BookExtraction, _ time.Time) error {
	r.published = append(r.published, value)
	return nil
}
func (r *extractionRepoFixture) FailExtraction(_ context.Context, _ ExtractionClaim, code string, _ time.Time) error {
	r.failedCode = code
	return nil
}

type extractionVaultFixture struct {
	tenant string
	key    string
	value  []byte
}

func (v extractionVaultFixture) GetVault(_ context.Context, tenant, key string) ([]byte, error) {
	if tenant != v.tenant || key != v.key || !bytes.HasPrefix([]byte(key), []byte("vault/"+tenant+"/")) {
		return nil, fmt.Errorf("outside tenant capability")
	}
	return append([]byte(nil), v.value...), nil
}

func TestExtractionWorkerPortsEveryRetainedFormatWithDurableProvenance(t *testing.T) {
	cases := []struct {
		name, media string
		content     []byte
		parser      string
	}{
		{name: "markdown", media: "text/markdown", content: []byte("# Chapter\nA cited paragraph."), parser: ParserMarkdownV1},
		{name: "text", media: "text/plain", content: []byte("A plain text document with provenance."), parser: ParserTextV1},
		{name: "epub", media: "application/epub+zip", content: extractionEPUB(t), parser: ParserEPUBV1},
		{name: "pdf", media: "application/pdf", content: extractionPDF("A positioned PDF sentence."), parser: ParserPDFNativeV2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claim := ExtractionClaim{TenantID: "tenant-a", SourceID: "00000000-0000-0000-0000-000000000001", Version: 1, MediaType: tc.media, VaultObjectKey: "vault/tenant-a/source/1.aesgcm", EncryptionVersion: "aes-256-gcm-v1"}
			encrypted, err := encryptVault(sha256Key("secret"), tc.content)
			if err != nil {
				t.Fatal(err)
			}
			repo := &extractionRepoFixture{claim: &claim}
			processor, err := NewExtractionProcessor(repo, extractionVaultFixture{tenant: claim.TenantID, key: claim.VaultObjectKey, value: encrypted}, "secret", func() time.Time { return time.Unix(1, 0).UTC() })
			if err != nil {
				t.Fatal(err)
			}
			count, err := processor.ProcessOnce(context.Background())
			if err != nil || count != 1 || len(repo.published) != 1 {
				t.Fatalf("published=%d values=%d err=%v code=%s", count, len(repo.published), err, repo.failedCode)
			}
			got := repo.published[0]
			if got.ParserVersion != tc.parser || got.NormalizationVersion != NormalizationTextV1 || len(got.Nodes) == 0 || len(got.Passages) == 0 {
				t.Fatalf("provenance or corpus missing: %+v", got)
			}
		})
	}
}

func TestExtractionWorkerRejectsCrossTenantCapabilityAndPublishesNothing(t *testing.T) {
	claim := ExtractionClaim{TenantID: "tenant-a", SourceID: "00000000-0000-0000-0000-000000000001", Version: 1, MediaType: "text/plain", VaultObjectKey: "vault/tenant-b/source/1.aesgcm", EncryptionVersion: "aes-256-gcm-v1"}
	repo := &extractionRepoFixture{claim: &claim}
	processor, err := NewExtractionProcessor(repo, extractionVaultFixture{tenant: "tenant-a", key: claim.VaultObjectKey, value: []byte("irrelevant")}, "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	count, err := processor.ProcessOnce(context.Background())
	if err == nil || count != 0 || len(repo.published) != 0 || repo.failedCode != "source_unavailable" {
		t.Fatalf("count=%d published=%d code=%q err=%v", count, len(repo.published), repo.failedCode, err)
	}
}

func TestExtractionErrorCodeClassifiesUntrustworthyPDFText(t *testing.T) {
	if code := extractionErrorCode(ingestion.ErrPDFTextUntrustworthy); code != "pdf_text_unreadable" {
		t.Fatalf("code=%q", code)
	}
}

func TestVaultCapabilityRejectsEveryUnrelatedTenantKey(t *testing.T) {
	for _, key := range []string{"vault/tenant-b/source/1.aesgcm", "vault/tenant-a-escape/source/1.aesgcm", "quarantine/tenant-a/source"} {
		if err := validateVaultCapability("tenant-a", key); err == nil {
			t.Fatalf("cross-tenant key %q was accepted", key)
		}
	}
	if err := validateVaultCapability("tenant-a", "vault/tenant-a/source/1.aesgcm"); err != nil {
		t.Fatalf("tenant key rejected: %v", err)
	}
}

func sha256Key(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func extractionEPUB(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	files := map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`,
		"OEBPS/content.opf":      `<package><metadata><title>Hosted</title><language>en</language><identifier>hosted-1</identifier></metadata><manifest><item id="c1" href="c1.xhtml"/></manifest><spine><itemref idref="c1"/></spine></package>`,
		"OEBPS/c1.xhtml":         `<html><head><title>One</title></head><body><h1>One</h1><p>Hosted EPUB passage.</p></body></html>`,
	}
	for name, value := range files {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func extractionPDF(value string) []byte {
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	stream := "BT /F1 12 Tf 72 720 Td (" + value + ") Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&output, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return output.Bytes()
}
