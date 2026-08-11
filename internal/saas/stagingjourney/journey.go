// Package stagingjourney validates and combines content-free human and agent
// journey evidence from a real staging release.
package stagingjourney

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
	JourneySchemaV1 = "agent-memory-staging-client-journey-v1"
	ReceiptSchemaV1 = "agent-memory-staging-client-journey-bundle-v1"

	maximumJourneyBytes  = 64 << 10
	maximumJourneyWindow = 30 * time.Minute
	maximumJourneyAge    = 24 * time.Hour
)

type ClientKind string
type CheckID string
type Outcome string

const (
	HumanWeb    ClientKind = "human_web"
	ScopedAgent ClientKind = "scoped_agent"

	CheckAuthenticated      CheckID = "identity_authenticated"
	CheckMemoryWriteAudited CheckID = "memory_write_audited"
	CheckMemorySearchAudit  CheckID = "memory_search_audited"
	CheckExportReadyAudited CheckID = "export_ready_audited"
	CheckClientCleanup      CheckID = "client_cleanup"

	OutcomePassed Outcome = "passed"
	OutcomeFailed Outcome = "failed"
)

var (
	digestPattern  = regexp.MustCompile(`^[a-f0-9]{64}$`)
	traceIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
	requiredChecks = []CheckID{
		CheckAuthenticated,
		CheckMemoryWriteAudited,
		CheckMemorySearchAudit,
		CheckExportReadyAudited,
		CheckClientCleanup,
	}
)

type Check struct {
	ID        CheckID `json:"id"`
	Outcome   Outcome `json:"outcome"`
	RequestID string  `json:"request_id"`
}

type Journey struct {
	Schema               string     `json:"schema"`
	Classification       string     `json:"classification"`
	Environment          string     `json:"environment"`
	ReleaseID            string     `json:"release_id"`
	ReleaseReceiptSHA256 string     `json:"release_receipt_sha256"`
	ClientKind           ClientKind `json:"client_kind"`
	Ready                bool       `json:"ready"`
	TraceID              string     `json:"trace_id"`
	StartedAt            time.Time  `json:"started_at"`
	CompletedAt          time.Time  `json:"completed_at"`
	Checks               []Check    `json:"checks"`
}

type ReceiptJourney struct {
	ClientKind  ClientKind `json:"client_kind"`
	InputSHA256 string     `json:"input_sha256"`
	TraceID     string     `json:"trace_id"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt time.Time  `json:"completed_at"`
	Checks      []Check    `json:"checks"`
}

type Receipt struct {
	Schema               string           `json:"schema"`
	Ready                bool             `json:"ready"`
	Environment          string           `json:"environment"`
	ReleaseID            string           `json:"release_id"`
	ReleaseReceiptSHA256 string           `json:"release_receipt_sha256"`
	CollectedAt          time.Time        `json:"collected_at"`
	Journeys             []ReceiptJourney `json:"journeys"`
}

type Assessment struct {
	Ready       bool
	ClientCount int
	CheckCount  int
	PassedCount int
	FailedCount int
}

type loadedJourney struct {
	value  Journey
	digest string
}

func Collect(releasePath, firstJourneyPath, secondJourneyPath string, now time.Time) (Receipt, error) {
	release, releaseDigest, err := platformrollback.LoadPassedRelease(releasePath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load passed staging release: %w", err)
	}
	if now.IsZero() {
		return Receipt{}, errors.New("staging journey collection time is invalid")
	}
	now = now.UTC()
	loaded := make([]loadedJourney, 0, 2)
	for _, path := range []string{firstJourneyPath, secondJourneyPath} {
		var journey Journey
		digest, err := decodeStrictRegular(path, &journey)
		if err != nil {
			return Receipt{}, err
		}
		if err := validateJourney(journey, release.ReleaseID, releaseDigest, release.CompletedAt.UTC(), now); err != nil {
			return Receipt{}, err
		}
		journey.Checks = orderChecks(journey.Checks)
		loaded = append(loaded, loadedJourney{value: journey, digest: digest})
	}
	if loaded[0].value.ClientKind == loaded[1].value.ClientKind || loaded[0].digest == loaded[1].digest || loaded[0].value.TraceID == loaded[1].value.TraceID {
		return Receipt{}, errors.New("staging journeys are not two distinct client runs")
	}
	requestIDs := make(map[string]struct{}, len(requiredChecks)*2)
	for _, item := range loaded {
		for _, check := range item.value.Checks {
			if _, exists := requestIDs[check.RequestID]; exists {
				return Receipt{}, errors.New("staging journey request IDs are not unique")
			}
			requestIDs[check.RequestID] = struct{}{}
		}
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].value.ClientKind < loaded[j].value.ClientKind })
	receipt := Receipt{
		Schema: ReceiptSchemaV1, Ready: true, Environment: "staging",
		ReleaseID: release.ReleaseID, ReleaseReceiptSHA256: releaseDigest,
		CollectedAt: now, Journeys: make([]ReceiptJourney, 0, 2),
	}
	for _, item := range loaded {
		journey := item.value
		if !journey.Ready {
			receipt.Ready = false
		}
		receipt.Journeys = append(receipt.Journeys, ReceiptJourney{
			ClientKind: journey.ClientKind, InputSHA256: item.digest, TraceID: journey.TraceID,
			StartedAt: journey.StartedAt.UTC(), CompletedAt: journey.CompletedAt.UTC(), Checks: journey.Checks,
		})
	}
	return receipt, nil
}

func validateJourney(journey Journey, releaseID, releaseDigest string, releaseCompleted, now time.Time) error {
	if journey.Schema != JourneySchemaV1 || journey.Classification != "staging_external" || journey.Environment != "staging" ||
		journey.ReleaseID != releaseID || journey.ReleaseReceiptSHA256 != releaseDigest || !digestPattern.MatchString(journey.ReleaseReceiptSHA256) ||
		(journey.ClientKind != HumanWeb && journey.ClientKind != ScopedAgent) || !traceIDPattern.MatchString(journey.TraceID) {
		return errors.New("staging journey identity is invalid")
	}
	started, completed := journey.StartedAt.UTC(), journey.CompletedAt.UTC()
	if started.IsZero() || completed.IsZero() || completed.Before(started) || completed.Sub(started) > maximumJourneyWindow ||
		started.Before(releaseCompleted) || completed.After(now) || completed.Before(now.Add(-maximumJourneyAge)) {
		return errors.New("staging journey window is invalid")
	}
	if len(journey.Checks) != len(requiredChecks) {
		return errors.New("staging journey checks are incomplete")
	}
	checks := make(map[CheckID]Check, len(journey.Checks))
	allPassed := true
	for _, check := range journey.Checks {
		if (check.Outcome != OutcomePassed && check.Outcome != OutcomeFailed) || !validRequestID(check.RequestID) {
			return errors.New("staging journey check is invalid")
		}
		if _, exists := checks[check.ID]; exists {
			return errors.New("staging journey check is duplicated")
		}
		checks[check.ID] = check
		allPassed = allPassed && check.Outcome == OutcomePassed
	}
	for _, id := range requiredChecks {
		if _, exists := checks[id]; !exists {
			return errors.New("staging journey required check is missing")
		}
	}
	if journey.Ready != allPassed {
		return errors.New("staging journey readiness contradicts its checks")
	}
	return nil
}

func orderChecks(checks []Check) []Check {
	byID := make(map[CheckID]Check, len(checks))
	for _, check := range checks {
		byID[check.ID] = check
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		ordered = append(ordered, byID[id])
	}
	return ordered
}

func validRequestID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value && parsed.Version() == 4
}

func Assess(receipt Receipt) Assessment {
	assessment := Assessment{Ready: receipt.Ready, ClientCount: len(receipt.Journeys)}
	for _, journey := range receipt.Journeys {
		assessment.CheckCount += len(journey.Checks)
		for _, check := range journey.Checks {
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
		return "", errors.New("staging journey path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumJourneyBytes {
		return "", errors.New("staging journey must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open staging journey")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("staging journey changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumJourneyBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumJourneyBytes {
		return "", errors.New("read staging journey")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("staging journey JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("staging journey contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("staging journey changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("staging journey changed while reading")
	}
	return sha256Hex(contents), nil
}

func sha256Hex(contents []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("staging journey bundle receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("staging journey bundle receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect staging journey bundle receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-staging-journey-*")
}
