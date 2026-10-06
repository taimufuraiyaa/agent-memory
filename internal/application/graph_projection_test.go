package application

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestGraphProjectionExcludesIneligibleCanonicalRecords(t *testing.T) {
	t.Parallel()
	request := graphProjectionRequestFixture()
	request.Records = []GraphProjectionRecord{
		graphProjectionRecord("eligible", "Eligible memory"),
		withGraphProjectionFlag(graphProjectionRecord("secret", "API secret"), func(record *GraphProjectionRecord) { record.Secret = true }),
		withGraphProjectionFlag(graphProjectionRecord("reasoning", "Private reasoning"), func(record *GraphProjectionRecord) { record.RawReasoning = true }),
		withGraphProjectionFlag(graphProjectionRecord("quarantine", "Quarantined"), func(record *GraphProjectionRecord) { record.Quarantined = true }),
		withGraphProjectionFlag(graphProjectionRecord("deleted", "Deleted"), func(record *GraphProjectionRecord) { record.Deleted = true }),
		withGraphProjectionFlag(graphProjectionRecord("expired", "Expired"), func(record *GraphProjectionRecord) { record.Expired = true }),
		withGraphProjectionFlag(graphProjectionRecord("unauthorized", "Unauthorized"), func(record *GraphProjectionRecord) { record.Authorized = false }),
		withGraphProjectionFlag(graphProjectionRecord("suppressed", "Suppressed"), func(record *GraphProjectionRecord) { record.SafetySuppressed = true }),
		withGraphProjectionFlag(graphProjectionRecord("nonexportable", "Non-exportable"), func(record *GraphProjectionRecord) { record.Exportable = false }),
	}

	projection, err := NewGraphProjectionBuilder().Build(request)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Manifest.TextUnitCount != 1 || len(projection.Correlations) != 1 {
		t.Fatalf("projection retained ineligible records: manifest=%#v correlations=%#v", projection.Manifest, projection.Correlations)
	}
	if bytes.Contains(projection.TextUnitsJSONL, []byte("secret")) || bytes.Contains(projection.TextUnitsJSONL, []byte("Private reasoning")) {
		t.Fatal("excluded content leaked into projection bytes")
	}
}

func TestGraphProjectionPreservesExplicitBookMembershipWithoutEncodingIdentityInToken(t *testing.T) {
	t.Parallel()
	request := graphProjectionRequestFixture()
	record := graphProjectionRecord("memory-10", "Day ten learning")
	record.SourceID = "book-a"
	record.EditionID = "book-a-edition-1"
	record.AssetID = "book-a.md"
	record.PassageID = "book-a-passage-7"
	request.Records = []GraphProjectionRecord{record}

	projection, err := NewGraphProjectionBuilder().Build(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.Correlations) != 1 {
		t.Fatalf("correlations = %d", len(projection.Correlations))
	}
	for token, reference := range projection.Correlations {
		if strings.Contains(token, record.ID) || strings.Contains(token, record.SourceID) {
			t.Fatalf("correlation token exposes canonical identity: %q", token)
		}
		if reference.SourceID != record.SourceID || reference.EditionID != record.EditionID ||
			reference.AssetID != record.AssetID || reference.PassageID != record.PassageID {
			t.Fatalf("explicit source membership lost: %#v", reference)
		}
	}
	if !bytes.Contains(projection.TextUnitsJSONL, []byte(`"source_id":"book-a"`)) {
		t.Fatal("projected metadata does not encode explicit source membership")
	}
}

func TestGraphProjectionIsByteDeterministicAcrossInputOrder(t *testing.T) {
	t.Parallel()
	first := graphProjectionRequestFixture()
	first.Records = []GraphProjectionRecord{
		graphProjectionRecord("memory-b", "Second"),
		graphProjectionRecord("memory-a", "First"),
	}
	second := first
	second.Records = []GraphProjectionRecord{first.Records[1], first.Records[0]}

	a, err := NewGraphProjectionBuilder().Build(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewGraphProjectionBuilder().Build(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.DocumentsJSONL, b.DocumentsJSONL) || !bytes.Equal(a.TextUnitsJSONL, b.TextUnitsJSONL) {
		t.Fatal("same canonical set produced different projection bytes")
	}
	aManifest, _ := a.Manifest.CanonicalJSON()
	bManifest, _ := b.Manifest.CanonicalJSON()
	if !bytes.Equal(aManifest, bManifest) {
		t.Fatal("same canonical set produced different manifest")
	}
}

func graphProjectionRequestFixture() GraphProjectionRequest {
	created := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	return GraphProjectionRequest{
		Scope:           core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"},
		ConfigurationID: "configuration-1", JobID: "job-1", RevisionID: "revision-1",
		Mode: contracts.GraphIndexModeFull, ProjectionPolicyVersion: "projection-v1",
		Cutoff:            core.GraphWatermark{Sequence: 42, EventTime: created, Digest: "sha256:cutoff"},
		PromptFingerprint: "sha256:prompts", ModelRoutes: []string{"index-text-primary"},
		CreatedAt: created, ExpiresAt: created.Add(24 * time.Hour), ProducerIdentity: "agent-memory/projection-v1",
	}
}

func graphProjectionRecord(id, content string) GraphProjectionRecord {
	return GraphProjectionRecord{
		ID: id, Kind: GraphProjectionMemory, Content: content, Fingerprint: core.FingerprintText(content),
		EventTime: time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC), Authorized: true, Exportable: true,
	}
}

func withGraphProjectionFlag(record GraphProjectionRecord, mutate func(*GraphProjectionRecord)) GraphProjectionRecord {
	mutate(&record)
	return record
}
