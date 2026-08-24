// Package stagingformat validates content-free evidence that all four MVP
// source formats completed the staging ingestion lifecycle for one release.
package stagingformat

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/platformrollback"
)

const (
	InputSchemaV1   = "agent-memory-staging-format-ingestion-v1"
	ReceiptSchemaV1 = "agent-memory-staging-format-ingestion-receipt-v1"

	maximumInputBytes = 128 << 10
	maximumRunWindow  = 6 * time.Hour
	maximumBundleSpan = 24 * time.Hour
	maximumInputAge   = 24 * time.Hour
	maximumDocuments  = 10_000_000
)

type Format string
type CheckID string
type Outcome string

const (
	FormatPDF      Format = "pdf"
	FormatEPUB     Format = "epub"
	FormatMarkdown Format = "markdown"
	FormatText     Format = "text"

	CheckUploadAccepted          CheckID = "upload_accepted"
	CheckSourceVersionPublished  CheckID = "source_version_published"
	CheckIngestionJobSucceeded   CheckID = "ingestion_job_succeeded"
	CheckFullTextProjectionReady CheckID = "fulltext_projection_ready"
	CheckVectorProjectionReady   CheckID = "vector_projection_ready"
	CheckSourceReady             CheckID = "source_ready"
	CheckSourceDeleted           CheckID = "source_deleted"

	OutcomePassed Outcome = "passed"
	OutcomeFailed Outcome = "failed"
)

var (
	digestPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	traceIDPattern  = regexp.MustCompile(`^[a-f0-9]{32}$`)
	versionPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	requiredFormats = []Format{FormatPDF, FormatEPUB, FormatMarkdown, FormatText}
	requiredChecks  = []CheckID{
		CheckUploadAccepted,
		CheckSourceVersionPublished,
		CheckIngestionJobSucceeded,
		CheckFullTextProjectionReady,
		CheckVectorProjectionReady,
		CheckSourceReady,
		CheckSourceDeleted,
	}
	formatMediaTypes = map[Format]string{
		FormatPDF: "application/pdf", FormatEPUB: "application/epub+zip",
		FormatMarkdown: "text/markdown", FormatText: "text/plain",
	}
)

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Projection struct {
	Version       string `json:"version"`
	DocumentCount int    `json:"document_count"`
	ReceiptSHA256 string `json:"receipt_sha256"`
}

type Run struct {
	Format                     Format     `json:"format"`
	MediaType                  string     `json:"media_type"`
	SourceID                   string     `json:"source_id"`
	SourceVersion              int64      `json:"source_version"`
	SourceVersionReceiptSHA256 string     `json:"source_version_receipt_sha256"`
	IngestionJobID             string     `json:"ingestion_job_id"`
	IngestionJobReceiptSHA256  string     `json:"ingestion_job_receipt_sha256"`
	TraceID                    string     `json:"trace_id"`
	StartedAt                  time.Time  `json:"started_at"`
	CompletedAt                time.Time  `json:"completed_at"`
	Ready                      bool       `json:"ready"`
	FullTextProjection         Projection `json:"fulltext_projection"`
	VectorProjection           Projection `json:"vector_projection"`
	Checks                     []Check    `json:"checks"`
}

type Input struct {
	Schema               string    `json:"schema"`
	Classification       string    `json:"classification"`
	Environment          string    `json:"environment"`
	ReleaseID            string    `json:"release_id"`
	ReleaseReceiptSHA256 string    `json:"release_receipt_sha256"`
	Ready                bool      `json:"ready"`
	GeneratedAt          time.Time `json:"generated_at"`
	Runs                 []Run     `json:"runs"`
}

type Receipt struct {
	Schema               string    `json:"schema"`
	Ready                bool      `json:"ready"`
	Environment          string    `json:"environment"`
	ReleaseID            string    `json:"release_id"`
	ReleaseReceiptSHA256 string    `json:"release_receipt_sha256"`
	InputSHA256          string    `json:"input_sha256"`
	GeneratedAt          time.Time `json:"generated_at"`
	CollectedAt          time.Time `json:"collected_at"`
	Runs                 []Run     `json:"runs"`
}

type Assessment struct {
	Ready       bool
	FormatCount int
	CheckCount  int
	PassedCount int
	FailedCount int
}

func Collect(releasePath, inputPath string, now time.Time) (Receipt, error) {
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load passed staging release: %w", err)
	}
	if now.IsZero() {
		return Receipt{}, errors.New("staging format collection time is invalid")
	}
	now = now.UTC()
	var input Input
	inputDigest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	runs, err := validateInput(input, release.ReleaseID, releaseDigest, release.CompletedAt.UTC(), now)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{
		Schema: ReceiptSchemaV1, Ready: input.Ready, Environment: "staging",
		ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest,
		InputSHA256: inputDigest, GeneratedAt: input.GeneratedAt.UTC(), CollectedAt: now,
		Runs: runs,
	}, nil
}

func validateInput(input Input, releaseID, releaseDigest string, releaseCompleted, now time.Time) ([]Run, error) {
	generated := input.GeneratedAt.UTC()
	if input.Schema != InputSchemaV1 || input.Classification != "staging_external" || input.Environment != "staging" ||
		input.ReleaseID != releaseID || input.ReleaseReceiptSHA256 != releaseDigest || !digestPattern.MatchString(input.ReleaseReceiptSHA256) ||
		generated.IsZero() || generated.After(now) || generated.Before(now.Add(-maximumInputAge)) || len(input.Runs) != len(requiredFormats) {
		return nil, errors.New("staging format input identity, freshness, or run count is invalid")
	}
	formats := make(map[Format]struct{}, len(input.Runs))
	sourceIDs := make(map[string]struct{}, len(input.Runs))
	jobIDs := make(map[string]struct{}, len(input.Runs))
	recordIDs := make(map[string]struct{}, len(input.Runs)*2)
	traceIDs := make(map[string]struct{}, len(input.Runs))
	runs := make([]Run, 0, len(input.Runs))
	allReady := true
	var earliest, latest time.Time
	for _, run := range input.Runs {
		if _, exists := formats[run.Format]; exists {
			return nil, errors.New("staging format run is duplicated")
		}
		if _, exists := formatMediaTypes[run.Format]; !exists {
			return nil, errors.New("staging format run format is invalid")
		}
		if _, exists := sourceIDs[run.SourceID]; exists {
			return nil, errors.New("staging format source IDs are not unique")
		}
		if _, exists := jobIDs[run.IngestionJobID]; exists {
			return nil, errors.New("staging format job IDs are not unique")
		}
		if _, exists := recordIDs[run.SourceID]; exists {
			return nil, errors.New("staging format record IDs are not unique")
		}
		if _, exists := recordIDs[run.IngestionJobID]; exists || run.IngestionJobID == run.SourceID {
			return nil, errors.New("staging format record IDs are not unique")
		}
		if _, exists := traceIDs[run.TraceID]; exists {
			return nil, errors.New("staging format trace IDs are not unique")
		}
		normalized, err := validateRun(run, releaseCompleted, generated)
		if err != nil {
			return nil, err
		}
		formats[run.Format], sourceIDs[run.SourceID], jobIDs[run.IngestionJobID], traceIDs[run.TraceID] = struct{}{}, struct{}{}, struct{}{}, struct{}{}
		recordIDs[run.SourceID], recordIDs[run.IngestionJobID] = struct{}{}, struct{}{}
		allReady = allReady && run.Ready
		if earliest.IsZero() || normalized.StartedAt.Before(earliest) {
			earliest = normalized.StartedAt
		}
		if latest.IsZero() || normalized.CompletedAt.After(latest) {
			latest = normalized.CompletedAt
		}
		runs = append(runs, normalized)
	}
	for _, format := range requiredFormats {
		if _, exists := formats[format]; !exists {
			return nil, errors.New("staging format required run is missing")
		}
	}
	if latest.Sub(earliest) > maximumBundleSpan || generated.Before(latest) || input.Ready != allReady {
		return nil, errors.New("staging format bundle window or readiness is invalid")
	}
	sort.Slice(runs, func(i, j int) bool { return formatIndex(runs[i].Format) < formatIndex(runs[j].Format) })
	return runs, nil
}

func validateRun(run Run, releaseCompleted, generated time.Time) (Run, error) {
	started, completed := run.StartedAt.UTC(), run.CompletedAt.UTC()
	if run.MediaType != mediaTypeFor(run.Format) || !uuidV4(run.SourceID) || !uuidV4(run.IngestionJobID) ||
		run.SourceVersion < 1 || !digestPattern.MatchString(run.SourceVersionReceiptSHA256) ||
		!digestPattern.MatchString(run.IngestionJobReceiptSHA256) || !traceIDPattern.MatchString(run.TraceID) ||
		started.IsZero() || completed.IsZero() || completed.Before(started) || completed.Sub(started) > maximumRunWindow ||
		started.Before(releaseCompleted) || completed.After(generated) {
		return Run{}, errors.New("staging format run identity or window is invalid")
	}
	if !validProjection(run.FullTextProjection) || !validProjection(run.VectorProjection) {
		return Run{}, errors.New("staging format projection summary is invalid")
	}
	checks, allPassed, err := validateChecks(run)
	if err != nil {
		return Run{}, err
	}
	ready := allPassed && run.FullTextProjection.DocumentCount > 0 && run.VectorProjection.DocumentCount > 0
	if run.Ready != ready {
		return Run{}, errors.New("staging format run readiness contradicts checks or projections")
	}
	run.StartedAt, run.CompletedAt, run.Checks = started, completed, checks
	return run, nil
}

func validateChecks(run Run) ([]Check, bool, error) {
	if len(run.Checks) != len(requiredChecks) {
		return nil, false, errors.New("staging format checks are incomplete")
	}
	byID := make(map[CheckID]Check, len(run.Checks))
	for _, check := range run.Checks {
		if (check.Outcome != OutcomePassed && check.Outcome != OutcomeFailed) || !digestPattern.MatchString(check.EvidenceSHA256) {
			return nil, false, errors.New("staging format check is invalid")
		}
		if _, exists := byID[check.ID]; exists {
			return nil, false, errors.New("staging format check is duplicated")
		}
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		check, exists := byID[id]
		if !exists {
			return nil, false, errors.New("staging format required check is missing")
		}
		ordered = append(ordered, check)
	}
	if byID[CheckSourceVersionPublished].EvidenceSHA256 != run.SourceVersionReceiptSHA256 ||
		byID[CheckIngestionJobSucceeded].EvidenceSHA256 != run.IngestionJobReceiptSHA256 ||
		byID[CheckFullTextProjectionReady].EvidenceSHA256 != run.FullTextProjection.ReceiptSHA256 ||
		byID[CheckVectorProjectionReady].EvidenceSHA256 != run.VectorProjection.ReceiptSHA256 {
		return nil, false, errors.New("staging format check receipt binding is invalid")
	}
	passed := func(id CheckID) bool { return byID[id].Outcome == OutcomePassed }
	if (passed(CheckSourceVersionPublished) && !passed(CheckUploadAccepted)) ||
		(passed(CheckIngestionJobSucceeded) && !passed(CheckSourceVersionPublished)) ||
		((passed(CheckFullTextProjectionReady) || passed(CheckVectorProjectionReady)) && !passed(CheckIngestionJobSucceeded)) ||
		(passed(CheckSourceReady) && (!passed(CheckFullTextProjectionReady) || !passed(CheckVectorProjectionReady))) {
		return nil, false, errors.New("staging format check prerequisites are contradictory")
	}
	allPassed := true
	for _, check := range ordered {
		allPassed = allPassed && check.Outcome == OutcomePassed
	}
	return ordered, allPassed, nil
}

func validProjection(value Projection) bool {
	return versionPattern.MatchString(value.Version) && value.DocumentCount >= 0 && value.DocumentCount <= maximumDocuments && digestPattern.MatchString(value.ReceiptSHA256)
}

func mediaTypeFor(format Format) string { return formatMediaTypes[format] }

func formatIndex(format Format) int {
	for index, candidate := range requiredFormats {
		if candidate == format {
			return index
		}
	}
	return len(requiredFormats)
}

func uuidV4(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value && parsed.Version() == 4
}

func Assess(receipt Receipt) Assessment {
	assessment := Assessment{Ready: receipt.Ready, FormatCount: len(receipt.Runs)}
	for _, run := range receipt.Runs {
		assessment.CheckCount += len(run.Checks)
		for _, check := range run.Checks {
			if check.Outcome == OutcomePassed {
				assessment.PassedCount++
			} else {
				assessment.FailedCount++
			}
		}
	}
	return assessment
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("staging format input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("staging format input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open staging format input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("staging format input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read staging format input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("staging format input JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("staging format input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("staging format input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("staging format input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("staging format receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("staging format receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect staging format receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-staging-format-*")
}
