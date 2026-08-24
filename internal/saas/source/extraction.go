package source

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/ingestion"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

const (
	ParserMarkdownV1      = "markdown-v1"
	ParserTextV1          = "text-v1"
	ParserEPUBV1          = "epub-v1"
	ParserPDFNativeV1     = "pdf-native-v1"
	ParserPDFNativeV2     = "pdf-native-poppler-fallback-v2"
	NormalizationTextV1   = "unicode-text-v1"
	extractionLeasePeriod = 5 * time.Minute
)

type ExtractionClaim struct {
	TenantID          string
	SourceID          string
	Version           int64
	MediaType         string
	VaultObjectKey    string
	EncryptionVersion string
}

type ExtractionRepository interface {
	ActiveTenantIDs(context.Context) ([]string, error)
	ClaimExtraction(context.Context, string, time.Time, time.Duration) (*ExtractionClaim, error)
	PublishExtraction(context.Context, ExtractionClaim, ingestion.BookExtraction, time.Time) error
	FailExtraction(context.Context, ExtractionClaim, string, time.Time) error
}

type TenantVaultReader interface {
	GetVault(context.Context, string, string) ([]byte, error)
}

type ExtractionProcessor struct {
	repository ExtractionRepository
	vault      TenantVaultReader
	extractors map[library.SourceFormat]ingestion.BookExtractor
	key        []byte
	now        func() time.Time
}

func NewExtractionProcessor(repository ExtractionRepository, vault TenantVaultReader, encryptionSecret string, now func() time.Time) (*ExtractionProcessor, error) {
	if repository == nil || vault == nil || strings.TrimSpace(encryptionSecret) == "" {
		return nil, errors.New("source extraction processor is not configured")
	}
	if now == nil {
		now = time.Now
	}
	sum := sha256.Sum256([]byte(encryptionSecret))
	extractors := []ingestion.BookExtractor{
		ingestion.MarkdownBookExtractor{Adapter: ingestion.MarkdownAdapter{ParserVersion: ParserMarkdownV1, NormalizationVersion: NormalizationTextV1}},
		ingestion.MarkdownBookExtractor{Adapter: ingestion.MarkdownAdapter{ParserVersion: ParserTextV1, NormalizationVersion: NormalizationTextV1}, AsText: true},
		ingestion.EPUBBookExtractor{Adapter: ingestion.EPUBAdapter{ParserVersion: ParserEPUBV1, NormalizationVersion: NormalizationTextV1}},
		ingestion.PDFBookExtractor{Adapter: ingestion.PDFAdapter{ParserVersion: ParserPDFNativeV2, NormalizationVersion: NormalizationTextV1, Extractor: ingestion.ReliablePDFExtractor{Primary: ingestion.NativePDFExtractor{}, Fallback: ingestion.PopplerPDFExtractor{}}}},
	}
	byFormat := make(map[library.SourceFormat]ingestion.BookExtractor, len(extractors))
	for _, extractor := range extractors {
		byFormat[extractor.Format()] = extractor
	}
	return &ExtractionProcessor{repository: repository, vault: vault, extractors: byFormat, key: sum[:], now: now}, nil
}

func (p *ExtractionProcessor) ProcessOnce(ctx context.Context) (int, error) {
	tenants, err := p.repository.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	published := 0
	var failures []error
	for _, tenant := range tenants {
		claim, err := p.repository.ClaimExtraction(ctx, tenant, p.now().UTC(), extractionLeasePeriod)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if claim == nil {
			continue
		}
		if err := p.processClaim(ctx, *claim); err != nil {
			_ = p.repository.FailExtraction(ctx, *claim, extractionErrorCode(err), p.now().UTC())
			failures = append(failures, err)
			continue
		}
		published++
	}
	return published, errors.Join(failures...)
}

func (p *ExtractionProcessor) processClaim(ctx context.Context, claim ExtractionClaim) error {
	if claim.EncryptionVersion != "aes-256-gcm-v1" {
		return errors.New("unsupported vault encryption version")
	}
	encrypted, err := p.vault.GetVault(ctx, claim.TenantID, claim.VaultObjectKey)
	if err != nil {
		return fmt.Errorf("read tenant vault object: %w", err)
	}
	plain, err := decryptVault(p.key, encrypted)
	if err != nil {
		return fmt.Errorf("decrypt tenant vault object: %w", err)
	}
	format, ok := sourceFormat(claim.MediaType)
	if !ok {
		return errors.New("unsupported extraction media type")
	}
	extractor := p.extractors[format]
	editionID := fmt.Sprintf("%s:v%d", claim.SourceID, claim.Version)
	extraction, err := extractor.Extract(ctx, editionID, claim.SourceID, plain)
	if err != nil {
		return fmt.Errorf("extract source: %w", err)
	}
	if err := validateExtraction(editionID, claim.SourceID, extraction); err != nil {
		return err
	}
	return p.repository.PublishExtraction(ctx, claim, extraction, p.now().UTC())
}

func (p *ExtractionProcessor) Run(ctx context.Context, poll time.Duration, report func(error)) {
	if poll <= 0 {
		poll = time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		if _, err := p.ProcessOnce(ctx); err != nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func decryptVault(key, value []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(value) < aead.NonceSize() {
		return nil, errors.New("encrypted source is truncated")
	}
	return aead.Open(nil, value[:aead.NonceSize()], value[aead.NonceSize():], nil)
}

func sourceFormat(mediaType string) (library.SourceFormat, bool) {
	switch mediaType {
	case "application/pdf":
		return library.FormatPDF, true
	case "application/epub+zip":
		return library.FormatEPUB, true
	case "text/markdown":
		return library.FormatMarkdown, true
	case "text/plain":
		return library.FormatText, true
	default:
		return "", false
	}
}

func validateExtraction(editionID, sourceID string, extraction ingestion.BookExtraction) error {
	if strings.TrimSpace(extraction.NormalizedText) == "" || extraction.ParserVersion == "" || extraction.NormalizationVersion == "" {
		return errors.New("extraction produced no searchable text or provenance")
	}
	if len(extraction.Nodes) == 0 || len(extraction.Passages) == 0 {
		return errors.New("extraction produced no publishable corpus")
	}
	if err := library.ValidateStructure(extraction.Nodes); err != nil {
		return err
	}
	for _, node := range extraction.Nodes {
		if node.EditionID != editionID {
			return errors.New("extraction node identity mismatch")
		}
	}
	for _, passage := range extraction.Passages {
		if passage.EditionID != editionID || passage.SourceAssetID != sourceID {
			return errors.New("extraction passage identity mismatch")
		}
		if err := passage.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func extractionErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ingestion.ErrPDFTextUntrustworthy) {
		return "pdf_text_unreadable"
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "vault") || strings.Contains(message, "decrypt"):
		return "source_unavailable"
	case strings.Contains(message, "unsupported"):
		return "format_unsupported"
	default:
		return "extraction_failed"
	}
}
