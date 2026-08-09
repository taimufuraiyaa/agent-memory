// Package localevidence builds tamper-evident, content-free local alpha manifests.
package localevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	SchemaV1                       = "agent-memory-local-alpha-evidence-v1"
	ClassificationLocalDevelopment = "local_development"
	maximumReceipts                = 64
	maximumReceiptBytes            = 8 << 20
)

var (
	identityPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,63}$`)
	runIDPattern    = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[a-f0-9]{7,64}$`)
	commitPattern   = regexp.MustCompile(`^[a-f0-9]{7,64}$`)
)

type Check struct {
	Name    string `json:"name"`
	Outcome string `json:"outcome"`
	Receipt string `json:"receipt"`
}

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type Metadata struct {
	RunID       string
	Profile     string
	GitCommit   string
	GitDirty    bool
	StartedAt   time.Time
	CompletedAt time.Time
	Checks      []Check
}

type Manifest struct {
	Schema         string    `json:"schema"`
	Classification string    `json:"classification"`
	RunID          string    `json:"run_id"`
	Profile        string    `json:"profile"`
	GitCommit      string    `json:"git_commit"`
	GitDirty       bool      `json:"git_dirty"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
	Passed         bool      `json:"passed"`
	Checks         []Check   `json:"checks"`
	Files          []File    `json:"files"`
}

func Build(root string, metadata Metadata) (Manifest, error) {
	manifest := Manifest{
		Schema: SchemaV1, Classification: ClassificationLocalDevelopment,
		RunID: metadata.RunID, Profile: metadata.Profile, GitCommit: metadata.GitCommit,
		GitDirty: metadata.GitDirty, StartedAt: metadata.StartedAt.UTC(),
		CompletedAt: metadata.CompletedAt.UTC(), Passed: true,
	}
	checks, err := validateChecks(metadata)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Checks = checks
	manifest.Files = make([]File, 0, len(checks))
	seenReceipts := make(map[string]struct{}, len(checks))
	for _, check := range checks {
		if _, duplicate := seenReceipts[check.Receipt]; duplicate {
			return Manifest{}, errors.New("local evidence receipt is duplicated")
		}
		seenReceipts[check.Receipt] = struct{}{}
		file, err := hashReceipt(root, check.Receipt)
		if err != nil {
			return Manifest{}, fmt.Errorf("hash receipt %q: %w", check.Receipt, err)
		}
		manifest.Files = append(manifest.Files, file)
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	return manifest, nil
}

func Validate(root string, manifest Manifest) error {
	if manifest.Schema != SchemaV1 || manifest.Classification != ClassificationLocalDevelopment || !manifest.Passed {
		return errors.New("local evidence manifest identity is invalid")
	}
	metadata := Metadata{
		RunID: manifest.RunID, Profile: manifest.Profile, GitCommit: manifest.GitCommit,
		GitDirty: manifest.GitDirty, StartedAt: manifest.StartedAt,
		CompletedAt: manifest.CompletedAt, Checks: manifest.Checks,
	}
	rebuilt, err := Build(root, metadata)
	if err != nil {
		return err
	}
	if len(rebuilt.Files) != len(manifest.Files) {
		return errors.New("local evidence receipt count changed")
	}
	for index := range rebuilt.Files {
		if rebuilt.Files[index] != manifest.Files[index] {
			return fmt.Errorf("local evidence receipt %q changed", rebuilt.Files[index].Path)
		}
	}
	return nil
}

func validateChecks(metadata Metadata) ([]Check, error) {
	if !runIDPattern.MatchString(metadata.RunID) || (metadata.Profile != "floci" && metadata.Profile != "minio") || !commitPattern.MatchString(metadata.GitCommit) {
		return nil, errors.New("local evidence run identity is invalid")
	}
	if metadata.StartedAt.IsZero() || !metadata.CompletedAt.After(metadata.StartedAt) || len(metadata.Checks) == 0 || len(metadata.Checks) > maximumReceipts {
		return nil, errors.New("local evidence run window or check count is invalid")
	}
	checks := append([]Check(nil), metadata.Checks...)
	seen := make(map[string]struct{}, len(checks))
	for index := range checks {
		checks[index].Name = strings.TrimSpace(checks[index].Name)
		checks[index].Receipt = filepath.ToSlash(strings.TrimSpace(checks[index].Receipt))
		if !identityPattern.MatchString(checks[index].Name) || checks[index].Outcome != "passed" || !safeReceiptPath(checks[index].Receipt) {
			return nil, errors.New("local evidence check is invalid or did not pass")
		}
		if _, duplicate := seen[checks[index].Name]; duplicate {
			return nil, errors.New("local evidence check name is duplicated")
		}
		seen[checks[index].Name] = struct{}{}
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].Name < checks[j].Name })
	return checks, nil
}

func safeReceiptPath(value string) bool {
	if !strings.HasPrefix(value, "receipts/") || filepath.IsAbs(value) || strings.ContainsRune(value, '\x00') {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	return clean == value && !strings.Contains(clean, "../") && clean != "receipts"
}

func hashReceipt(root, relative string) (File, error) {
	path := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return File{}, errors.New("receipt must be a regular non-symlink file")
	}
	if info.Size() < 1 || info.Size() > maximumReceiptBytes {
		return File{}, errors.New("receipt size is invalid")
	}
	handle, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer handle.Close()
	opened, err := handle.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return File{}, errors.New("receipt changed before it was opened")
	}
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(handle, maximumReceiptBytes+1))
	if err != nil || written != info.Size() || written > maximumReceiptBytes {
		return File{}, errors.New("receipt changed while it was hashed")
	}
	return File{Path: relative, SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: written}, nil
}
