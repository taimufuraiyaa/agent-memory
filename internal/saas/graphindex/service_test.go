package graphindex

import (
	"context"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/contracts"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/graphworker"
)

type hostedGraphRepositoryFixture struct {
	imported, activated, audited int
	activation                   core.GraphActivation
}

func (r *hostedGraphRepositoryFixture) ImportGraphRevisionBatch(context.Context, contracts.GraphRevisionImportBatch) error {
	r.imported++
	return nil
}
func (r *hostedGraphRepositoryFixture) PrepareGraphImport(context.Context, graphworker.CompletionEvent, contracts.GraphArtifactManifest, time.Time) (bool, error) {
	return true, nil
}
func (r *hostedGraphRepositoryFixture) RecordGraphFailure(context.Context, graphworker.CompletionEvent, time.Time) error {
	return nil
}
func (r *hostedGraphRepositoryFixture) ActivateGraphRevision(_ context.Context, activation core.GraphActivation) error {
	r.activated++
	r.activation = activation
	return nil
}
func (r *hostedGraphRepositoryFixture) AppendGraphOperatorAudit(context.Context, core.GraphScope, string, string, string, map[string]string) error {
	r.audited++
	return nil
}

type hostedArtifactLoaderFixture struct {
	manifest contracts.GraphArtifactManifest
	batch    contracts.GraphRevisionImportBatch
}

func (l hostedArtifactLoaderFixture) LoadNormalized(context.Context, string) (contracts.GraphArtifactManifest, contracts.GraphRevisionImportBatch, error) {
	return l.manifest, l.batch, nil
}

type hostedCompletionLedgerFixture struct {
	claimed  bool
	finished int
}

func (l *hostedCompletionLedgerFixture) ClaimGraphCompletion(context.Context, graphworker.CompletionEvent, string, time.Duration, time.Time) (bool, error) {
	return l.claimed, nil
}
func (l *hostedCompletionLedgerFixture) FinishGraphCompletion(context.Context, graphworker.CompletionEvent, string, time.Time) error {
	l.finished++
	return nil
}

func TestGraphImportActivationValidatesCompletionAndActivates(t *testing.T) {
	event, manifest, batch := hostedCompletionFixtures()
	repository := &hostedGraphRepositoryFixture{}
	ledger := &hostedCompletionLedgerFixture{claimed: true}
	service, _ := NewService(repository, hostedArtifactLoaderFixture{manifest, batch}, ledger, "importer-a", time.Minute, time.Now)
	if err := service.HandleCompletion(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if repository.imported != 1 || repository.activated != 1 || repository.audited != 1 || ledger.finished != 1 || repository.activation.ExpectedRevision != event.ExpectedRevision {
		t.Fatalf("incomplete hosted activation: repo=%#v ledger=%#v", repository, ledger)
	}
}

func TestGraphImportActivationRejectsForgedEventBeforeLoad(t *testing.T) {
	event, manifest, batch := hostedCompletionFixtures()
	event.ArtifactPrefix = "graph-artifacts/staging/tenant-b/workspace-b/job/revision/"
	repository := &hostedGraphRepositoryFixture{}
	service, _ := NewService(repository, hostedArtifactLoaderFixture{manifest, batch}, &hostedCompletionLedgerFixture{claimed: true}, "importer-a", time.Minute, time.Now)
	if err := service.HandleCompletion(context.Background(), event); err == nil {
		t.Fatal("forged completion accepted")
	}
	if repository.imported != 0 {
		t.Fatal("forged completion reached importer")
	}
}

func TestGraphImportActivationCoalescesFinishedEvent(t *testing.T) {
	event, manifest, batch := hostedCompletionFixtures()
	repository := &hostedGraphRepositoryFixture{}
	service, _ := NewService(repository, hostedArtifactLoaderFixture{manifest, batch}, &hostedCompletionLedgerFixture{claimed: false}, "importer-a", time.Minute, time.Now)
	if err := service.HandleCompletion(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if repository.imported != 0 {
		t.Fatal("duplicate completion reimported")
	}
}

func hostedCompletionFixtures() (graphworker.CompletionEvent, contracts.GraphArtifactManifest, contracts.GraphRevisionImportBatch) {
	scope := core.GraphScope{TenantID: "tenant-a", WorkspaceID: "workspace-a"}
	event := graphworker.CompletionEvent{ID: "event-a", Scope: scope, JobID: "job-a", ConfigurationID: "configuration-a", RevisionID: "revision-a", ExpectedRevision: "revision-old", ArtifactPrefix: "graph-artifacts/staging/tenant-a/workspace-a/job-a/revision-a/", Status: "completed"}
	manifest := contracts.GraphArtifactManifest{ContractVersion: contracts.GraphAdapterContractV1, ArtifactSchemaVersion: contracts.GraphArtifactSchemaV1, Scope: scope, ConfigurationID: "configuration-a", JobID: "job-a", RevisionID: "revision-a", AdapterName: "adapter", AdapterVersion: "0.1.0", GraphRAGVersion: "3.1.2", PythonVersion: "3.13", EnvironmentFingerprint: "env", InputManifestHash: "input", ConfigurationFingerprint: "config", PromptFingerprint: "prompt", IndexMethod: core.GraphIndexStandard, Mode: contracts.GraphIndexModeFull, Outputs: []contracts.GraphArtifactFile{{Name: "entities.jsonl", Kind: "entities", Required: true, Bytes: 1, Rows: 1, SchemaFingerprint: "schema", ContentHash: "hash"}}, Models: []string{"model"}, Status: contracts.GraphArtifactCompleted, CompletedAt: time.Now(), Attestation: contracts.GraphArtifactAttestation{ProducerIdentity: "worker", BuildDigest: "build", Signature: "signature"}}
	batch := contracts.GraphRevisionImportBatch{Scope: scope, ConfigurationID: "configuration-a", RevisionID: "revision-a"}
	return event, manifest, batch
}
