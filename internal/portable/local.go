// Package portable builds user-selected local migration bundles.
package portable

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type Selection struct {
	Workspace   string
	SourceFiles map[string]string
	ExportedAt  time.Time
}

func BuildLocal(ctx context.Context, store *sqlite.Store, selection Selection) (exportservice.Bundle, error) {
	selection.Workspace = strings.TrimSpace(selection.Workspace)
	if store == nil || selection.Workspace == "" {
		return exportservice.Bundle{}, errors.New("local portable export requires a store and workspace")
	}
	if selection.ExportedAt.IsZero() {
		selection.ExportedAt = time.Now().UTC()
	}
	bundle := exportservice.Bundle{Format: "agent-memory-portable", Version: "2.0", MinReaderVersion: "2.0", ExportedAt: selection.ExportedAt.UTC(), TenantID: "local:" + selection.Workspace, WorkspaceID: selection.Workspace, Memories: []map[string]any{}, Notes: []map[string]any{}, Sources: []map[string]any{}, SourceVersions: []map[string]any{}, Lineage: []map[string]any{}, Attestations: []map[string]any{}, Policies: []map[string]any{}, SourceObjects: []exportservice.SourceObject{}}
	memories, err := store.ListMemoriesByWorkspace(ctx, selection.Workspace)
	if err != nil {
		return exportservice.Bundle{}, err
	}
	for _, memory := range memories {
		value, err := objectMap(memory)
		if err != nil {
			return exportservice.Bundle{}, err
		}
		bundle.Memories = append(bundle.Memories, value)
		if lineage, lineageErr := store.GetBookMemoryLineage(ctx, memory.ID); lineageErr == nil {
			lineageValue, err := objectMap(lineage)
			if err != nil {
				return exportservice.Bundle{}, err
			}
			bundle.Lineage = append(bundle.Lineage, lineageValue)
		} else if !errors.Is(lineageErr, sql.ErrNoRows) {
			return exportservice.Bundle{}, lineageErr
		}
	}
	notes, err := store.ListNotes(ctx, selection.Workspace, false)
	if err != nil {
		return exportservice.Bundle{}, err
	}
	for _, note := range notes {
		value, err := objectMap(note)
		if err != nil {
			return exportservice.Bundle{}, err
		}
		bundle.Notes = append(bundle.Notes, value)
	}
	sourceIDs := make([]string, 0, len(selection.SourceFiles))
	for sourceID := range selection.SourceFiles {
		sourceIDs = append(sourceIDs, sourceID)
	}
	sort.Strings(sourceIDs)
	for _, sourceID := range sourceIDs {
		sourcePath := selection.SourceFiles[sourceID]
		asset, err := store.GetSourceAsset(ctx, strings.TrimSpace(sourceID))
		if err != nil {
			return exportservice.Bundle{}, fmt.Errorf("selected source %q is not in the local catalog: %w", sourceID, err)
		}
		body, err := os.ReadFile(strings.TrimSpace(sourcePath))
		if err != nil {
			return exportservice.Bundle{}, fmt.Errorf("read selected source %q: %w", sourceID, err)
		}
		sum := sha256.Sum256(body)
		checksum := hex.EncodeToString(sum[:])
		if strings.TrimPrefix(strings.ToLower(asset.ByteFingerprint), "sha256:") != checksum {
			return exportservice.Bundle{}, fmt.Errorf("selected source %q no longer matches its catalog fingerprint", sourceID)
		}
		rights := "lawfully_acquired_private_use"
		if attestation, attestationErr := store.GetSourceAttestation(ctx, asset.ID); attestationErr == nil {
			rights = attestation.RightsBasis
			attestationValue, err := objectMap(attestation)
			if err != nil {
				return exportservice.Bundle{}, err
			}
			bundle.Attestations = append(bundle.Attestations, attestationValue)
		}
		mediaType, err := sourceMediaType(string(asset.Format))
		if err != nil {
			return exportservice.Bundle{}, err
		}
		bundle.Sources = append(bundle.Sources, map[string]any{"id": asset.ID, "edition_id": asset.EditionID, "format": asset.Format, "rights_basis": rights, "byte_fingerprint": asset.ByteFingerprint, "normalized_fingerprint": asset.NormalizedFingerprint})
		bundle.SourceVersions = append(bundle.SourceVersions, map[string]any{"source_id": asset.ID, "version": 1, "content_sha256": checksum, "parser_version": asset.ParserVersion, "normalized_fingerprint": asset.NormalizedFingerprint})
		policyValue, err := objectMap(asset.Policy)
		if err != nil {
			return exportservice.Bundle{}, err
		}
		policyValue["source_id"] = asset.ID
		bundle.Policies = append(bundle.Policies, policyValue)
		bundle.SourceObjects = append(bundle.SourceObjects, exportservice.SourceObject{SourceID: asset.ID, Filename: filepath.Base(sourcePath), MediaType: mediaType, SizeBytes: int64(len(body)), ChecksumSHA256: checksum, BytesBase64: base64.StdEncoding.EncodeToString(body)})
	}
	bundle.SourceBytesIncluded = len(bundle.SourceObjects) > 0
	bundle.SkillLifecycle, err = store.ExportSkillLifecycle(ctx, selection.Workspace)
	if err != nil {
		return exportservice.Bundle{}, err
	}
	if err := bundle.SealManifest(); err != nil {
		return exportservice.Bundle{}, err
	}
	return bundle, nil
}

func objectMap(value any) (map[string]any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func sourceMediaType(format string) (string, error) {
	switch format {
	case "pdf":
		return "application/pdf", nil
	case "epub":
		return "application/epub+zip", nil
	case "markdown":
		return "text/markdown", nil
	case "text":
		return "text/plain", nil
	default:
		return "", fmt.Errorf("source format %q is not portable", format)
	}
}
