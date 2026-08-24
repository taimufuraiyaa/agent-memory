package evidenceindex

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/readiness"
)

const maximumApprovalFiles = 512

func LoadTrustBundle(path string) (readiness.TrustBundle, error) {
	var value readiness.TrustBundle
	if err := decodeStrictRegular(path, maximumMetadataBytes, &value); err != nil {
		return readiness.TrustBundle{}, fmt.Errorf("load external evidence trust bundle: %w", err)
	}
	return value, nil
}

func LoadApprovalsDirectory(path string) ([]readiness.SignedApproval, error) {
	return loadApprovalsDirectoryWithHook(path, nil)
}

// CanonicalFileVerification is the result of one stable, path-based external
// evidence decision. Its digests identify the exact source bytes used.
type CanonicalFileVerification struct {
	Catalog           Catalog
	Index             Index
	Report            Report
	CatalogSHA256     string
	IndexSHA256       string
	TrustBundleSHA256 string
	ApprovalSetSHA256 string
}

func VerifyCanonicalFiles(catalogPath, indexPath, artifactRoot, trustPath, approvalsPath string, now time.Time) (CanonicalFileVerification, error) {
	return verifyCanonicalFilesWithHook(catalogPath, indexPath, artifactRoot, trustPath, approvalsPath, now, nil)
}

func verifyCanonicalFilesWithHook(catalogPath, indexPath, artifactRoot, trustPath, approvalsPath string, now time.Time, afterSources func()) (CanonicalFileVerification, error) {
	return verifyCanonicalFilesWithHooks(catalogPath, indexPath, artifactRoot, trustPath, approvalsPath, now, afterSources, nil)
}

func verifyCanonicalFilesWithHooks(catalogPath, indexPath, artifactRoot, trustPath, approvalsPath string, now time.Time, beforeFirstDossier func(), afterDossier func(int)) (CanonicalFileVerification, error) {
	return verifyCanonicalFilesWithAllHooks(catalogPath, indexPath, artifactRoot, trustPath, approvalsPath, now, beforeFirstDossier, afterDossier, nil)
}

func verifyCanonicalFilesWithFinalizationHook(catalogPath, indexPath, artifactRoot, trustPath, approvalsPath string, now time.Time, afterSourceRevalidation func()) (CanonicalFileVerification, error) {
	return verifyCanonicalFilesWithAllHooks(catalogPath, indexPath, artifactRoot, trustPath, approvalsPath, now, nil, nil, afterSourceRevalidation)
}

func verifyCanonicalFilesWithAllHooks(catalogPath, indexPath, artifactRootPath, trustPath, approvalsPath string, now time.Time, beforeFirstDossier func(), afterDossier func(int), afterSourceRevalidation func()) (CanonicalFileVerification, error) {
	var catalog Catalog
	catalogSnapshot, catalogDigest, err := decodeStrictRegularSnapshot(catalogPath, maximumMetadataBytes, &catalog, nil, nil)
	if err != nil {
		return CanonicalFileVerification{}, fmt.Errorf("load external control catalog: %w", err)
	}
	if err := validateCanonicalCatalog(catalog); err != nil {
		return CanonicalFileVerification{}, err
	}
	var index Index
	indexSnapshot, indexDigest, err := decodeStrictRegularSnapshot(indexPath, maximumMetadataBytes, &index, nil, nil)
	if err != nil {
		return CanonicalFileVerification{}, fmt.Errorf("load external evidence index: %w", err)
	}
	var bundle readiness.TrustBundle
	trustSnapshot, trustDigest, err := decodeStrictRegularSnapshot(trustPath, maximumMetadataBytes, &bundle, nil, nil)
	if err != nil {
		return CanonicalFileVerification{}, fmt.Errorf("load external evidence trust bundle: %w", err)
	}
	approvals, approvalDigest, approvalSnapshot, err := loadApprovalDirectorySnapshot(approvalsPath, nil)
	if err != nil {
		return CanonicalFileVerification{}, err
	}
	finalizer := func(root *artifactRoot, dossierSnapshots map[string]dossierFileSnapshot) error {
		for _, snapshot := range []regularFileSnapshot{catalogSnapshot, indexSnapshot, trustSnapshot} {
			if err := snapshot.validate(); err != nil {
				return err
			}
		}
		if err := approvalSnapshot.validate(); err != nil {
			return err
		}
		if afterSourceRevalidation != nil {
			afterSourceRevalidation()
		}
		for _, snapshot := range dossierSnapshots {
			if err := snapshot.validate(root); err != nil {
				return err
			}
		}
		return root.validate()
	}
	report, err := verifyCanonicalWithDossierHooksAndFinalizer(catalog, index, artifactRootPath, bundle, approvals, now, beforeFirstDossier, afterDossier, finalizer)
	if err != nil {
		return CanonicalFileVerification{}, err
	}
	return CanonicalFileVerification{
		Catalog: catalog, Index: index, Report: report,
		CatalogSHA256: catalogDigest, IndexSHA256: indexDigest,
		TrustBundleSHA256: trustDigest, ApprovalSetSHA256: approvalDigest,
	}, nil
}

type approvalFileSnapshot struct {
	name string
	path string
	info os.FileInfo
}

func loadApprovalsDirectoryWithHook(path string, afterSnapshot func()) ([]readiness.SignedApproval, error) {
	result, _, _, err := loadApprovalDirectorySnapshot(path, afterSnapshot)
	return result, err
}

type approvalDirectorySnapshot struct {
	path      string
	directory os.FileInfo
	files     []approvalFileSnapshot
}

func (snapshot approvalDirectorySnapshot) validate() error {
	currentDirectoryInfo, err := os.Lstat(snapshot.path)
	if err != nil || !currentDirectoryInfo.IsDir() || currentDirectoryInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(snapshot.directory, currentDirectoryInfo) {
		return errors.New("external evidence approvals directory changed during verification")
	}
	currentFiles, err := snapshotApprovalDirectory(snapshot.path)
	if err != nil || !sameApprovalDirectorySnapshot(snapshot.files, currentFiles) {
		return errors.New("external evidence approval set changed during verification")
	}
	return nil
}

func loadApprovalDirectorySnapshot(path string, afterSnapshot func()) ([]readiness.SignedApproval, string, approvalDirectorySnapshot, error) {
	directoryInfo, err := os.Lstat(path)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return nil, "", approvalDirectorySnapshot{}, errors.New("external evidence approvals must be a non-symlink directory")
	}
	snapshot, err := snapshotApprovalDirectory(path)
	if err != nil {
		return nil, "", approvalDirectorySnapshot{}, err
	}
	if afterSnapshot != nil {
		afterSnapshot()
	}
	result := make([]readiness.SignedApproval, 0, len(snapshot))
	hash := sha256.New()
	for _, artifact := range snapshot {
		var approval readiness.SignedApproval
		_, digest, err := decodeStrictRegularSnapshot(artifact.path, maximumMetadataBytes, &approval, artifact.info, nil)
		if err != nil {
			return nil, "", approvalDirectorySnapshot{}, errors.New("load external evidence approval artifact")
		}
		result = append(result, approval)
		fmt.Fprintf(hash, "%s\x00%s\n", artifact.name, digest)
	}
	state := approvalDirectorySnapshot{path: path, directory: directoryInfo, files: snapshot}
	if err := state.validate(); err != nil {
		return nil, "", approvalDirectorySnapshot{}, err
	}
	return result, fmt.Sprintf("%x", hash.Sum(nil)), state, nil
}

func snapshotApprovalDirectory(path string) ([]approvalFileSnapshot, error) {
	items, err := os.ReadDir(path)
	if err != nil || len(items) == 0 || len(items) > maximumApprovalFiles {
		return nil, errors.New("read external evidence approvals directory")
	}
	snapshot := make([]approvalFileSnapshot, 0, len(items))
	for _, item := range items {
		if item.IsDir() || item.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(item.Name(), ".json") || item.Name() == ".json" {
			return nil, errors.New("external evidence approval directory contains an invalid entry")
		}
		artifactPath := filepath.Join(path, item.Name())
		info, err := os.Lstat(artifactPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumMetadataBytes {
			return nil, errors.New("external evidence approval directory contains an invalid entry")
		}
		snapshot = append(snapshot, approvalFileSnapshot{name: item.Name(), path: artifactPath, info: info})
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].name < snapshot[j].name })
	return snapshot, nil
}

func sameApprovalDirectorySnapshot(left, right []approvalFileSnapshot) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].name != right[index].name || left[index].info.Size() != right[index].info.Size() || !left[index].info.ModTime().Equal(right[index].info.ModTime()) || !os.SameFile(left[index].info, right[index].info) {
			return false
		}
	}
	return true
}
