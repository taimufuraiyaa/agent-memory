package readiness

import (
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
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("approval artifact directory is invalid")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, errors.New("read approval artifact directory")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return nil, errors.New("approval artifact must be a regular JSON file")
		}
		names = append(names, entry.Name())
	}
	if len(names) == 0 || len(names) > maxApprovalFiles {
		return nil, errors.New("approval artifact count is outside the allowed range")
	}
	sort.Strings(names)
	approvals := make([]SignedApproval, 0, len(names))
	for _, name := range names {
		var approval SignedApproval
		if err := decodeStrictJSONFile(filepath.Join(directory, name), &approval); err != nil {
			return nil, fmt.Errorf("load approval artifact %q: %w", name, err)
		}
		approvals = append(approvals, approval)
	}
	return approvals, nil
}

func decodeStrictJSONFile(path string, destination any) error {
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() {
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
	decoder := json.NewDecoder(io.LimitReader(file, maxApprovalFileBytes+1))
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
	if validated == nil || opened == nil || !validated.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(validated, opened) || opened.Size() <= 0 || opened.Size() > maxApprovalFileBytes {
		return errors.New("evidence file identity or size is invalid")
	}
	return nil
}
