package portable

import (
	"testing"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphExportIsOptInMetadataOnly(t *testing.T) {
	t.Parallel()
	metadata := GraphMetadata{
		Entities: []core.GraphEntity{{ID: "entity-a"}}, Edges: []core.GraphEdge{{ID: "edge-a"}},
		Reports:                 []core.GraphReport{{ID: "report-a", Summary: "derived context"}},
		NativeArtifactLocations: []string{"/private/native/output.parquet"},
	}
	if exported := BuildGraphExport(GraphExportSelection{}, metadata); exported != nil {
		t.Fatal("graph metadata exported without explicit request")
	}
	exported := BuildGraphExport(GraphExportSelection{IncludeGraphMetadata: true}, metadata)
	if exported == nil || len(exported.Entities) != 1 || len(exported.Reports) != 1 {
		t.Fatalf("normalized graph metadata missing: %#v", exported)
	}
	if len(exported.NativeArtifacts) != 0 {
		t.Fatalf("native artifacts leaked into canonical export: %#v", exported.NativeArtifacts)
	}
}
