package export_test

import (
	"encoding/json"
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/portable"
	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
)

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
	bundle := exportservice.Bundle{}
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
