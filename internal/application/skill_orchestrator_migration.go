package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

type SkillMigrationInventoryRepository interface {
	InspectSkillOrchestratorMigration(context.Context, core.SkillOrchestratorScope, int) (core.SkillMigrationInventory, error)
}

type SkillMigrationGateConfig struct {
	ExpectedSchemaVersion string
	ExpectedShadowDigest  string
	Limit                 int
}

type SkillMigrationShadowPrediction struct {
	Kind         core.SkillMigrationInventoryKind `json:"kind"`
	SourceID     string                           `json:"source_id"`
	Stage        core.SkillOrchestratorStage      `json:"stage"`
	WouldEnqueue bool                             `json:"would_enqueue"`
	ReasonCode   string                           `json:"reason_code"`
	DecisionKey  string                           `json:"decision_key"`
}

type SkillMigrationGateReport struct {
	Scope        core.SkillOrchestratorScope      `json:"scope"`
	Ready        bool                             `json:"ready"`
	Inventory    core.SkillMigrationInventory     `json:"inventory"`
	Predictions  []SkillMigrationShadowPrediction `json:"predictions"`
	ShadowDigest string                           `json:"shadow_digest"`
	Blockers     []string                         `json:"blockers"`
	VerifiedAt   time.Time                        `json:"verified_at"`
}

type SkillOrchestratorMigrationGate struct {
	repository SkillMigrationInventoryRepository
	config     SkillMigrationGateConfig
	now        func() time.Time
}

func NewSkillOrchestratorMigrationGate(repository SkillMigrationInventoryRepository, config SkillMigrationGateConfig, now func() time.Time) (*SkillOrchestratorMigrationGate, error) {
	if repository == nil || strings.TrimSpace(config.ExpectedSchemaVersion) == "" || config.Limit < 1 || config.Limit > 10_000 {
		return nil, errors.New("skill migration gate dependencies and bounds are required")
	}
	if now == nil {
		now = time.Now
	}
	return &SkillOrchestratorMigrationGate{repository: repository, config: config, now: now}, nil
}

func (g *SkillOrchestratorMigrationGate) Verify(ctx context.Context, scope core.SkillOrchestratorScope) (SkillMigrationGateReport, error) {
	if g == nil || scope.Validate() != nil {
		return SkillMigrationGateReport{}, errors.New("valid skill migration gate scope is required")
	}
	inventory, err := g.repository.InspectSkillOrchestratorMigration(ctx, scope, g.config.Limit)
	if err != nil {
		return SkillMigrationGateReport{}, err
	}
	report := SkillMigrationGateReport{Scope: scope, Inventory: inventory, Blockers: []string{}, VerifiedAt: g.now().UTC()}
	if inventory.Scope != scope {
		report.Blockers = append(report.Blockers, "inventory_scope_mismatch")
	}
	if inventory.SchemaVersion != g.config.ExpectedSchemaVersion {
		report.Blockers = append(report.Blockers, "migration_version_mismatch")
	}
	if inventory.RestorePaused {
		report.Blockers = append(report.Blockers, "restore_reconciliation_paused")
	}
	if inventory.ConfigurationMode != core.SkillOrchestratorDisabled && inventory.ConfigurationMode != core.SkillOrchestratorShadow {
		report.Blockers = append(report.Blockers, "unsafe_migration_mode")
	}
	if inventory.Truncated {
		report.Blockers = append(report.Blockers, "inventory_truncated")
	}
	if len(inventory.UnsupportedContracts) > 0 {
		report.Blockers = append(report.Blockers, "unsupported_contract_version")
	}
	seenItems := make(map[string]struct{}, len(inventory.Items))
	for _, item := range inventory.Items {
		key := string(item.Kind) + "\x00" + item.ID
		_, duplicate := seenItems[key]
		seenItems[key] = struct{}{}
		if !validSkillMigrationInventoryKind(item.Kind) || strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.State) == "" || strings.TrimSpace(item.EvidenceDigest) == "" || duplicate {
			report.Blockers = append(report.Blockers, "invalid_inventory_item")
			break
		}
	}
	report.Predictions = predictSkillMigrationShadow(inventory.Items)
	report.ShadowDigest, err = digestSkillMigrationReport(scope, inventory.SchemaVersion, report.Predictions)
	if err != nil {
		return SkillMigrationGateReport{}, err
	}
	if g.config.ExpectedShadowDigest != "" && g.config.ExpectedShadowDigest != report.ShadowDigest {
		report.Blockers = append(report.Blockers, "shadow_parity_mismatch")
	}
	report.Ready = len(report.Blockers) == 0
	return report, nil
}

func validSkillMigrationInventoryKind(kind core.SkillMigrationInventoryKind) bool {
	return kind == core.SkillMigrationCandidate || kind == core.SkillMigrationTestingRevision || kind == core.SkillMigrationCanary || kind == core.SkillMigrationActivationOperation
}

func predictSkillMigrationShadow(items []core.SkillMigrationInventoryItem) []SkillMigrationShadowPrediction {
	ordered := append([]core.SkillMigrationInventoryItem(nil), items...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Kind != ordered[j].Kind {
			return ordered[i].Kind < ordered[j].Kind
		}
		return ordered[i].ID < ordered[j].ID
	})
	result := make([]SkillMigrationShadowPrediction, 0, len(ordered))
	for _, item := range ordered {
		stage := core.SkillStageBuild
		switch item.Kind {
		case core.SkillMigrationTestingRevision:
			stage = core.SkillStageEvaluate
		case core.SkillMigrationCanary:
			stage = core.SkillStageAnalyzeCanary
		case core.SkillMigrationActivationOperation:
			stage = core.SkillStageReconcileMaterialization
		}
		prediction := SkillMigrationShadowPrediction{Kind: item.Kind, SourceID: item.ID, Stage: stage, WouldEnqueue: !item.ExistingOpenWorkflow, ReasonCode: "missing_workflow"}
		if item.ExistingOpenWorkflow {
			prediction.ReasonCode = "workflow_already_present"
		}
		hash := sha256.Sum256([]byte(string(item.Kind) + "\x00" + item.ID + "\x00" + item.State + "\x00" + item.EvidenceDigest + "\x00" + string(stage) + "\x00" + prediction.ReasonCode))
		prediction.DecisionKey = "sha256:" + hex.EncodeToString(hash[:])
		result = append(result, prediction)
	}
	return result
}

func digestSkillMigrationReport(scope core.SkillOrchestratorScope, schema string, predictions []SkillMigrationShadowPrediction) (string, error) {
	payload, err := json.Marshal(struct {
		Scope       core.SkillOrchestratorScope      `json:"scope"`
		Schema      string                           `json:"schema"`
		Predictions []SkillMigrationShadowPrediction `json:"predictions"`
	}{scope, schema, predictions})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
