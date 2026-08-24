// Package mvpreadinessevidence derives the final eight MVP gates from the
// independently verified 49-control P0-P12 foundation.
package mvpreadinessevidence

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidenceindex"
)

const (
	InputSchemaV1   = "agent-memory-final-mvp-readiness-input-v1"
	ReceiptSchemaV1 = "agent-memory-final-mvp-readiness-receipt-v1"
	mappingVersion  = "agent-memory-final-mvp-mapping-v1"
)

type GateID string
type Outcome string

const (
	GateMVPA GateID = "MVP-A"
	GateMVPB GateID = "MVP-B"
	GateMVPC GateID = "MVP-C"
	GateMVPD GateID = "MVP-D"
	GateMVPE GateID = "MVP-E"
	GateMVPF GateID = "MVP-F"
	GateMVPG GateID = "MVP-G"
	GateMVPH GateID = "MVP-H"

	OutcomePassed       Outcome = "passed"
	OutcomeFailed       Outcome = "failed"
	OutcomeInconclusive Outcome = "inconclusive"
)

var (
	digestPattern       = regexp.MustCompile(`^[a-f0-9]{64}$`)
	opaquePattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:+-]{0,127}$`)
	canonicalControlIDs = []string{
		"P0.1-A", "P0.1-B", "P0.2-A", "P0.2-B", "P0.2-C", "CP0-A", "CP0-B",
		"P1.2-A", "P1.4-A", "P1.4-B", "P1.4-C", "CP1-A", "CP1-B", "CP1-C",
		"CP2-A", "CP2-B", "CP3-A", "CP3-B", "CP4-A", "CP4-B", "CP4-C",
		"CP5-A", "CP5-B", "CP5-C", "P6.5-A", "CP6-A", "P7.4-A", "CP7-A",
		"CP9-A", "CP9-B", "P10.1-A", "P10.2-A", "P10.2-B", "P10.3-A", "P10.3-B",
		"CP10-A", "CP10-B", "CP10-C", "P11.1-A", "P11.2-A", "P11.3-A", "P11.3-B",
		"P11.3-C", "CP11-A", "CP11-B", "CP11-C", "P12.2-A", "P12.2-B", "P12.2-C",
		"MVP-A", "MVP-B", "MVP-C", "MVP-D", "MVP-E", "MVP-F", "MVP-G", "MVP-H",
	}
	finalMVPControlIDs = []string{"MVP-A", "MVP-B", "MVP-C", "MVP-D", "MVP-E", "MVP-F", "MVP-G", "MVP-H"}
	gateDependencies   = map[GateID][]string{
		GateMVPB: {"CP3-A", "CP4-C", "CP7-A", "P10.1-A"},
		GateMVPC: {"CP2-A", "CP5-B", "P10.2-A", "P10.2-B"},
		GateMVPD: {"P0.2-B", "P0.2-C", "CP2-B", "CP3-B", "P6.5-A", "CP6-A", "P7.4-A", "CP7-A", "P10.3-A", "P12.2-B"},
		GateMVPE: {"CP0-B", "CP5-C", "CP10-C", "P11.2-A", "CP11-B", "P12.2-A"},
		GateMVPF: {"P0.1-A", "P0.1-B", "P0.2-C", "CP4-B", "P6.5-A", "CP6-A", "CP7-A"},
		GateMVPG: {"P1.2-A", "CP1-B", "P10.3-A", "P10.3-B", "CP10-C", "P11.1-A", "CP11-A", "CP11-C", "P12.2-C"},
		GateMVPH: {"P10.2-A", "P10.2-B", "CP10-B", "P11.3-C", "CP11-B", "P12.2-C"},
	}
)

type Input struct {
	Schema               string    `json:"schema"`
	Classification       string    `json:"classification"`
	Environment          string    `json:"environment"`
	ReadinessID          string    `json:"readiness_id"`
	ProgramVersion       string    `json:"program_version"`
	ReviewDecisionSHA256 string    `json:"review_decision_sha256"`
	GeneratedAt          time.Time `json:"generated_at"`
	ExpectedReady        bool      `json:"expected_ready"`
}

type Gate struct {
	ID                     GateID  `json:"id"`
	Outcome                Outcome `json:"outcome"`
	PrerequisiteCount      int     `json:"prerequisite_count"`
	VerifiedCount          int     `json:"verified_count"`
	MissingCount           int     `json:"missing_count"`
	RejectedOrExpiredCount int     `json:"rejected_or_expired_count"`
	EvidenceSHA256         string  `json:"evidence_sha256"`
}

type Receipt struct {
	Schema                          string    `json:"schema"`
	Classification                  string    `json:"classification"`
	Environment                     string    `json:"environment"`
	ReadinessID                     string    `json:"readiness_id"`
	ProgramVersion                  string    `json:"program_version"`
	MappingVersion                  string    `json:"mapping_version"`
	ReviewDecisionSHA256            string    `json:"review_decision_sha256"`
	InputSHA256                     string    `json:"input_sha256"`
	CatalogSHA256                   string    `json:"catalog_sha256"`
	IndexSHA256                     string    `json:"index_sha256"`
	TrustBundleSHA256               string    `json:"trust_bundle_sha256"`
	ApprovalSetSHA256               string    `json:"approval_set_sha256"`
	GeneratedAt                     time.Time `json:"generated_at"`
	CollectedAt                     time.Time `json:"collected_at"`
	Ready                           bool      `json:"ready"`
	CanonicalControlCount           int       `json:"canonical_control_count"`
	FoundationalControlCount        int       `json:"foundational_control_count"`
	FinalMVPControlCount            int       `json:"final_mvp_control_count"`
	VerifiedFoundationalCount       int       `json:"verified_foundational_count"`
	MissingFoundationalCount        int       `json:"missing_foundational_count"`
	RejectedFoundationalCount       int       `json:"rejected_foundational_count"`
	ExpiredFoundationalCount        int       `json:"expired_foundational_count"`
	UnavailableFoundationalControls []string  `json:"unavailable_foundational_controls"`
	Gates                           []Gate    `json:"gates"`
}

type sourceDigests struct{ catalog, index, trust, approvals string }

func CanonicalControlIDs() []string { return append([]string(nil), canonicalControlIDs...) }
func FinalMVPControlIDs() []string  { return append([]string(nil), finalMVPControlIDs...) }

func build(catalog evidenceindex.Catalog, index evidenceindex.Index, report evidenceindex.Report, input Input, inputDigest string, sources sourceDigests, now time.Time) (Receipt, error) {
	if err := validateIdentity(catalog, report, input, inputDigest, sources, now); err != nil {
		return Receipt{}, err
	}
	states, unavailable, missing, rejected, expired, err := verificationStates(report)
	if err != nil {
		return Receipt{}, err
	}
	entryDigests := make(map[string]string, len(index.Entries))
	for _, entry := range index.Entries {
		if strings.HasPrefix(entry.ControlID, "MVP-") {
			return Receipt{}, errors.New("MVP readiness index must not contain final MVP entries")
		}
		if _, duplicate := entryDigests[entry.ControlID]; duplicate || !digestPattern.MatchString(entry.EvidenceSHA256) {
			return Receipt{}, errors.New("MVP readiness index entries are invalid")
		}
		entryDigests[entry.ControlID] = entry.EvidenceSHA256
	}
	foundations := canonicalControlIDs[:len(canonicalControlIDs)-len(finalMVPControlIDs)]
	dependencies := make(map[GateID][]string, len(finalMVPControlIDs))
	dependencies[GateMVPA] = append([]string(nil), foundations...)
	for id, values := range gateDependencies {
		dependencies[id] = append([]string(nil), values...)
	}
	gates := make([]Gate, 0, len(finalMVPControlIDs))
	for _, rawID := range finalMVPControlIDs {
		gate, err := deriveGate(GateID(rawID), dependencies[GateID(rawID)], states, entryDigests)
		if err != nil {
			return Receipt{}, err
		}
		gates = append(gates, gate)
	}
	ready := len(unavailable) == 0
	for _, gate := range gates {
		ready = ready && gate.Outcome == OutcomePassed
	}
	if input.ExpectedReady != ready {
		return Receipt{}, errors.New("MVP expected readiness contradicts verified foundations")
	}
	return Receipt{
		Schema: ReceiptSchemaV1, Classification: input.Classification, Environment: input.Environment,
		ReadinessID: input.ReadinessID, ProgramVersion: input.ProgramVersion, MappingVersion: mappingVersion,
		ReviewDecisionSHA256: input.ReviewDecisionSHA256, InputSHA256: inputDigest,
		CatalogSHA256: sources.catalog, IndexSHA256: sources.index, TrustBundleSHA256: sources.trust, ApprovalSetSHA256: sources.approvals,
		GeneratedAt: input.GeneratedAt.UTC(), CollectedAt: now.UTC(), Ready: ready,
		CanonicalControlCount: len(canonicalControlIDs), FoundationalControlCount: len(foundations), FinalMVPControlCount: len(finalMVPControlIDs),
		VerifiedFoundationalCount: len(foundations) - len(unavailable), MissingFoundationalCount: missing,
		RejectedFoundationalCount: rejected, ExpiredFoundationalCount: expired,
		UnavailableFoundationalControls: unavailable, Gates: gates,
	}, nil
}

func validateIdentity(catalog evidenceindex.Catalog, report evidenceindex.Report, input Input, inputDigest string, sources sourceDigests, now time.Time) error {
	if catalog.Schema != evidenceindex.CatalogSchemaV1 || len(catalog.Controls) != len(canonicalControlIDs) {
		return errors.New("MVP readiness catalog is not canonical")
	}
	for position, control := range catalog.Controls {
		if control.ID != canonicalControlIDs[position] {
			return errors.New("MVP readiness catalog control set is not canonical")
		}
	}
	if report.Schema != evidenceindex.ReportSchemaV1 || report.Total != len(canonicalControlIDs) || report.Ready || report.Verified < 0 || report.Verified > 49 {
		return errors.New("MVP foundational verification report is invalid")
	}
	if input.Schema != InputSchemaV1 || input.Classification != "external_review" || input.Environment != "external" || !opaquePattern.MatchString(input.ReadinessID) || !opaquePattern.MatchString(input.ProgramVersion) || !digestPattern.MatchString(input.ReviewDecisionSHA256) {
		return errors.New("MVP readiness input identity is invalid")
	}
	if now.IsZero() || input.GeneratedAt.IsZero() || input.GeneratedAt.After(now) || input.GeneratedAt.Before(now.Add(-24*time.Hour)) {
		return errors.New("MVP readiness input timeline is invalid")
	}
	if !digestPattern.MatchString(inputDigest) || !digestPattern.MatchString(sources.catalog) || !digestPattern.MatchString(sources.index) || !digestPattern.MatchString(sources.trust) || !digestPattern.MatchString(sources.approvals) {
		return errors.New("MVP readiness source digest is invalid")
	}
	return nil
}

func verificationStates(report evidenceindex.Report) (map[string]string, []string, int, int, int, error) {
	states := make(map[string]string, len(canonicalControlIDs))
	for _, id := range canonicalControlIDs {
		states[id] = "verified"
	}
	counts := map[string]int{"missing": 0, "rejected": 0, "expired": 0}
	seen := map[string]struct{}{}
	for state, ids := range map[string][]string{"missing": report.Missing, "rejected": report.Rejected, "expired": report.Expired} {
		for _, id := range ids {
			if _, known := states[id]; !known {
				return nil, nil, 0, 0, 0, errors.New("MVP verification report contains unknown control")
			}
			if _, duplicate := seen[id]; duplicate {
				return nil, nil, 0, 0, 0, errors.New("MVP verification report duplicates a control state")
			}
			seen[id], states[id] = struct{}{}, state
		}
	}
	for _, id := range finalMVPControlIDs {
		if states[id] != "missing" {
			return nil, nil, 0, 0, 0, errors.New("MVP verification must be pre-final with all eight MVP controls missing")
		}
	}
	if report.Verified != len(canonicalControlIDs)-len(seen) {
		return nil, nil, 0, 0, 0, errors.New("MVP verification aggregate does not reconcile")
	}
	unavailable := make([]string, 0)
	for _, id := range canonicalControlIDs[:49] {
		if state := states[id]; state != "verified" {
			unavailable = append(unavailable, id)
			counts[state]++
		}
	}
	return states, unavailable, counts["missing"], counts["rejected"], counts["expired"], nil
}

func deriveGate(id GateID, dependencies []string, states, entryDigests map[string]string) (Gate, error) {
	if len(dependencies) == 0 {
		return Gate{}, fmt.Errorf("MVP gate %s has no prerequisites", id)
	}
	values := append([]string(nil), dependencies...)
	sort.Strings(values)
	gate := Gate{ID: id, Outcome: OutcomePassed, PrerequisiteCount: len(values)}
	tokens := make([]string, 0, len(values))
	for _, controlID := range values {
		state, known := states[controlID]
		if !known {
			return Gate{}, fmt.Errorf("MVP gate %s references unknown control", id)
		}
		switch state {
		case "verified":
			digest := entryDigests[controlID]
			if !digestPattern.MatchString(digest) {
				return Gate{}, fmt.Errorf("MVP gate %s lacks verified dossier digest", id)
			}
			gate.VerifiedCount++
			tokens = append(tokens, controlID+":verified:"+digest)
		case "missing":
			gate.MissingCount++
			gate.Outcome = OutcomeInconclusive
			tokens = append(tokens, controlID+":missing")
		case "rejected", "expired":
			gate.RejectedOrExpiredCount++
			gate.Outcome = OutcomeFailed
			tokens = append(tokens, controlID+":"+state)
		default:
			return Gate{}, fmt.Errorf("MVP gate %s has invalid control state", id)
		}
	}
	sum := sha256.Sum256([]byte(mappingVersion + "\n" + string(id) + "\n" + strings.Join(tokens, "\n")))
	gate.EvidenceSHA256 = fmt.Sprintf("%x", sum)
	return gate, nil
}
