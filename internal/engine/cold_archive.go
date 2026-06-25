// Package engine provides the cold-tier archive storage implementation.
// Archives compress the original memory content with gzip and persist it to
// ~/.agent-memory/archives/<workspace>/<memory-id>.gz so facts can be
// recovered on demand even after the live memory is evicted.
package engine

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ArchiveRecord is the JSON envelope stored inside each .gz file.
// It captures enough context to reconstruct or audit the original memory.
type ArchiveRecord struct {
	MemoryID    string    `json:"memory_id"`
	Workspace   string    `json:"workspace"`
	Type        string    `json:"type"`
	Content     string    `json:"content"`
	Entities    []string  `json:"entities,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	Confidence  float64   `json:"confidence,omitempty"`
	StorageTier string    `json:"storage_tier"`
	ArchivedAt  time.Time `json:"archived_at"`
}

// ColdArchive manages gzip-compressed archives of evicted memory content.
type ColdArchive struct {
	// baseDir is the root archives directory, e.g. ~/.agent-memory/archives
	baseDir string
}

// NewColdArchive creates a ColdArchive rooted at <dataDir>/archives.
func NewColdArchive(dataDir string) *ColdArchive {
	return &ColdArchive{
		baseDir: filepath.Join(dataDir, "archives"),
	}
}

// workspaceDir returns the per-workspace archive directory, creating it if needed.
func (a *ColdArchive) workspaceDir(workspace string) (string, error) {
	// Sanitize workspace name to prevent path traversal.
	clean := strings.ReplaceAll(filepath.Clean(workspace), "/", "_")
	dir := filepath.Join(a.baseDir, clean)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create archive dir: %w", err)
	}
	return dir, nil
}

// archivePath returns the .gz file path for a given memory ID.
func (a *ColdArchive) archivePath(workspace, memoryID string) (string, error) {
	dir, err := a.workspaceDir(workspace)
	if err != nil {
		return "", err
	}
	// Sanitize memoryID (UUIDs are safe but be defensive).
	safe := strings.ReplaceAll(filepath.Base(memoryID), "/", "_")
	return filepath.Join(dir, safe+".gz"), nil
}

// Store compresses rec and writes it to <baseDir>/<workspace>/<id>.gz.
// It is safe to call multiple times; existing archives are overwritten.
func (a *ColdArchive) Store(rec ArchiveRecord) error {
	path, err := a.archivePath(rec.Workspace, rec.MemoryID)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open archive file: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz := gzip.NewWriter(f)
	gz.Name = rec.MemoryID + ".json"
	gz.Comment = "agent-memory cold archive"

	enc := json.NewEncoder(gz)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rec); err != nil {
		return fmt.Errorf("encode archive: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("finalize gzip: %w", err)
	}
	return nil
}

// Load decompresses and returns the ArchiveRecord for memoryID.
// Returns os.ErrNotExist (via errors.Is) if no archive exists.
func (a *ColdArchive) Load(workspace, memoryID string) (*ArchiveRecord, error) {
	path, err := a.archivePath(workspace, memoryID)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err // callers use errors.Is(err, os.ErrNotExist)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("open gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()

	body, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("decompress archive: %w", err)
	}

	var rec ArchiveRecord
	if err := json.Unmarshal(body, &rec); err != nil {
		return nil, fmt.Errorf("decode archive: %w", err)
	}
	return &rec, nil
}

// Delete removes the archive file for memoryID. No-op if it does not exist.
func (a *ColdArchive) Delete(workspace, memoryID string) error {
	path, err := a.archivePath(workspace, memoryID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete archive: %w", err)
	}
	return nil
}

// ListIDs returns the memory IDs for which archives exist in the workspace.
func (a *ColdArchive) ListIDs(workspace string) ([]string, error) {
	dir, err := a.workspaceDir(workspace)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list archives: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gz") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(e.Name(), ".gz"))
	}
	return ids, nil
}
