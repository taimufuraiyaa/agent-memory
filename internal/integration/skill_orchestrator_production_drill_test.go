package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
	"github.com/taimufuraiyaa/agent-memory/internal/workspace"
)

func TestSkillOrchestratorRepeatedProductionDrillsPreserveActiveSkillAndAuditHistory(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	root := t.TempDir()
	allowMigrationCleanup(t, root)
	skillRoot := filepath.Join(root, ".agents", "skills", "production-drill")
	if err := os.MkdirAll(skillRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte("---\nname: production-drill\ndescription: Verify production drills\n---\n# Production drill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "production-drill.db")
	store := openProductionDrillStore(t, databasePath)
	defer func() { _ = store.Close() }()
	if _, err := workspace.ImportExistingSkills(ctx, store, "ws", root, func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	skills, err := store.ListLogicalSkills(ctx, "ws", 10)
	if err != nil || len(skills) != 1 {
		t.Fatalf("skills=%+v err=%v", skills, err)
	}
	activation, err := store.GetSkillActivation(ctx, "ws", "local", skills[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	activeDigest := activation.ActiveDigest
	scope := core.SkillOrchestratorScope{WorkspaceID: "ws", Environment: "local"}
	releasePublic, releasePrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	productPublic, productPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	modes := []core.SkillOrchestratorMode{
		core.SkillOrchestratorDisabled, core.SkillOrchestratorShadow, core.SkillOrchestratorManual,
		core.SkillOrchestratorCanary, core.SkillOrchestratorAutomaticLowRisk,
	}
	rollout := make([]application.SkillRolloutObservation, 0, len(modes))
	for index, mode := range modes {
		configuration := productionDrillConfiguration(scope, int64(index+1), mode, now.Add(time.Duration(index)*time.Minute))
		receiptID := "configuration-" + string(rune('1'+index))
		if mode == core.SkillOrchestratorAutomaticLowRisk {
			configuration.SignatureReference = receiptID
		}
		configuration.Digest, err = application.ComputeSkillOrchestratorConfigurationDigest(configuration)
		if err != nil {
			t.Fatal(err)
		}
		created, err := store.StoreSkillOrchestratorConfiguration(ctx, configuration, core.SkillOrchestratorConfigurationAudit{
			ActorID: "release-operator", RequestID: "rollout-" + string(mode), Operation: "skill_orchestrator.configuration.create",
			FromVersion: int64(index), ToVersion: int64(index + 1), ReasonCode: "staged_rollout", OccurredAt: configuration.CreatedAt,
		})
		if err != nil || !created {
			t.Fatalf("store mode %s created=%v err=%v", mode, created, err)
		}
		receipt, err := application.SignSkillOrchestratorConfigurationReceipt(application.SkillOrchestratorConfigurationReceipt{
			Schema: application.SkillOrchestratorConfigurationReceiptSchemaV1, ReceiptID: receiptID,
			ReleaseID: "release-33", BuildDigest: productionDrillDigest("build"), MigrationDigest: productionDrillDigest("migration"),
			Configuration: configuration, SignerID: "release-signer", SignedAt: configuration.CreatedAt.Add(30 * time.Second), SigningKeyID: "release-key",
		}, releasePrivate)
		if err != nil {
			t.Fatal(err)
		}
		rollout = append(rollout, application.SkillRolloutObservation{Sequence: index + 1, ConfigurationReceipt: receipt, Passed: true})
	}

	drills := make([]application.SkillOperationalDrill, 0, 8)
	for iteration := 1; iteration <= 2; iteration++ {
		for _, operation := range application.RequiredSkillReleaseDrillOperations() {
			before := productionDrillSnapshot(t, ctx, store, skills[0].ID)
			started := time.Now()
			switch operation {
			case application.SkillReleaseDrillPause, application.SkillReleaseDrillDrain:
				runtime, finished := startProductionDrillRuntime(t, store, iteration, operation)
				shutdownNaturalRuntime(t, runtime, finished)
			case application.SkillReleaseDrillRestore:
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				store = openProductionDrillStore(t, databasePath)
			case application.SkillReleaseDrillShutdown:
				content, err := os.ReadFile(filepath.Join(skillRoot, "SKILL.md"))
				if err != nil || !strings.Contains(string(content), "Production drill") {
					t.Fatalf("legacy active skill unavailable after shutdown: %v", err)
				}
			}
			if _, err := store.AppendAuditEvent(ctx, sqlite.AuditEventInput{
				Workspace: "ws", Operation: "skill_orchestrator.drill." + string(operation), Outcome: "success",
				Actor: "release-operator", Source: "production_drill", RequestID: "drill-" + string(rune('0'+iteration)) + "-" + string(operation),
				TargetType: "skill_orchestrator_release", TargetIDs: []string{"release-33"}, Reason: "staging_certification", OccurredAt: time.Now().UTC(),
			}); err != nil {
				t.Fatal(err)
			}
			after := productionDrillSnapshot(t, ctx, store, skills[0].ID)
			drills = append(drills, application.SkillOperationalDrill{
				Iteration: iteration, Operation: operation, Passed: before.activeDigest == activeDigest && after.activeDigest == activeDigest,
				ActiveSkillDigestBefore: before.activeDigest, ActiveSkillDigestAfter: after.activeDigest,
				AuditRecordsBefore: int64(before.auditCount), AuditRecordsAfter: int64(after.auditCount),
				RollbackMillis: time.Since(started).Milliseconds(), AlertsRouted: true,
				RunbookID: "skill-orchestrator-production-release", RunbookDigest: productionDrillDigest("runbook"),
			})
		}
	}

	evidence, err := application.SignSkillProductionReleaseEvidence(application.SkillProductionReleaseEvidence{
		Schema: application.SkillProductionReleaseEvidenceSchemaV2, ReleaseID: "release-33",
		BuildDigest: productionDrillDigest("build"), MigrationDigest: productionDrillDigest("migration"), PolicyDigest: productionDrillDigest("policy"),
		Rollout: rollout, Drills: drills, RollbackSLOMillis: 2000,
		StandaloneReportDigest: productionDrillDigest("standalone"), HostedReportDigest: productionDrillDigest("hosted"),
		ChaosCertificateDigest: productionDrillDigest("chaos"), SecurityReportDigest: productionDrillDigest("security"),
		CapacityReportDigest: productionDrillDigest("capacity"), MigrationReportDigest: productionDrillDigest("migration-report"),
		AlertRoutingDigest: productionDrillDigest("alerts"), GeneratedAt: now.Add(10 * time.Minute),
		SignerID: "release-signer", SigningKeyID: "release-key",
	}, releasePrivate)
	if err != nil {
		t.Fatal(err)
	}
	evidenceDigest, err := application.ComputeSkillProductionReleaseEvidenceDigest(evidence)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := application.SignSkillProductApproval(application.SkillProductApproval{
		Schema: application.SkillProductApprovalSchemaV2, ApprovalID: "approval-33", ReleaseID: evidence.ReleaseID,
		BuildDigest: evidence.BuildDigest, MigrationDigest: evidence.MigrationDigest, PolicyDigest: evidence.PolicyDigest,
		ConfigurationDigest:   rollout[len(rollout)-1].ConfigurationReceipt.Configuration.Digest,
		ReleaseEvidenceDigest: evidenceDigest,
		ApproverID:            "accountable-product-owner", ApproverRole: "accountable_product",
		RiskClassesApproved: true, ThresholdsApproved: true, CanaryPolicyApproved: true, RetryDeadLetterApproved: true,
		BudgetsApproved: true, RetentionApproved: true, SLOsApproved: true, AutomaticLowRiskApproved: true,
		ApprovedAt: now.Add(15 * time.Minute), ExpiresAt: now.Add(7 * 24 * time.Hour), SigningKeyID: "product-key",
	}, productPrivate)
	if err != nil {
		t.Fatal(err)
	}
	report, err := application.EvaluateSkillOrchestratorReleaseGate(application.SkillReleaseGateConfig{
		ReleaseID: evidence.ReleaseID, BuildDigest: evidence.BuildDigest, MigrationDigest: evidence.MigrationDigest,
		PolicyDigest: evidence.PolicyDigest, ReleaseSignerID: "release-signer",
		TrustedReleaseKeys: map[string]ed25519.PublicKey{"release-key": releasePublic},
		TrustedProductKeys: map[string]ed25519.PublicKey{"product-key": productPublic}, MaximumApprovalAge: 30 * 24 * time.Hour,
	}, evidence, approval, now.Add(time.Hour))
	if err != nil || !report.Ready || report.DrillIterations != 2 {
		t.Fatalf("release report=%+v err=%v", report, err)
	}
}

type productionDrillSnapshotValue struct {
	activeDigest string
	auditCount   int
}

func productionDrillSnapshot(t *testing.T, ctx context.Context, store *sqlite.Store, skillID string) productionDrillSnapshotValue {
	t.Helper()
	activation, err := store.GetSkillActivation(ctx, "ws", "local", skillID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.ListAuditEvents(ctx, sqlite.AuditFilter{Workspace: "ws", Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return productionDrillSnapshotValue{activeDigest: activation.ActiveDigest, auditCount: len(events)}
}

func productionDrillConfiguration(scope core.SkillOrchestratorScope, version int64, mode core.SkillOrchestratorMode, createdAt time.Time) core.SkillOrchestratorConfiguration {
	configuration := core.SkillOrchestratorConfiguration{
		Scope: scope, Version: version, ContractVersion: core.SkillOrchestratorContractVersion,
		PolicyDigest: productionDrillDigest("policy"), Mode: mode, PollInterval: 100 * time.Millisecond,
		ReconciliationInterval: time.Second, ClaimBatch: 2, WorkerConcurrency: 2, TenantConcurrency: 2,
		WorkspaceConcurrency: 1, DrainTimeout: time.Second, StaleReadinessThreshold: time.Minute, EvaluationBudgetUnits: 100,
		AlertTargets:  core.SkillOrchestratorAlertTargets{ReadyQueueStuckAfter: time.Minute, LeaseChurnWindow: time.Minute, LeaseFailureCount: 5, CanaryStaleAfter: time.Hour, RollbackFailureAfter: time.Minute},
		StagePolicies: []core.SkillOrchestratorStagePolicy{{Stage: core.SkillStageDetect, Enabled: true, LeaseDuration: time.Minute, RenewalInterval: time.Second, Timeout: 30 * time.Second, MaxAttempts: 3, InitialBackoff: time.Second, MaximumBackoff: time.Minute}},
		CreatedBy:     "release-operator", CreatedAt: createdAt,
	}
	if mode == core.SkillOrchestratorAutomaticLowRisk {
		configuration.ApprovalReference = "approval-33"
		configuration.ReleaseEvidenceReference = "release-33"
		configuration.SignatureReference = "signature-33"
	}
	return configuration
}

type productionDrillNoopWorker struct {
	once    sync.Once
	started chan struct{}
}

func (w *productionDrillNoopWorker) RunOnce(context.Context) (application.SkillWorkerRunReport, error) {
	w.once.Do(func() { close(w.started) })
	return application.SkillWorkerRunReport{}, nil
}

type productionDrillNoopReconciler struct{}

func (productionDrillNoopReconciler) RunOnce(context.Context) (application.SkillReconciliationReport, error) {
	return application.SkillReconciliationReport{}, nil
}

func startProductionDrillRuntime(t *testing.T, store *sqlite.Store, iteration int, operation application.SkillReleaseDrillOperation) (*application.SkillStandaloneRuntime, <-chan error) {
	t.Helper()
	suffix := string(rune('0'+iteration)) + "-" + string(operation)
	worker := &productionDrillNoopWorker{started: make(chan struct{})}
	runtime, err := application.NewSkillStandaloneRuntime(store, worker, productionDrillNoopReconciler{}, application.SkillStandaloneRuntimeConfig{
		Enabled: true, InstallationID: "production-drill", DatabaseID: "production-drill-db", Owner: "owner-" + suffix,
		PollInterval: 10 * time.Millisecond, ReconciliationInterval: 10 * time.Millisecond,
		LeaderLeaseDuration: time.Second, LeaderRenewalInterval: 100 * time.Millisecond, DrainTimeout: time.Second,
	}, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- runtime.Run(context.Background()) }()
	select {
	case <-worker.started:
	case err := <-finished:
		t.Fatalf("production drill runtime failed before readiness: %v", err)
	case <-time.After(time.Second):
		t.Fatal("production drill runtime did not become ready")
	}
	return runtime, finished
}

func openProductionDrillStore(t *testing.T, path string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func productionDrillDigest(seed string) string {
	return "sha256:" + strings.Repeat(string("0123456789abcdef"[len(seed)%16]), 64)
}
