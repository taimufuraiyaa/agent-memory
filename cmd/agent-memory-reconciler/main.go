package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	baseobservability "github.com/taimufuraiyaa/agent-memory/internal/observability"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/billing"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/config"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retention"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/security"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/skillreconciler"
	sourceservice "github.com/taimufuraiyaa/agent-memory/internal/saas/source"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/telemetry"
)

type hostedSkillReconcilerRuntime interface {
	Run(context.Context)
}

var buildHostedSkillReconcilerRuntime = func(context.Context, *saaspostgres.SkillOrchestratorRepository, skillreconciler.RuntimeConfig) (hostedSkillReconcilerRuntime, error) {
	return nil, errors.New("hosted skill reconciliation domains are not linked into this process")
}

func main() {
	cfg, err := config.LoadFor(config.Reconciler)
	if err == nil {
		err = run(cfg)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdownTracing, err := baseobservability.InitTracing(baseobservability.TracingConfig{Enabled: cfg.TracingEnabled, ServiceName: string(cfg.Service), Environment: string(cfg.Environment), SampleRate: cfg.TracingSampleRate})
	if err != nil {
		return fmt.Errorf("initialize tracing: %w", err)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()
	pool, err := saaspostgres.Open(ctx, cfg.PostgresURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	skillConfiguration, err := skillreconciler.LoadRuntimeConfig()
	if err != nil {
		return fmt.Errorf("load skill reconciler configuration: %w", err)
	}
	if skillConfiguration.Enabled {
		skillRuntime, buildErr := buildHostedSkillReconcilerRuntime(ctx, saaspostgres.NewSkillOrchestratorRepository(pool), skillConfiguration)
		if buildErr != nil {
			return fmt.Errorf("build skill reconciler: %w", buildErr)
		}
		go skillRuntime.Run(ctx)
	}
	objects, err := sourceservice.NewMinIOQuarantine(cfg.ObjectEndpoint, cfg.ObjectAccessKey, cfg.ObjectSecretKey)
	if err != nil {
		return fmt.Errorf("open source object stores: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", cfg.Service, "environment", cfg.Environment)
	observer := telemetry.New(string(cfg.Service), logger)
	go func() {
		if err := observer.ServeMetrics(ctx, cfg.TelemetryAddr); err != nil {
			logger.Error("telemetry server failed", "error_class", telemetry.ErrorClass(err))
		}
	}()
	observer.RecordComponent("database", "connect", "success", 0)
	observer.RecordComponent("object_storage", "connect", "success", 0)
	auditPolicy, err := retention.NewRegistry(pool).Active(ctx, "audit_events")
	if err != nil {
		return fmt.Errorf("load audit retention policy: %w", err)
	}
	auditStore, err := audit.NewMinIOArchiveStore(cfg.ObjectEndpoint, cfg.ObjectAccessKey, cfg.ObjectSecretKey, auditPolicy.Duration)
	if err != nil {
		return fmt.Errorf("open audit archive: %w", err)
	}
	auditRepository := audit.NewPostgresRepository(pool)
	go audit.NewArchiver(auditRepository, auditStore, time.Now).Run(ctx, auditRepository, time.Minute, func(count int, err error) {
		if err != nil {
			observer.RecordComponent("worker", "audit_archive", "error", 0)
			logger.Error("audit archive delivery failed", "error_class", telemetry.ErrorClass(err))
			return
		}
		observer.RecordComponent("worker", "audit_archive", "success", 0)
		if count > 0 {
			logger.Info("audit archive delivery completed", "events", count)
		}
	})
	securityRepository := security.NewPostgresRepository(pool)
	go security.NewDetector(auditRepository, securityRepository, auditRepository, time.Now).Run(ctx, time.Minute, func(count int, err error) {
		if err != nil {
			observer.RecordComponent("worker", "security_detection", "error", 0)
			logger.Error("security detection failed", "error_class", telemetry.ErrorClass(err))
			return
		}
		observer.RecordComponent("worker", "security_detection", "success", 0)
		if count > 0 {
			logger.Warn("security findings created", "findings", count)
		}
	})
	billingRepository := billing.NewRepository(pool, nil)
	go runUsageReconciliation(ctx, auditRepository, billingRepository, logger)
	logger.Info("vault reconciler started")
	sourceservice.NewVaultReconciler(sourceservice.NewPostgresRepository(pool), objects).Run(ctx, time.Minute, func(findings []sourceservice.VaultFinding, err error) {
		if err != nil {
			observer.RecordComponent("object_storage", "vault_reconcile", "error", 0)
			logger.Error("vault reconciliation failed", "error_class", telemetry.ErrorClass(err))
			return
		}
		observer.RecordComponent("object_storage", "vault_reconcile", "success", 0)
		for _, finding := range findings {
			logger.Warn("vault reconciliation finding", "tenant_id", finding.TenantID, "kind", finding.Kind, "references", finding.References)
		}
	})
	logger.Info("vault reconciler stopped")
	return nil
}

type usageTenantLister interface {
	ActiveTenantIDs(context.Context) ([]string, error)
}
type usageReconciler interface {
	ReconcileUsage(context.Context, string, time.Time) (map[string]int64, error)
}

func runUsageReconciliation(ctx context.Context, tenants usageTenantLister, reconciler usageReconciler, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	run := func() {
		ids, err := tenants.ActiveTenantIDs(ctx)
		if err != nil {
			logger.Error("usage tenant scan failed", "error_class", telemetry.ErrorClass(err))
			return
		}
		for _, tenantID := range ids {
			if _, err := reconciler.ReconcileUsage(ctx, tenantID, time.Now().UTC()); err != nil {
				logger.Error("usage reconciliation failed", "tenant_id", tenantID, "error_class", telemetry.ErrorClass(err))
			}
		}
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
