package evidenceindex

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("external evidence approvals must be a non-symlink directory")
	}
	items, err := os.ReadDir(path)
	if err != nil || len(items) > maximumApprovalFiles {
		return nil, errors.New("read external evidence approvals directory")
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name() < items[j].Name() })
	result := make([]readiness.SignedApproval, 0, len(items))
	for _, item := range items {
		if item.IsDir() || item.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(item.Name(), ".json") || item.Name() == ".json" {
			return nil, errors.New("external evidence approval directory contains an invalid entry")
		}
		var approval readiness.SignedApproval
		if err := decodeStrictRegular(filepath.Join(path, item.Name()), maximumMetadataBytes, &approval); err != nil {
			return nil, errors.New("load external evidence approval artifact")
		}
		result = append(result, approval)
	}
	return result, nil
}
