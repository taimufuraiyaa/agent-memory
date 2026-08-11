package mvpreadinessevidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidenceindex"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/evidencepublish"
)

const maximumInputBytes = 64 << 10

func Collect(catalogPath, indexPath, artifactRoot, trustPath, approvalsDirectory, inputPath string, now time.Time) (Receipt, error) {
	verification, err := evidenceindex.VerifyCanonicalFiles(catalogPath, indexPath, artifactRoot, trustPath, approvalsDirectory, now)
	if err != nil {
		return Receipt{}, err
	}
	var input Input
	inputDigest, err := decodeInput(inputPath, &input)
	if err != nil {
		return Receipt{}, err
	}
	return build(verification.Catalog, verification.Index, verification.Report, input, inputDigest, sourceDigests{
		catalog: verification.CatalogSHA256, index: verification.IndexSHA256,
		trust: verification.TrustBundleSHA256, approvals: verification.ApprovalSetSHA256,
	}, now)
}

func decodeInput(path string, destination *Input) (string, error) {
	contents, err := readRegular(path, maximumInputBytes)
	if err != nil {
		return "", fmt.Errorf("read MVP readiness input: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", errors.New("decode MVP readiness input")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return "", errors.New("MVP readiness input contains trailing JSON")
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents)), nil
}

func readRegular(path string, maximum int64) ([]byte, error) {
	return readRegularExpectedWithHook(path, maximum, nil, nil)
}

func readRegularWithHook(path string, maximum int64, afterOpen func()) ([]byte, error) {
	return readRegularExpectedWithHook(path, maximum, nil, afterOpen)
}

func readRegularExpectedWithHook(path string, maximum int64, expected os.FileInfo, afterOpen func()) ([]byte, error) {
	validated, err := os.Lstat(path)
	if err != nil || !validated.Mode().IsRegular() || validated.Size() <= 0 || validated.Size() > maximum || (expected != nil && (!os.SameFile(expected, validated) || expected.Size() != validated.Size() || !expected.ModTime().Equal(validated.ModTime()))) {
		return nil, errors.New("file must be bounded, regular, and non-symlink")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(validated, opened) || !opened.Mode().IsRegular() || opened.Size() != validated.Size() || !opened.ModTime().Equal(validated.ModTime()) {
		return nil, errors.New("file changed before open")
	}
	if afterOpen != nil {
		afterOpen()
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(contents)) != opened.Size() || int64(len(contents)) > maximum {
		return nil, errors.New("read bounded file")
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return nil, errors.New("file changed while reading")
	}
	pathAfter, err := os.Lstat(path)
	if err != nil || !pathAfter.Mode().IsRegular() || !os.SameFile(opened, pathAfter) || pathAfter.Size() != opened.Size() || !pathAfter.ModTime().Equal(opened.ModTime()) {
		return nil, errors.New("file changed while reading")
	}
	return contents, nil
}

func Publish(path string, receipt Receipt) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("MVP readiness receipt path is required")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("MVP readiness receipt destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect MVP readiness receipt destination")
	}
	return evidencepublish.JSON(path, receipt, ".agent-memory-mvp-readiness-*")
}
