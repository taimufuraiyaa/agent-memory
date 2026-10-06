// Package portable owns the local encrypted bundle contract.
package portable

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

type Bundle struct {
	Format              string                      `json:"format"`
	Version             string                      `json:"version"`
	MinReaderVersion    string                      `json:"min_reader_version"`
	ExportedAt          time.Time                   `json:"exported_at"`
	TenantID            string                      `json:"tenant_id"`
	WorkspaceID         string                      `json:"workspace_id,omitempty"`
	Memories            []map[string]any            `json:"memories"`
	Notes               []map[string]any            `json:"notes"`
	Sources             []map[string]any            `json:"sources"`
	SourceVersions      []map[string]any            `json:"source_versions"`
	Lineage             []map[string]any            `json:"lineage"`
	Attestations        []map[string]any            `json:"attestations"`
	Policies            []map[string]any            `json:"policies"`
	SourceBytesIncluded bool                        `json:"source_bytes_included"`
	SourceObjects       []SourceObject              `json:"source_objects,omitempty"`
	GraphMetadata       json.RawMessage             `json:"graph_metadata,omitempty"`
	SkillLifecycle      map[string][]map[string]any `json:"skill_lifecycle"`
	Manifest            BundleManifest              `json:"manifest"`
}

type SourceObject struct {
	SourceID       string `json:"source_id"`
	Filename       string `json:"filename"`
	MediaType      string `json:"media_type"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
	BytesBase64    string `json:"bytes_base64"`
}

type BundleManifest struct {
	Algorithm     string         `json:"algorithm"`
	PayloadSHA256 string         `json:"payload_sha256"`
	Counts        map[string]int `json:"counts"`
}

func (b *Bundle) SealManifest() error {
	if b.Memories == nil {
		b.Memories = []map[string]any{}
	}
	if b.Notes == nil {
		b.Notes = []map[string]any{}
	}
	if b.Sources == nil {
		b.Sources = []map[string]any{}
	}
	if b.SourceVersions == nil {
		b.SourceVersions = []map[string]any{}
	}
	if b.Lineage == nil {
		b.Lineage = []map[string]any{}
	}
	if b.Attestations == nil {
		b.Attestations = []map[string]any{}
	}
	if b.Policies == nil {
		b.Policies = []map[string]any{}
	}
	if b.SourceObjects == nil {
		b.SourceObjects = []SourceObject{}
	}
	if b.SkillLifecycle == nil {
		b.SkillLifecycle = map[string][]map[string]any{}
	}
	b.Manifest = BundleManifest{}
	payload, err := json.Marshal(struct {
		Memories, Notes, Sources, SourceVersions, Lineage, Attestations, Policies []map[string]any
		SourceObjects                                                             []SourceObject
		GraphMetadata                                                             json.RawMessage
		SkillLifecycle                                                            map[string][]map[string]any
	}{b.Memories, b.Notes, b.Sources, b.SourceVersions, b.Lineage, b.Attestations, b.Policies, b.SourceObjects, b.GraphMetadata, b.SkillLifecycle})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	graphCount := 0
	if len(b.GraphMetadata) > 0 {
		graphCount = 1
	}
	skillRecords := 0
	for _, records := range b.SkillLifecycle {
		skillRecords += len(records)
	}
	b.Manifest = BundleManifest{Algorithm: "sha256", PayloadSHA256: hex.EncodeToString(sum[:]), Counts: map[string]int{"memories": len(b.Memories), "notes": len(b.Notes), "sources": len(b.Sources), "source_versions": len(b.SourceVersions), "lineage": len(b.Lineage), "attestations": len(b.Attestations), "policies": len(b.Policies), "source_objects": len(b.SourceObjects), "graph_metadata": graphCount, "skill_lifecycle_records": skillRecords}}
	return nil
}

// AttachGraphMetadata adds explicitly authorized normalized graph metadata to a
// local export. Native GraphRAG artifact locations are rejected because they
// are internal, rebuildable cache custody rather than customer data.
func (b *Bundle) AttachGraphMetadata(authorized bool, encoded []byte) error {
	if !authorized {
		return errors.New("graph metadata export is not authorized")
	}
	var envelope struct {
		SchemaVersion   string            `json:"schema_version"`
		NativeArtifacts []json.RawMessage `json:"native_artifacts"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil {
		// The complete graph export has additional owned fields. Decode those as
		// a map after strict top-level JSON validation and inspect the two policy
		// fields without coupling the portable package to graph types.
		var raw map[string]json.RawMessage
		if json.Unmarshal(encoded, &raw) != nil {
			return errors.New("graph metadata export is invalid")
		}
		if err := json.Unmarshal(raw["schema_version"], &envelope.SchemaVersion); err != nil {
			return errors.New("graph metadata schema is invalid")
		}
		if value, ok := raw["native_artifacts"]; ok && string(value) != "null" {
			if err := json.Unmarshal(value, &envelope.NativeArtifacts); err != nil {
				return errors.New("graph native artifact metadata is invalid")
			}
		}
	}
	if envelope.SchemaVersion != "agent-memory-graph-metadata/v1" || len(envelope.NativeArtifacts) != 0 {
		return errors.New("graph export contains unsupported or native artifact metadata")
	}
	b.GraphMetadata = append(json.RawMessage(nil), encoded...)
	return nil
}

func (b Bundle) VerifyManifest() error {
	copy := b
	expected := b.Manifest
	if err := copy.SealManifest(); err != nil {
		return err
	}
	if expected.Algorithm != "sha256" || expected.PayloadSHA256 != copy.Manifest.PayloadSHA256 || !sameCounts(expected.Counts, copy.Manifest.Counts) {
		return errors.New("portable bundle manifest mismatch")
	}
	return nil
}

func sameCounts(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
