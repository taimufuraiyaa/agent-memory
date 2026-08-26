package contracts

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGraphProjectionManifestGoldenRoundTripIsDeterministic(t *testing.T) {
	t.Parallel()

	data := readGraphManifestFixture(t, "graph_projection_v1.json")
	var manifest GraphProjectionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode golden projection: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("validate golden projection: %v", err)
	}
	first, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("canonical projection encoding is not deterministic")
	}
}

func TestGraphArtifactManifestGoldenRoundTripIsDeterministic(t *testing.T) {
	t.Parallel()

	data := readGraphManifestFixture(t, "graph_artifact_v1.json")
	var manifest GraphArtifactManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode golden artifact: %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("validate golden artifact: %v", err)
	}
	first, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("canonical artifact encoding is not deterministic")
	}
}

func TestGraphProjectionManifestRejectsMissingIdentityAndUnknownVersion(t *testing.T) {
	t.Parallel()

	var manifest GraphProjectionManifest
	if err := json.Unmarshal(readGraphManifestFixture(t, "graph_projection_v1.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Scope.WorkspaceID = ""
	if err := manifest.Validate(); err == nil {
		t.Fatal("missing workspace must fail")
	}

	if err := json.Unmarshal(readGraphManifestFixture(t, "graph_projection_v1.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.ContractVersion = "graph-projection/v99"
	if err := manifest.Validate(); err == nil {
		t.Fatal("unknown required projection contract must fail")
	}

	manifest.ContractVersion = GraphProjectionContractV1
	manifest.CorrelationMapHash = ""
	if err := manifest.Validate(); err == nil {
		t.Fatal("missing correlation map hash must fail")
	}
}

func TestGraphArtifactManifestRejectsMissingHashesAndBounds(t *testing.T) {
	t.Parallel()

	var manifest GraphArtifactManifest
	if err := json.Unmarshal(readGraphManifestFixture(t, "graph_artifact_v1.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.InputManifestHash = ""
	if err := manifest.Validate(); err == nil {
		t.Fatal("missing input manifest hash must fail")
	}

	if err := json.Unmarshal(readGraphManifestFixture(t, "graph_artifact_v1.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Outputs[0].Bytes = MaxGraphArtifactFileBytes + 1
	if err := manifest.Validate(); err == nil {
		t.Fatal("oversized artifact must fail")
	}

	manifest.Outputs[0].Bytes = 128
	manifest.ArtifactSchemaVersion = "graph-artifact/v99"
	if err := manifest.Validate(); err == nil {
		t.Fatal("unknown artifact schema must fail")
	}
}

func readGraphManifestFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
