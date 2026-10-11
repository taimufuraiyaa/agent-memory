package portable_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/portable"
)

func TestPortableBundleEncryptedManifestAndExplicitSourceBytes(t *testing.T) {
	bundle := portable.Bundle{Format: "agent-memory-portable", Version: "2.0", MinReaderVersion: "2.0", ExportedAt: time.Now().UTC(), Memories: []map[string]any{{"id": "memory"}}, Notes: []map[string]any{}, Sources: []map[string]any{{"id": "source"}}, SourceVersions: []map[string]any{}, Lineage: []map[string]any{}, Attestations: []map[string]any{}, Policies: []map[string]any{}, SourceBytesIncluded: false, SourceObjects: []portable.SourceObject{}}
	if err := bundle.SealManifest(); err != nil {
		t.Fatal(err)
	}
	plain, _ := json.Marshal(bundle)
	encrypted, err := portable.EncryptPortable("correct horse battery staple", plain)
	if err != nil {
		t.Fatal(err)
	}
	if string(encrypted) == string(plain) {
		t.Fatal("portable bundle was not encrypted")
	}
	decoded, err := portable.DecryptPortable("correct horse battery staple", encrypted)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip portable.Bundle
	if err := json.Unmarshal(decoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if err := roundTrip.VerifyManifest(); err != nil {
		t.Fatal(err)
	}
	roundTrip.Manifest.Counts["memories"]++
	if err := roundTrip.VerifyManifest(); err == nil {
		t.Fatal("tampered manifest counts verified")
	}
	roundTrip.Manifest.Counts["memories"]--
	roundTrip.Memories[0]["id"] = "tampered"
	if err := roundTrip.VerifyManifest(); err == nil {
		t.Fatal("tampered manifest verified")
	}
}

func TestGraphExportIsAuthorizedNormalizedMetadataWithoutNativeArtifacts(t *testing.T) {
	graph := portable.BuildGraphExport(portable.GraphExportSelection{IncludeGraphMetadata: true}, portable.GraphMetadata{
		Entities:                []core.GraphEntity{{ID: "entity-a"}},
		Edges:                   []core.GraphEdge{{ID: "edge-a"}},
		Reports:                 []core.GraphReport{{ID: "report-a", Summary: "derived summary"}},
		NativeArtifactLocations: []string{"graph-artifacts/staging/tenant-a/secret"},
	})
	encoded, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	bundle := portable.Bundle{}
	if err := bundle.AttachGraphMetadata(false, encoded); err == nil {
		t.Fatal("unauthorized graph metadata export was accepted")
	}
	if err := bundle.AttachGraphMetadata(true, encoded); err != nil {
		t.Fatal(err)
	}
	if err := bundle.SealManifest(); err != nil {
		t.Fatal(err)
	}
	if bundle.Manifest.Counts["graph_metadata"] != 1 || len(bundle.GraphMetadata) == 0 {
		t.Fatalf("graph metadata was not integrity-bound: %+v", bundle.Manifest)
	}
	if err := bundle.VerifyManifest(); err != nil {
		t.Fatal(err)
	}
	var exported map[string]any
	if err := json.Unmarshal(bundle.GraphMetadata, &exported); err != nil {
		t.Fatal(err)
	}
	if native, present := exported["native_artifacts"]; present && native != nil {
		if values, ok := native.([]any); !ok || len(values) != 0 {
			t.Fatalf("native artifact custody leaked into export: %#v", native)
		}
	}
}
