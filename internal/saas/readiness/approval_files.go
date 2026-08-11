package readiness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxApprovalFileBytes = 1 << 20
	maxApprovalFiles     = 256
)

func LoadTrustBundle(path string) (TrustBundle, error) {
	var bundle TrustBundle
	if err := decodeStrictJSONFile(path, &bundle); err != nil {
		return TrustBundle{}, fmt.Errorf("load approval trust bundle: %w", err)
	}
	if _, err := validateTrustBundle(bundle); err != nil {
		return TrustBundle{}, err
	}
	return bundle, nil
}

func LoadApprovals(directory string) ([]SignedApproval, error) {
	return loadApprovalsWithHook(directory, nil)
}

type approvalFileSnapshot struct {
	name string
	path string
	info os.FileInfo
}

func loadApprovalsWithHook(directory string, afterSnapshot func()) ([]SignedApproval, error) {
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("approval artifact directory is invalid")
	}
	snapshot, err := snapshotApprovalFiles(directory)
	if err != nil {
		return nil, err
	}
	if afterSnapshot != nil {
		afterSnapshot()
	}
	approvals := make([]SignedApproval, 0, len(snapshot))
	for _, artifact := range snapshot {
		var approval SignedApproval
		if err := decodeStrictJSONFileExpected(artifact.path, &approval, artifact.info, nil); err != nil {
			return nil, fmt.Errorf("load approval artifact %q: %w", artifact.name, err)
		}
		approvals = append(approvals, approval)
	}
	currentDirectoryInfo, err := os.Lstat(directory)
	if err != nil || !os.SameFile(directoryInfo, currentDirectoryInfo) || !currentDirectoryInfo.IsDir() || currentDirectoryInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("approval artifact directory changed during verification")
	}
	currentSnapshot, err := snapshotApprovalFiles(directory)
	if err != nil || !sameApprovalFileSnapshot(snapshot, currentSnapshot) {
		return nil, errors.New("approval artifact set changed during verification")
	}
	return approvals, nil
}

func snapshotApprovalFiles(directory string) ([]approvalFileSnapshot, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, errors.New("read approval artifact directory")
	}
	snapshot := make([]approvalFileSnapshot, 0, len(entries))
	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxApprovalFileBytes {
			return nil, errors.New("approval artifact must be a regular JSON file")
		}
		snapshot = append(snapshot, approvalFileSnapshot{name: entry.Name(), path: path, info: info})
	}
	if len(snapshot) == 0 || len(snapshot) > maxApprovalFiles {
		return nil, errors.New("approval artifact count is outside the allowed range")
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].name < snapshot[j].name })
	return snapshot, nil
}

func sameApprovalFileSnapshot(left, right []approvalFileSnapshot) bool {
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

func decodeStrictJSONFile(path string, destination any) error {
	return decodeStrictJSONFileExpected(path, destination, nil, nil)
}

func decodeStrictJSONFileWithHook(path string, destination any, afterOpen func()) error {
	return decodeStrictJSONFileExpected(path, destination, nil, afterOpen)
}

func decodeStrictJSONFileExpected(path string, destination any, expected os.FileInfo, afterOpen func()) error {
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || (expected != nil && (!os.SameFile(expected, validated) || expected.Size() != validated.Size() || !expected.ModTime().Equal(validated.ModTime()))) {
		return errors.New("evidence file is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("read evidence file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || validateOpenedEvidenceFile(validated, opened) != nil {
		return errors.New("evidence file is invalid")
	}
	if afterOpen != nil {
		afterOpen()
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxApprovalFileBytes+1))
	if err != nil || int64(len(contents)) != opened.Size() {
		return errors.New("read evidence file")
	}
	openedAfterRead, err := file.Stat()
	if err != nil || !os.SameFile(opened, openedAfterRead) || opened.Size() != openedAfterRead.Size() || !opened.ModTime().Equal(openedAfterRead.ModTime()) {
		return errors.New("evidence file changed during read")
	}
	pathAfterRead, err := os.Lstat(path)
	if err != nil || !pathAfterRead.Mode().IsRegular() || !os.SameFile(opened, pathAfterRead) || opened.Size() != pathAfterRead.Size() || !opened.ModTime().Equal(pathAfterRead.ModTime()) {
		return errors.New("evidence file changed during read")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("decode evidence JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("evidence JSON contains trailing data")
	}
	return nil
}

func validateOpenedEvidenceFile(validated, opened os.FileInfo) error {
	if validated == nil || opened == nil || !validated.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || validated.Size() != opened.Size() || !validated.ModTime().Equal(opened.ModTime()) || opened.Size() <= 0 || opened.Size() > maxApprovalFileBytes {
		return errors.New("evidence file identity or size is invalid")
	}
	return nil
}
