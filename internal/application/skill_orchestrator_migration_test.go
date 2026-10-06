package application

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

func TestSkillMigrationGateDiscoversExistingStateDeterministicallyWithoutMutation(t *testing.T) {
	scope := core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"}
	inventory := core.SkillMigrationInventory{Scope: scope, SchemaVersion: "30", ConfigurationMode: core.SkillOrchestratorShadow, Items: []core.SkillMigrationInventoryItem{
		{Kind: core.SkillMigrationCanary, ID: "revision-canary", SkillID: "skill-a", State: "canary", EvidenceDigest: "sha256:canary"},
		{Kind: core.SkillMigrationCandidate, ID: "candidate-a", State: "accepted", EvidenceDigest: "sha256:candidate"},
		{Kind: core.SkillMigrationActivationOperation, ID: "operation-a", SkillID: "skill-a", State: "materializing", EvidenceDigest: "operation-key", ExistingOpenWorkflow: true},
		{Kind: core.SkillMigrationTestingRevision, ID: "revision-testing", SkillID: "skill-a", State: "testing", EvidenceDigest: "sha256:testing"},
	}}
	repository := &skillMigrationInventoryFixture{inventory: inventory}
	gate, err := NewSkillOrchestratorMigrationGate(repository, SkillMigrationGateConfig{ExpectedSchemaVersion: "30", Limit: 100}, func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	first, err := gate.Verify(context.Background(), scope)
	if err != nil || !first.Ready || len(first.Predictions) != 4 || first.Predictions[0].Kind != core.SkillMigrationActivationOperation || first.Predictions[0].WouldEnqueue {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := gate.Verify(context.Background(), scope)
	if err != nil || second.ShadowDigest != first.ShadowDigest || !reflect.DeepEqual(second.Predictions, first.Predictions) || repository.calls != 2 {
		t.Fatalf("second=%+v calls=%d err=%v", second, repository.calls, err)
	}
	if !reflect.DeepEqual(repository.inventory, inventory) {
		t.Fatal("shadow verification mutated source inventory")
	}
}

func TestSkillMigrationGateFreshRestoreMixedAndParityMatrix(t *testing.T) {
	scope := core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"}
	base := core.SkillMigrationInventory{Scope: scope, SchemaVersion: "30", ConfigurationMode: core.SkillOrchestratorDisabled, Items: []core.SkillMigrationInventoryItem{}}
	for _, test := range []struct {
		name      string
		mutate    func(*core.SkillMigrationInventory)
		expected  string
		wantReady bool
	}{
		{name: "fresh", mutate: func(*core.SkillMigrationInventory) {}, wantReady: true},
		{name: "restore", mutate: func(i *core.SkillMigrationInventory) { i.RestorePaused = true }, expected: "restore_reconciliation_paused"},
		{name: "mixed", mutate: func(i *core.SkillMigrationInventory) { i.UnsupportedContracts = []string{"skill-orchestrator/v0"} }, expected: "unsupported_contract_version"},
		{name: "truncated", mutate: func(i *core.SkillMigrationInventory) { i.Truncated = true }, expected: "inventory_truncated"},
		{name: "unsafe_mode", mutate: func(i *core.SkillMigrationInventory) { i.ConfigurationMode = core.SkillOrchestratorAutomaticLowRisk }, expected: "unsafe_migration_mode"},
		{name: "schema", mutate: func(i *core.SkillMigrationInventory) { i.SchemaVersion = "29" }, expected: "migration_version_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inventory := base
			test.mutate(&inventory)
			gate, _ := NewSkillOrchestratorMigrationGate(&skillMigrationInventoryFixture{inventory: inventory}, SkillMigrationGateConfig{ExpectedSchemaVersion: "30", Limit: 100}, time.Now)
			report, err := gate.Verify(context.Background(), scope)
			if err != nil || report.Ready != test.wantReady {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			if test.expected != "" && (len(report.Blockers) != 1 || report.Blockers[0] != test.expected) {
				t.Fatalf("blockers=%v", report.Blockers)
			}
		})
	}
}

func TestSkillMigrationGateRejectsUnapprovedShadowDigest(t *testing.T) {
	scope := core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "production"}
	inventory := core.SkillMigrationInventory{Scope: scope, SchemaVersion: "30", ConfigurationMode: core.SkillOrchestratorShadow, Items: []core.SkillMigrationInventoryItem{{Kind: core.SkillMigrationCandidate, ID: "candidate", State: "accepted", EvidenceDigest: "sha256:candidate"}}}
	gate, _ := NewSkillOrchestratorMigrationGate(&skillMigrationInventoryFixture{inventory: inventory}, SkillMigrationGateConfig{ExpectedSchemaVersion: "30", ExpectedShadowDigest: "sha256:wrong", Limit: 100}, time.Now)
	report, err := gate.Verify(context.Background(), scope)
	if err != nil || report.Ready || len(report.Blockers) != 1 || report.Blockers[0] != "shadow_parity_mismatch" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

type skillMigrationInventoryFixture struct {
	inventory core.SkillMigrationInventory
	calls     int
}

func (f *skillMigrationInventoryFixture) InspectSkillOrchestratorMigration(context.Context, core.SkillOrchestratorScope, int) (core.SkillMigrationInventory, error) {
	f.calls++
	return f.inventory, nil
}
