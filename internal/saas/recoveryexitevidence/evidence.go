// Package recoveryexitevidence normalizes content-free P0.2-B component
// recovery and external-integration exit evidence without approving P0.2-B.
package recoveryexitevidence

import (
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"

	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/platforminventory"
)

const (
	InputSchemaV1     = "agent-memory-component-recovery-exit-input-v1"
	ReceiptSchemaV1   = "agent-memory-component-recovery-exit-receipt-v1"
	maximumInputBytes = 256 << 10
	maximumAge        = 24 * time.Hour
	maximumAggregate  = 1_000_000_000
)

type SubjectClass string
type CheckID string
type Outcome string

const (
	ClassCoreComponent       SubjectClass = "core_component"
	ClassExternalIntegration SubjectClass = "external_integration"
	OutcomePassed            Outcome      = "passed"
	OutcomeFailed            Outcome      = "failed"
	OutcomeInconclusive      Outcome      = "inconclusive"

	CheckInventoryBinding CheckID = "inventory_binding_verified"
	CheckSubjectInventory CheckID = "subject_inventory_complete"
	CheckReplacement      CheckID = "replacement_exercises_passed"
	CheckFailover         CheckID = "failover_exercises_passed"
	CheckExport           CheckID = "export_exercises_passed"
	CheckRestore          CheckID = "restore_exercises_passed"
	CheckRecoveryTargets  CheckID = "recovery_targets_met"
	CheckOperationsReview CheckID = "operations_review_complete"
)

var (
	digestPattern        = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`)
	requiredComponents   = []string{"kubernetes", "identity", "postgres", "object_storage", "queue", "secrets", "observability", "backup"}
	requiredIntegrations = []string{"payment", "email", "model"}
	requiredChecks       = []CheckID{CheckInventoryBinding, CheckSubjectInventory, CheckReplacement, CheckFailover, CheckExport, CheckRestore, CheckRecoveryTargets, CheckOperationsReview}
)

type OperationReview struct {
	ProcedureSHA256        string  `json:"procedure_sha256"`
	ExerciseSHA256         string  `json:"exercise_sha256"`
	AttemptCount           int     `json:"attempt_count"`
	PassedCount            int     `json:"passed_count"`
	FailedCount            int     `json:"failed_count"`
	InconclusiveCount      int     `json:"inconclusive_count"`
	MaximumTargetSeconds   int     `json:"maximum_target_seconds"`
	MaximumObservedSeconds int     `json:"maximum_observed_seconds"`
	Outcome                Outcome `json:"outcome"`
}

type SubjectReview struct {
	Class            SubjectClass    `json:"class"`
	Kind             string          `json:"kind"`
	Enabled          bool            `json:"enabled"`
	ProcedureVersion string          `json:"procedure_version"`
	Replacement      OperationReview `json:"replacement"`
	Failover         OperationReview `json:"failover"`
	Export           OperationReview `json:"export"`
	Restore          OperationReview `json:"restore"`
	Outcome          Outcome         `json:"outcome"`
}

type Check struct {
	ID             CheckID `json:"id"`
	Outcome        Outcome `json:"outcome"`
	EvidenceSHA256 string  `json:"evidence_sha256"`
}

type Input struct {
	Schema                   string          `json:"schema"`
	Classification           string          `json:"classification"`
	Environment              string          `json:"environment"`
	ReviewID                 string          `json:"review_id"`
	ProcedureManifestVersion string          `json:"procedure_manifest_version"`
	ExerciseManifestVersion  string          `json:"exercise_manifest_version"`
	TargetPolicyVersion      string          `json:"target_policy_version"`
	InventoryID              string          `json:"inventory_id"`
	InventoryReceiptSHA256   string          `json:"inventory_receipt_sha256"`
	ProcedureManifestSHA256  string          `json:"procedure_manifest_sha256"`
	ExerciseManifestSHA256   string          `json:"exercise_manifest_sha256"`
	TargetPolicySHA256       string          `json:"target_policy_sha256"`
	OperationsReviewSHA256   string          `json:"operations_review_sha256"`
	ReviewedAt               time.Time       `json:"reviewed_at"`
	GeneratedAt              time.Time       `json:"generated_at"`
	Ready                    bool            `json:"ready"`
	Subjects                 []SubjectReview `json:"subjects"`
	Checks                   []Check         `json:"checks"`
}

type Receipt struct {
	Schema                     string            `json:"schema"`
	Classification             string            `json:"classification"`
	Environment                string            `json:"environment"`
	ReviewID                   string            `json:"review_id"`
	ProcedureManifestVersion   string            `json:"procedure_manifest_version"`
	ExerciseManifestVersion    string            `json:"exercise_manifest_version"`
	TargetPolicyVersion        string            `json:"target_policy_version"`
	InventoryID                string            `json:"inventory_id"`
	InventoryReceiptSHA256     string            `json:"inventory_receipt_sha256"`
	InputSHA256                string            `json:"input_sha256"`
	EvidenceDigests            map[string]string `json:"evidence_digests"`
	ReviewedAt                 time.Time         `json:"reviewed_at"`
	GeneratedAt                time.Time         `json:"generated_at"`
	CollectedAt                time.Time         `json:"collected_at"`
	Ready                      bool              `json:"ready"`
	SubjectCount               int               `json:"subject_count"`
	ComponentCount             int               `json:"component_count"`
	IntegrationCount           int               `json:"integration_count"`
	EnabledIntegrationCount    int               `json:"enabled_integration_count"`
	PassedSubjectCount         int               `json:"passed_subject_count"`
	FailedSubjectCount         int               `json:"failed_subject_count"`
	InconclusiveSubjectCount   int               `json:"inconclusive_subject_count"`
	OperationCount             int               `json:"operation_count"`
	PassedOperationCount       int               `json:"passed_operation_count"`
	FailedOperationCount       int               `json:"failed_operation_count"`
	InconclusiveOperationCount int               `json:"inconclusive_operation_count"`
	CheckCount                 int               `json:"check_count"`
	PassedCount                int               `json:"passed_count"`
	FailedCount                int               `json:"failed_count"`
	InconclusiveCount          int               `json:"inconclusive_count"`
	Subjects                   []SubjectReview   `json:"subjects"`
	Checks                     []Check           `json:"checks"`
}

func RequiredChecks() []CheckID { return append([]CheckID(nil), requiredChecks...) }

func Collect(inventoryPath, inputPath string, now time.Time) (Receipt, error) {
	inventory, err := platforminventory.Load(inventoryPath)
	if err != nil {
		return Receipt{}, fmt.Errorf("load self-managed platform inventory: %w", err)
	}
	var input Input
	digest, err := decodeStrictRegular(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(inventory, input, digest, now)
}

type summary struct{ components, integrations, enabled, subjectsPassed, subjectsFailed, subjectsInconclusive, operationsPassed, operationsFailed, operationsInconclusive int }

func build(inventory platforminventory.Inventory, input Input, inputDigest string, now time.Time) (Receipt, error) {
	if input.Schema != InputSchemaV1 || input.Classification != "self_managed_external" || input.Environment != string(inventory.Environment) || input.InventoryID != inventory.InventoryID || input.InventoryReceiptSHA256 != inventory.ReceiptSHA256 ||
		!allOpaque(input.ReviewID, input.ProcedureManifestVersion, input.ExerciseManifestVersion, input.TargetPolicyVersion) || !allDigests(inputDigest, input.ProcedureManifestSHA256, input.ExerciseManifestSHA256, input.TargetPolicySHA256, input.OperationsReviewSHA256) {
		return Receipt{}, errors.New("recovery-exit identity or inventory binding is invalid")
	}
	if now.IsZero() {
		return Receipt{}, errors.New("recovery-exit collection time is invalid")
	}
	now, reviewed, generated := now.UTC(), input.ReviewedAt.UTC(), input.GeneratedAt.UTC()
	if reviewed.IsZero() || generated.IsZero() || reviewed.Before(inventory.GeneratedAt.UTC()) || generated.Before(reviewed) || generated.After(now) || generated.Before(now.Add(-maximumAge)) {
		return Receipt{}, errors.New("recovery-exit evidence timeline is invalid")
	}
	subjects, totals, operationOutcomes, targetOutcome, err := validateSubjects(input.Subjects, inventory)
	if err != nil {
		return Receipt{}, err
	}
	checks, passed, failed, inconclusive, err := validateChecks(input.Checks)
	if err != nil {
		return Receipt{}, err
	}
	expected := map[CheckID]Outcome{
		CheckInventoryBinding: OutcomePassed, CheckSubjectInventory: OutcomePassed,
		CheckReplacement: operationOutcomes[0], CheckFailover: operationOutcomes[1], CheckExport: operationOutcomes[2], CheckRestore: operationOutcomes[3], CheckRecoveryTargets: targetOutcome,
	}
	for id, want := range expected {
		if outcomeFor(checks, id) != want {
			return Receipt{}, errors.New("recovery-exit check contradicts derived evidence")
		}
	}
	ready := totals.subjectsPassed == len(subjects) && totals.operationsPassed == len(subjects)*4 && passed == len(requiredChecks) && failed == 0 && inconclusive == 0
	if input.Ready != ready {
		return Receipt{}, errors.New("recovery-exit readiness contradicts evidence")
	}
	return Receipt{Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment, ReviewID: input.ReviewID,
		ProcedureManifestVersion: input.ProcedureManifestVersion, ExerciseManifestVersion: input.ExerciseManifestVersion, TargetPolicyVersion: input.TargetPolicyVersion,
		InventoryID: inventory.InventoryID, InventoryReceiptSHA256: inventory.ReceiptSHA256, InputSHA256: inputDigest,
		EvidenceDigests: map[string]string{"procedure_manifest": input.ProcedureManifestSHA256, "exercise_manifest": input.ExerciseManifestSHA256, "target_policy": input.TargetPolicySHA256, "operations_review": input.OperationsReviewSHA256},
		ReviewedAt:      reviewed, GeneratedAt: generated, CollectedAt: now, Ready: ready, SubjectCount: len(subjects), ComponentCount: totals.components, IntegrationCount: totals.integrations, EnabledIntegrationCount: totals.enabled,
		PassedSubjectCount: totals.subjectsPassed, FailedSubjectCount: totals.subjectsFailed, InconclusiveSubjectCount: totals.subjectsInconclusive,
		OperationCount: len(subjects) * 4, PassedOperationCount: totals.operationsPassed, FailedOperationCount: totals.operationsFailed, InconclusiveOperationCount: totals.operationsInconclusive,
		CheckCount: len(checks), PassedCount: passed, FailedCount: failed, InconclusiveCount: inconclusive, Subjects: subjects, Checks: checks}, nil
}

func validateSubjects(values []SubjectReview, inventory platforminventory.Inventory) ([]SubjectReview, summary, [4]Outcome, Outcome, error) {
	expected := make(map[string]bool, 11)
	for _, c := range inventory.Components {
		expected[string(ClassCoreComponent)+":"+string(c.Kind)] = true
	}
	for _, i := range inventory.ExternalIntegrations {
		expected[string(ClassExternalIntegration)+":"+string(i.Kind)] = i.Enabled
	}
	if len(values) != len(expected) {
		return nil, summary{}, [4]Outcome{}, "", errors.New("recovery-exit subject set is incomplete")
	}
	seen := make(map[string]struct{}, len(values))
	ordered := append([]SubjectReview(nil), values...)
	totals := summary{}
	opCounts := [4][3]int{}
	targetsFailed := false
	for _, value := range ordered {
		key := string(value.Class) + ":" + value.Kind
		enabled, ok := expected[key]
		if !ok || value.Enabled != enabled || !opaquePattern.MatchString(value.ProcedureVersion) {
			return nil, summary{}, [4]Outcome{}, "", errors.New("recovery-exit subject binding is invalid")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, summary{}, [4]Outcome{}, "", errors.New("recovery-exit subject is duplicated")
		}
		seen[key] = struct{}{}
		if value.Class == ClassCoreComponent {
			totals.components++
		} else if value.Class == ClassExternalIntegration {
			totals.integrations++
			if value.Enabled {
				totals.enabled++
			}
		} else {
			return nil, summary{}, [4]Outcome{}, "", errors.New("recovery-exit subject class is invalid")
		}
		ops := []OperationReview{value.Replacement, value.Failover, value.Export, value.Restore}
		derived := make([]Outcome, 4)
		for index, op := range ops {
			outcome, targetMet, err := validateOperation(op)
			if err != nil {
				return nil, summary{}, [4]Outcome{}, "", err
			}
			derived[index] = outcome
			if !targetMet {
				targetsFailed = true
			}
			switch outcome {
			case OutcomePassed:
				totals.operationsPassed++
				opCounts[index][0]++
			case OutcomeFailed:
				totals.operationsFailed++
				opCounts[index][1]++
			case OutcomeInconclusive:
				totals.operationsInconclusive++
				opCounts[index][2]++
			}
		}
		want := aggregateOutcomes(derived...)
		if value.Outcome != want {
			return nil, summary{}, [4]Outcome{}, "", errors.New("recovery-exit subject outcome contradicts operations")
		}
		switch want {
		case OutcomePassed:
			totals.subjectsPassed++
		case OutcomeFailed:
			totals.subjectsFailed++
		case OutcomeInconclusive:
			totals.subjectsInconclusive++
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Class == ordered[j].Class {
			return ordered[i].Kind < ordered[j].Kind
		}
		return ordered[i].Class < ordered[j].Class
	})
	var outcomes [4]Outcome
	for i := range outcomes {
		outcomes[i] = aggregateCounts(opCounts[i][0], opCounts[i][1], opCounts[i][2], len(values))
	}
	targetOutcome := OutcomePassed
	if targetsFailed {
		targetOutcome = OutcomeFailed
	}
	return ordered, totals, outcomes, targetOutcome, nil
}

func validateOperation(value OperationReview) (Outcome, bool, error) {
	if !allDigests(value.ProcedureSHA256, value.ExerciseSHA256) {
		return "", false, errors.New("recovery-exit operation digest is invalid")
	}
	counts := []int{value.AttemptCount, value.PassedCount, value.FailedCount, value.InconclusiveCount, value.MaximumTargetSeconds, value.MaximumObservedSeconds}
	for _, count := range counts {
		if count < 0 || count > maximumAggregate {
			return "", false, errors.New("recovery-exit operation aggregate is invalid")
		}
	}
	if value.AttemptCount == 0 || value.MaximumTargetSeconds == 0 || value.PassedCount+value.FailedCount+value.InconclusiveCount != value.AttemptCount {
		return "", false, errors.New("recovery-exit operation attempts are invalid")
	}
	targetMet := value.MaximumObservedSeconds <= value.MaximumTargetSeconds
	derived := OutcomePassed
	if value.FailedCount > 0 || !targetMet {
		derived = OutcomeFailed
	} else if value.InconclusiveCount > 0 || value.PassedCount != value.AttemptCount {
		derived = OutcomeInconclusive
	}
	if value.Outcome != derived {
		return "", false, errors.New("recovery-exit operation outcome contradicts exercise")
	}
	return derived, targetMet, nil
}

func validateChecks(values []Check) ([]Check, int, int, int, error) {
	if len(values) != len(requiredChecks) {
		return nil, 0, 0, 0, errors.New("recovery-exit check set is incomplete")
	}
	byID := map[CheckID]Check{}
	passed, failed, inconclusive := 0, 0, 0
	allowed := map[CheckID]struct{}{}
	for _, id := range requiredChecks {
		allowed[id] = struct{}{}
	}
	for _, v := range values {
		if _, ok := allowed[v.ID]; !ok || !digestPattern.MatchString(v.EvidenceSHA256) {
			return nil, 0, 0, 0, errors.New("recovery-exit check is invalid")
		}
		if _, dup := byID[v.ID]; dup {
			return nil, 0, 0, 0, errors.New("recovery-exit check is duplicated")
		}
		switch v.Outcome {
		case OutcomePassed:
			passed++
		case OutcomeFailed:
			failed++
		case OutcomeInconclusive:
			inconclusive++
		default:
			return nil, 0, 0, 0, errors.New("recovery-exit check outcome is invalid")
		}
		byID[v.ID] = v
	}
	ordered := make([]Check, 0, len(requiredChecks))
	for _, id := range requiredChecks {
		ordered = append(ordered, byID[id])
	}
	return ordered, passed, failed, inconclusive, nil
}

func outcomeFor(values []Check, id CheckID) Outcome {
	for _, v := range values {
		if v.ID == id {
			return v.Outcome
		}
	}
	return ""
}
func aggregateCounts(passed, failed, inconclusive, total int) Outcome {
	if failed > 0 {
		return OutcomeFailed
	}
	if inconclusive > 0 || passed != total {
		return OutcomeInconclusive
	}
	return OutcomePassed
}
func aggregateOutcomes(values ...Outcome) Outcome {
	failed, inconclusive := 0, 0
	for _, v := range values {
		if v == OutcomeFailed {
			failed++
		} else if v == OutcomeInconclusive {
			inconclusive++
		}
	}
	return aggregateCounts(len(values)-failed-inconclusive, failed, inconclusive, len(values))
}
func allDigests(values ...string) bool {
	for _, v := range values {
		if !digestPattern.MatchString(v) {
			return false
		}
	}
	return true
}
func allOpaque(values ...string) bool {
	for _, v := range values {
		if !opaquePattern.MatchString(v) {
			return false
		}
	}
	return true
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("receipt path is required")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-recovery-exit-*")
}

func decodeStrictRegular(path string, destination any) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("input path is required")
	}
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumInputBytes {
		return "", errors.New("input must be a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("open input")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return "", errors.New("input changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximumInputBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() || len(contents) > maximumInputBytes {
		return "", errors.New("read input")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(destination); err != nil {
		return "", errors.New("input JSON is invalid")
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("input contains trailing JSON")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || openedAfterRead.Size() != opened.Size() || !openedAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("input changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return "", errors.New("input changed while reading")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}
