// Package evidenceindex verifies externally retained P0-P12 evidence dossiers.
package evidenceindex

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
)

const (
	CatalogSchemaV1        = "agent-memory-external-control-catalog-v1"
	IndexSchemaV1          = "agent-memory-external-evidence-index-v1"
	ReportSchemaV1         = "agent-memory-external-evidence-report-v1"
	ExternalEvidenceGate   = "external_evidence"
	canonicalCatalogSHA256 = "b834c9a410ec3355e988ffb8a4f939c882757fea501a62bd9fc73e5b4ecd9232"
	maximumMetadataBytes   = 4 << 20
	maximumDossierBytes    = 1 << 30
)

type Classification string
type Environment string

const (
	ExternalStaging     Classification = "external_staging"
	ExternalProduction  Classification = "external_production"
	ExternalReview      Classification = "external_review"
	ExternalBusiness    Classification = "external_business"
	ExternalObservation Classification = "external_observation"

	Staging    Environment = "staging"
	Production Environment = "production"
	External   Environment = "external"
)

type Catalog struct {
	Schema   string    `json:"schema"`
	Controls []Control `json:"controls"`
}

type Control struct {
	ID                  string `json:"id"`
	ApprovalControl     string `json:"approval_control"`
	OwnerGroup          string `json:"owner_group"`
	EvidenceRequirement string `json:"evidence_requirement"`
}

type Index struct {
	Schema      string    `json:"schema"`
	Gate        string    `json:"gate"`
	GeneratedAt time.Time `json:"generated_at"`
	Entries     []Entry   `json:"entries"`
}

type Entry struct {
	ControlID       string         `json:"control_id"`
	ApprovalControl string         `json:"approval_control"`
	DossierPath     string         `json:"dossier_path"`
	EvidenceRef     string         `json:"evidence_ref"`
	EvidenceSHA256  string         `json:"evidence_sha256"`
	Classification  Classification `json:"classification"`
	Environment     Environment    `json:"environment"`
	ReleaseID       string         `json:"release_id,omitempty"`
	CollectedAt     time.Time      `json:"collected_at"`
	WindowStart     *time.Time     `json:"window_start,omitempty"`
	WindowEnd       *time.Time     `json:"window_end,omitempty"`
}

type Report struct {
	Schema   string   `json:"schema"`
	Ready    bool     `json:"ready"`
	Total    int      `json:"total"`
	Verified int      `json:"verified"`
	Missing  []string `json:"missing"`
	Rejected []string `json:"rejected"`
	Expired  []string `json:"expired"`
}

var (
	controlIDPattern   = regexp.MustCompile(`^(P[0-9]+\.[0-9]+-[A-Z]|CP[0-9]+-[A-Z]|MVP-[A-Z])$`)
	approvalPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	ownerPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
	releasePattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	evidenceRefPattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://[^\s\x00-\x1f]{1,240}$`)
)

func LoadCatalog(path string) (Catalog, error) {
	var value Catalog
	if err := decodeStrictRegular(path, maximumMetadataBytes, &value); err != nil {
		return Catalog{}, fmt.Errorf("load external control catalog: %w", err)
	}
	if err := validateCatalog(value); err != nil {
		return Catalog{}, err
	}
	return value, nil
}

func LoadIndex(path string) (Index, error) {
	var value Index
	if err := decodeStrictRegular(path, maximumMetadataBytes, &value); err != nil {
		return Index{}, fmt.Errorf("load external evidence index: %w", err)
	}
	return value, nil
}

func verify(catalog Catalog, index Index, artifactRoot string, bundle readiness.TrustBundle, approvals []readiness.SignedApproval, now time.Time) (Report, error) {
	return verifyWithDossierHooks(catalog, index, artifactRoot, bundle, approvals, now, nil, nil)
}

func verifyWithDossierHook(catalog Catalog, index Index, artifactRoot string, bundle readiness.TrustBundle, approvals []readiness.SignedApproval, now time.Time, beforeFirstDossier func()) (Report, error) {
	return verifyWithDossierHooks(catalog, index, artifactRoot, bundle, approvals, now, beforeFirstDossier, nil)
}

func verifyWithDossierHooks(catalog Catalog, index Index, artifactRoot string, bundle readiness.TrustBundle, approvals []readiness.SignedApproval, now time.Time, beforeFirstDossier func(), afterDossier func(int)) (Report, error) {
	return verifyWithDossierHooksAndFinalizer(catalog, index, artifactRoot, bundle, approvals, now, beforeFirstDossier, afterDossier, nil)
}

type verificationFinalizer func(*artifactRoot, map[string]dossierFileSnapshot) error

func verifyWithDossierHooksAndFinalizer(catalog Catalog, index Index, artifactRoot string, bundle readiness.TrustBundle, approvals []readiness.SignedApproval, now time.Time, beforeFirstDossier func(), afterDossier func(int), finalizer verificationFinalizer) (Report, error) {
	report := Report{Schema: ReportSchemaV1, Missing: []string{}, Rejected: []string{}, Expired: []string{}}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if err := validateCatalog(catalog); err != nil {
		return report, err
	}
	report.Total = len(catalog.Controls)
	if index.Schema != IndexSchemaV1 || index.Gate != ExternalEvidenceGate || index.GeneratedAt.IsZero() || index.GeneratedAt.After(now.Add(5*time.Minute)) {
		return report, errors.New("external evidence index identity is invalid")
	}
	if len(index.Entries) > len(catalog.Controls) {
		return report, errors.New("external evidence index has too many entries")
	}
	root, err := openArtifactRoot(artifactRoot)
	if err != nil {
		return report, err
	}
	defer root.handle.Close()
	byID := make(map[string]Control, len(catalog.Controls))
	approvalToID := make(map[string]string, len(catalog.Controls))
	required := make([]string, 0, len(catalog.Controls))
	for _, control := range catalog.Controls {
		byID[control.ID] = control
		approvalToID[control.ApprovalControl] = control.ID
		required = append(required, control.ApprovalControl)
	}
	entries := make(map[string]Entry, len(index.Entries))
	for _, entry := range index.Entries {
		control, ok := byID[entry.ControlID]
		if !ok || entry.ApprovalControl != control.ApprovalControl {
			return report, errors.New("external evidence entry targets an unknown control")
		}
		if _, duplicate := entries[entry.ControlID]; duplicate {
			return report, errors.New("external evidence control is duplicated")
		}
		if err := validateEntry(entry, now); err != nil {
			return report, fmt.Errorf("external evidence control %s: %w", entry.ControlID, err)
		}
		entries[entry.ControlID] = entry
	}
	approvalReport, err := readiness.VerifyApprovals(ExternalEvidenceGate, required, bundle, approvals, now)
	if err != nil {
		return report, fmt.Errorf("verify external evidence approvals: %w", err)
	}
	missingApprovals := mappedSet(approvalReport.Missing, approvalToID)
	rejectedApprovals := mappedSet(approvalReport.Rejected, approvalToID)
	expiredApprovals := mappedSet(approvalReport.Expired, approvalToID)
	missing := map[string]struct{}{}
	rejected := map[string]struct{}{}
	expired := map[string]struct{}{}
	dossierSnapshots := make(map[string]dossierFileSnapshot, len(index.Entries))
	for _, control := range catalog.Controls {
		entry, exists := entries[control.ID]
		if !exists || missingApprovals[control.ID] || rejectedApprovals[control.ID] || expiredApprovals[control.ID] {
			continue
		}
		verified, ok := approvalReport.Verified[control.ApprovalControl]
		if !ok || verified.EvidenceRef != entry.EvidenceRef || verified.EvidenceSHA256 != entry.EvidenceSHA256 {
			return report, fmt.Errorf("external evidence control %s approval does not bind the indexed dossier", control.ID)
		}
		snapshot, err := captureDossierSnapshot(root, entry.DossierPath, nil)
		if err != nil {
			return report, fmt.Errorf("external evidence control %s dossier is invalid", control.ID)
		}
		dossierSnapshots[control.ID] = snapshot
	}
	dossierHook := beforeFirstDossier
	dossierPosition := 0
	for _, control := range catalog.Controls {
		entry, exists := entries[control.ID]
		if !exists {
			missing[control.ID] = struct{}{}
			continue
		}
		if missingApprovals[control.ID] {
			missing[control.ID] = struct{}{}
			continue
		}
		if rejectedApprovals[control.ID] {
			rejected[control.ID] = struct{}{}
			continue
		}
		if expiredApprovals[control.ID] {
			expired[control.ID] = struct{}{}
			continue
		}
		if dossierHook != nil {
			dossierHook()
			dossierHook = nil
		}
		digest, err := hashDossierSnapshotAtRoot(root, dossierSnapshots[control.ID], nil)
		if err != nil {
			return report, fmt.Errorf("external evidence control %s dossier is invalid", control.ID)
		}
		if digest != entry.EvidenceSHA256 {
			return report, fmt.Errorf("external evidence control %s dossier digest changed", control.ID)
		}
		report.Verified++
		if afterDossier != nil {
			afterDossier(dossierPosition)
		}
		dossierPosition++
	}
	for _, control := range catalog.Controls {
		if snapshot, ok := dossierSnapshots[control.ID]; ok {
			if err := snapshot.validate(root); err != nil {
				return report, fmt.Errorf("external evidence control %s dossier set changed", control.ID)
			}
		}
	}
	if err := root.validate(); err != nil {
		return report, err
	}
	if finalizer != nil {
		if err := finalizer(root, dossierSnapshots); err != nil {
			return report, err
		}
	}
	report.Missing = sortedSet(missing)
	report.Rejected = sortedSet(rejected)
	report.Expired = sortedSet(expired)
	report.Ready = report.Verified == report.Total && len(report.Missing) == 0 && len(report.Rejected) == 0 && len(report.Expired) == 0
	return report, nil
}

func VerifyCanonical(catalog Catalog, index Index, artifactRoot string, bundle readiness.TrustBundle, approvals []readiness.SignedApproval, now time.Time) (Report, error) {
	return verifyCanonicalWithDossierHook(catalog, index, artifactRoot, bundle, approvals, now, nil)
}

func verifyCanonicalWithDossierHook(catalog Catalog, index Index, artifactRoot string, bundle readiness.TrustBundle, approvals []readiness.SignedApproval, now time.Time, beforeFirstDossier func()) (Report, error) {
	return verifyCanonicalWithDossierHooks(catalog, index, artifactRoot, bundle, approvals, now, beforeFirstDossier, nil)
}

func verifyCanonicalWithDossierHooks(catalog Catalog, index Index, artifactRoot string, bundle readiness.TrustBundle, approvals []readiness.SignedApproval, now time.Time, beforeFirstDossier func(), afterDossier func(int)) (Report, error) {
	return verifyCanonicalWithDossierHooksAndFinalizer(catalog, index, artifactRoot, bundle, approvals, now, beforeFirstDossier, afterDossier, nil)
}

func verifyCanonicalWithDossierHooksAndFinalizer(catalog Catalog, index Index, artifactRoot string, bundle readiness.TrustBundle, approvals []readiness.SignedApproval, now time.Time, beforeFirstDossier func(), afterDossier func(int), finalizer verificationFinalizer) (Report, error) {
	if err := validateCanonicalCatalog(catalog); err != nil {
		return Report{Schema: ReportSchemaV1, Missing: []string{}, Rejected: []string{}, Expired: []string{}}, err
	}
	return verifyWithDossierHooksAndFinalizer(catalog, index, artifactRoot, bundle, approvals, now, beforeFirstDossier, afterDossier, finalizer)
}

func validateCanonicalCatalog(catalog Catalog) error {
	if err := validateCatalog(catalog); err != nil {
		return err
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		return errors.New("marshal external control catalog")
	}
	digest := sha256.Sum256(encoded)
	if hex.EncodeToString(digest[:]) != canonicalCatalogSHA256 {
		return errors.New("external control catalog is not canonical")
	}
	return nil
}

func validateCatalog(catalog Catalog) error {
	if catalog.Schema != CatalogSchemaV1 || len(catalog.Controls) == 0 || len(catalog.Controls) > 256 {
		return errors.New("external control catalog is invalid")
	}
	ids := map[string]struct{}{}
	approvals := map[string]struct{}{}
	for _, control := range catalog.Controls {
		if !controlIDPattern.MatchString(control.ID) || !approvalPattern.MatchString(control.ApprovalControl) || !ownerPattern.MatchString(control.OwnerGroup) || strings.TrimSpace(control.EvidenceRequirement) == "" || len(control.EvidenceRequirement) > 500 {
			return errors.New("external control catalog entry is invalid")
		}
		if _, exists := ids[control.ID]; exists {
			return errors.New("external control catalog ID is duplicated")
		}
		if _, exists := approvals[control.ApprovalControl]; exists {
			return errors.New("external approval control is duplicated")
		}
		ids[control.ID] = struct{}{}
		approvals[control.ApprovalControl] = struct{}{}
	}
	return nil
}

func validateEntry(entry Entry, now time.Time) error {
	if !evidenceRefPattern.MatchString(entry.EvidenceRef) || !validDigest(entry.EvidenceSHA256) || !validDossierRelativePath(entry.DossierPath) {
		return errors.New("dossier reference is invalid")
	}
	if !validClassification(entry.Classification) || !validEnvironment(entry.Environment) || entry.CollectedAt.IsZero() || entry.CollectedAt.After(now.Add(5*time.Minute)) {
		return errors.New("classification or collection time is invalid")
	}
	if entry.Classification == ExternalStaging && entry.Environment != Staging || entry.Classification == ExternalProduction && entry.Environment != Production {
		return errors.New("classification does not match environment")
	}
	if entry.ReleaseID != "" && !releasePattern.MatchString(entry.ReleaseID) {
		return errors.New("release ID is invalid")
	}
	if (entry.WindowStart == nil) != (entry.WindowEnd == nil) {
		return errors.New("observation window is incomplete")
	}
	if entry.WindowStart != nil && (!entry.WindowEnd.After(*entry.WindowStart) || entry.CollectedAt.Before(*entry.WindowEnd)) {
		return errors.New("observation window is invalid")
	}
	return nil
}

func validClassification(value Classification) bool {
	switch value {
	case ExternalStaging, ExternalProduction, ExternalReview, ExternalBusiness, ExternalObservation:
		return true
	default:
		return false
	}
}

func validEnvironment(value Environment) bool {
	return value == Staging || value == Production || value == External
}

func validDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validDossierRelativePath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.ContainsRune(value, '\x00') || filepath.Separator != '/' && strings.ContainsRune(value, filepath.Separator) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	return clean == value && strings.HasPrefix(value, "artifacts/") && value != "artifacts"
}

type artifactRoot struct {
	path   string
	handle *os.Root
	info   os.FileInfo
}

func openArtifactRoot(root string) (*artifactRoot, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, errors.New("external evidence artifact root is invalid")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("external evidence artifact root must be a non-symlink directory")
	}
	handle, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, errors.New("open external evidence artifact root")
	}
	opened, err := handle.Lstat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(info, opened) {
		handle.Close()
		return nil, errors.New("external evidence artifact root changed before open")
	}
	return &artifactRoot{path: absolute, handle: handle, info: opened}, nil
}

func (root *artifactRoot) validate() error {
	opened, err := root.handle.Lstat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(root.info, opened) {
		return errors.New("external evidence artifact root changed while hashing")
	}
	current, err := os.Lstat(root.path)
	if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(root.info, current) {
		return errors.New("external evidence artifact root changed while hashing")
	}
	return nil
}

func hashDossier(root, relative string) (string, error) {
	return hashDossierWithHook(root, relative, nil)
}

func hashDossierWithHook(root, relative string, afterOpen func()) (string, error) {
	return hashDossierWithHooks(root, relative, nil, afterOpen)
}

func hashDossierWithHooks(root, relative string, afterComponents, afterOpen func()) (string, error) {
	anchored, err := openArtifactRoot(root)
	if err != nil {
		return "", err
	}
	defer anchored.handle.Close()
	digest, err := hashDossierAtRoot(anchored, relative, afterComponents, afterOpen)
	if err != nil {
		return "", err
	}
	if err := anchored.validate(); err != nil {
		return "", err
	}
	return digest, nil
}

func hashDossierAtRoot(root *artifactRoot, relative string, afterComponents, afterOpen func()) (string, error) {
	snapshot, err := captureDossierSnapshot(root, relative, afterComponents)
	if err != nil {
		return "", err
	}
	return hashDossierSnapshotAtRoot(root, snapshot, afterOpen)
}

type dossierFileSnapshot struct {
	relative string
	name     string
	info     os.FileInfo
}

func captureDossierSnapshot(root *artifactRoot, relative string, afterComponents func()) (dossierFileSnapshot, error) {
	if !validDossierRelativePath(relative) {
		return dossierFileSnapshot{}, errors.New("invalid dossier path")
	}
	if err := validateDossierDirectories(root.handle, relative); err != nil {
		return dossierFileSnapshot{}, err
	}
	if afterComponents != nil {
		afterComponents()
	}
	name := filepath.FromSlash(relative)
	validated, err := root.handle.Lstat(name)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximumDossierBytes {
		return dossierFileSnapshot{}, errors.New("dossier must be a bounded regular file")
	}
	if err := validateDossierDirectories(root.handle, relative); err != nil {
		return dossierFileSnapshot{}, err
	}
	return dossierFileSnapshot{relative: relative, name: name, info: validated}, nil
}

func (snapshot dossierFileSnapshot) validate(root *artifactRoot) error {
	if err := validateDossierDirectories(root.handle, snapshot.relative); err != nil {
		return err
	}
	current, err := root.handle.Lstat(snapshot.name)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(snapshot.info, current) || current.Size() != snapshot.info.Size() || !current.ModTime().Equal(snapshot.info.ModTime()) {
		return errors.New("dossier set changed during verification")
	}
	return nil
}

func hashDossierSnapshotAtRoot(root *artifactRoot, snapshot dossierFileSnapshot, afterOpen func()) (string, error) {
	if err := snapshot.validate(root); err != nil {
		return "", err
	}
	file, err := root.handle.Open(snapshot.name)
	if err != nil {
		return "", errors.New("open dossier")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(snapshot.info, opened) || opened.Size() != snapshot.info.Size() || !opened.ModTime().Equal(snapshot.info.ModTime()) {
		return "", errors.New("dossier changed before open")
	}
	if afterOpen != nil {
		afterOpen()
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, maximumDossierBytes+1))
	if err != nil || written != opened.Size() {
		return "", errors.New("hash dossier")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(snapshot.info, after) || after.Size() != written || !after.ModTime().Equal(snapshot.info.ModTime()) {
		return "", errors.New("dossier changed while hashing")
	}
	if err := snapshot.validate(root); err != nil {
		return "", errors.New("dossier changed while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validateDossierDirectories(root *os.Root, relative string) error {
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	current := ""
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("dossier directory is invalid")
		}
	}
	return nil
}

func decodeStrictRegular(path string, maximum int64, destination any) error {
	_, _, err := decodeStrictRegularSnapshot(path, maximum, destination, nil, nil)
	return err
}

func decodeStrictRegularWithHook(path string, maximum int64, destination any, afterOpen func()) error {
	_, _, err := decodeStrictRegularSnapshot(path, maximum, destination, nil, afterOpen)
	return err
}

func decodeStrictRegularExpected(path string, maximum int64, destination any, expected os.FileInfo, afterOpen func()) error {
	_, _, err := decodeStrictRegularSnapshot(path, maximum, destination, expected, afterOpen)
	return err
}

type regularFileSnapshot struct {
	path string
	info os.FileInfo
}

func (snapshot regularFileSnapshot) validate() error {
	current, err := os.Lstat(snapshot.path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(snapshot.info, current) || current.Size() != snapshot.info.Size() || !current.ModTime().Equal(snapshot.info.ModTime()) {
		return errors.New("metadata source changed during verification")
	}
	return nil
}

func decodeStrictRegularSnapshot(path string, maximum int64, destination any, expected os.FileInfo, afterOpen func()) (regularFileSnapshot, string, error) {
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximum || (expected != nil && (!os.SameFile(expected, validated) || expected.Size() != validated.Size() || !expected.ModTime().Equal(validated.ModTime()))) {
		return regularFileSnapshot{}, "", errors.New("metadata must be a bounded regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return regularFileSnapshot{}, "", errors.New("open metadata")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(validated, opened) || !opened.Mode().IsRegular() || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return regularFileSnapshot{}, "", errors.New("metadata changed before open")
	}
	if afterOpen != nil {
		afterOpen()
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(contents)) != opened.Size() {
		return regularFileSnapshot{}, "", errors.New("read metadata")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return regularFileSnapshot{}, "", errors.New("metadata changed while reading")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || pathAfterRead.Size() != opened.Size() || !pathAfterRead.ModTime().Equal(opened.ModTime()) {
		return regularFileSnapshot{}, "", errors.New("metadata changed while reading")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return regularFileSnapshot{}, "", errors.New("decode metadata")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return regularFileSnapshot{}, "", errors.New("metadata contains trailing data")
	}
	digest := sha256.Sum256(contents)
	return regularFileSnapshot{path: path, info: opened}, hex.EncodeToString(digest[:]), nil
}

func mappedSet(values []string, mapping map[string]string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		if id := mapping[value]; id != "" {
			result[id] = true
		}
	}
	return result
}

func sortedSet(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
