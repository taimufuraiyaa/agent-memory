package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	baseobservability "github.com/taimufuraiyaa/agent-memory/internal/observability"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/billing"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/config"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/deletion"
	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/graphindex"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/graphworker"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/modelgateway"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/objectcustody"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/outbox"
	saaspostgres "github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retention"
	searchservice "github.com/taimufuraiyaa/agent-memory/internal/saas/search"
	sourceservice "github.com/taimufuraiyaa/agent-memory/internal/saas/source"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/telemetry"
)

func main() {
	cfg, err := config.LoadFor(config.Worker)
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
	broker, err := outbox.NewNATSBroker(cfg.QueueURL)
	if err != nil {
		return fmt.Errorf("connect durable event bus: %w", err)
	}
	defer broker.Close()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", cfg.Service, "environment", cfg.Environment)
	observer := telemetry.New(string(cfg.Service), logger)
	go func() {
		if err := observer.ServeMetrics(ctx, cfg.TelemetryAddr); err != nil {
			logger.Error("telemetry server failed", "error_class", telemetry.ErrorClass(err))
		}
	}()
	observer.RecordComponent("database", "connect", "success", 0)
	observer.RecordComponent("queue", "connect", "success", 0)
	logger.Info("outbox publisher started")
	objects, err := exportservice.NewMinIOStore(cfg.ObjectEndpoint, cfg.ObjectAccessKey, cfg.ObjectSecretKey)
	if err != nil {
		return fmt.Errorf("open export object store: %w", err)
	}
	observer.RecordComponent("object_storage", "connect", "success", 0)
	if cfg.GraphRAGEnabled {
		if err := startHostedGraphServices(ctx, cfg, pool, observer, logger); err != nil {
			return err
		}
	}
	exports, err := exportservice.NewService(exportservice.NewPostgresRepository(pool), objects, cfg.ExportEncryptionKey, nil)
	if err != nil {
		return err
	}
	go exports.Run(ctx, time.Second, componentErrorReporter(observer, logger, "worker", "export", "export processing cycle failed"))
	quarantine, err := sourceservice.NewMinIOQuarantine(cfg.ObjectEndpoint, cfg.ObjectAccessKey, cfg.ObjectSecretKey)
	if err != nil {
		return fmt.Errorf("open source object stores: %w", err)
	}
	retentionRegistry := retention.NewRegistry(pool)
	deletionRepository := deletion.NewPostgresRepository(pool, retentionRegistry)
	deletionService := deletion.NewService(deletionRepository, map[string]deletion.Purger{
		"object": deletion.NewObjectPurger(quarantine), "database": deletion.NewDatabasePurger(pool, "database"),
		"index": deletion.NewDatabasePurger(pool, "index"), "cache": deletion.NewDatabasePurger(pool, "cache"),
		"queue": deletion.NewDatabasePurger(pool, "queue"),
	}, nil)
	go runDeletionWorker(ctx, deletionRepository, deletionService, logger)
	go runAccountDeletionWorker(ctx, deletion.NewAccountService(pool, deletionRepository, retentionRegistry, nil), logger)
	sourceProcessor, err := sourceservice.NewProcessor(sourceservice.NewPostgresRepository(pool), quarantine, cfg.VaultEncryptionKey, nil)
	if err != nil {
		return err
	}
	go sourceProcessor.Run(ctx, time.Second, componentErrorReporter(observer, logger, "worker", "source_validation", "source validation cycle failed"))
	extractionProcessor, err := sourceservice.NewExtractionProcessor(sourceservice.NewPostgresRepository(pool), quarantine, cfg.VaultEncryptionKey, nil)
	if err != nil {
		return err
	}
	go extractionProcessor.Run(ctx, time.Second, componentErrorReporter(observer, logger, "worker", "source_extraction", "source extraction cycle failed"))
	fullTextProjector, err := searchservice.NewFullTextProjector(searchservice.NewPostgresRepository(pool), nil)
	if err != nil {
		return err
	}
	go fullTextProjector.Run(ctx, time.Second, componentErrorReporter(observer, logger, "worker", "fulltext_projection", "full-text projection cycle failed"))
	gatewayProvider, err := modelgateway.NewRuntimeProvider(modelgateway.RuntimeProviderConfig{
		Name: cfg.ModelProvider, Endpoint: cfg.ModelEndpoint, APIKey: cfg.ModelAPIKey,
		Model: cfg.ModelVersion, Dimension: cfg.ModelDimension, Retention: cfg.ModelRetention,
	})
	if err != nil {
		return err
	}
	observer.RecordComponent("model_gateway", "configure", "success", 0)
	gateway, err := modelgateway.New(modelgateway.Config{
		Providers: []modelgateway.Provider{gatewayProvider},
		Policies: []modelgateway.ProviderPolicy{{
			Provider:             gatewayProvider.Name(),
			Models:               []string{gatewayProvider.ModelVersion()},
			RetentionPolicies:    []string{cfg.ModelRetention},
			MaxInputTokens:       32768,
			Timeout:              30 * time.Second,
			MaxRetries:           1,
			FailureThreshold:     3,
			Cooldown:             time.Minute,
			InputCostPerMillion:  0,
			OutputCostPerMillion: 0,
		}},
		Quota: billing.NewQuota(pool),
	}, modelgateway.NewObservedPostgresUsageSink(pool, func(usage modelgateway.Usage) {
		observer.RecordComponent("model_gateway", string(usage.Operation), usage.Outcome, usage.EstimatedCostMicros)
	}), modelgateway.ContentRedactor{}, nil)
	if err != nil {
		return err
	}
	vectorProjector, err := searchservice.NewVectorProjector(searchservice.NewPostgresRepository(pool), gateway, gatewayProvider.Name(), gatewayProvider.ModelVersion(), nil)
	if err != nil {
		return err
	}
	go vectorProjector.Run(ctx, time.Second, componentErrorReporter(observer, logger, "worker", "vector_projection", "vector projection cycle failed"))
	outbox.NewPublisher(outbox.NewPostgresRepository(pool), broker, nil).Run(ctx, time.Second, func(err error) {
		observer.RecordComponent("queue", "publish", "error", 0)
		logger.Error("outbox publication cycle failed", "error_class", telemetry.ErrorClass(err))
	})
	logger.Info("outbox publisher stopped")
	return nil
}

func startHostedGraphServices(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, observer *telemetry.Observer, logger *slog.Logger) error {
	privateKey, err := base64.StdEncoding.DecodeString(cfg.GraphBundleSigningKey)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("hosted graph bundle signing key is invalid")
	}
	graphObjects, err := objectcustody.NewMinIOGraphObjects(cfg.ObjectEndpoint, cfg.ObjectAccessKey, cfg.ObjectSecretKey)
	if err != nil {
		return err
	}
	transport, err := graphworker.NewNATSTransport(cfg.QueueURL, "agent-memory-general-worker")
	if err != nil {
		return err
	}
	publicKey := ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey)
	dispatcher, err := graphindex.NewDispatcher(
		graphindex.NewPostgresProjectionRepository(pool), objectcustody.NewGraphBundleObjectStore(graphObjects, publicKey), transport,
		"agent-memory-general-worker", 10*time.Minute, ed25519.PrivateKey(privateKey), time.Now,
	)
	if err != nil {
		transport.Close()
		return err
	}
	repository := saaspostgres.NewGraphIndexRepository(pool)
	loader, err := graphindex.NewObjectArtifactLoader(graphObjects, time.Now)
	if err != nil {
		transport.Close()
		return err
	}
	completion, err := graphindex.NewService(repository, loader, repository, "agent-memory-graph-importer", 10*time.Minute, time.Now)
	if err != nil {
		transport.Close()
		return err
	}
	go func() {
		defer transport.Close()
		if err := transport.RunCompletions(ctx, "agent-memory-graph-importer", completion, componentErrorReporter(observer, logger, "graph", "completion", "graph completion cycle failed")); err != nil && ctx.Err() == nil {
			logger.Error("graph completion consumer stopped", "error_class", telemetry.ErrorClass(err))
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, err := dispatcher.RunOnce(ctx)
				componentErrorReporter(observer, logger, "graph", "dispatch", "graph dispatch cycle failed")(err)
			}
		}
	}()
	logger.Info("hosted graph dispatcher and completion importer started")
	return nil
}

func componentErrorReporter(observer *telemetry.Observer, logger *slog.Logger, component, operation, message string) func(error) {
	return func(err error) {
		observer.RecordComponent(component, operation, telemetry.StatusOutcome(err), 0)
		if err != nil {
			logger.Error(message, "error_class", telemetry.ErrorClass(err))
		}
	}
}

type accountDeletionRunner interface {
	PendingTenantIDs(context.Context) ([]string, error)
	RunOnce(context.Context, string) (bool, error)
}

func runAccountDeletionWorker(ctx context.Context, service accountDeletionRunner, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := service.PendingTenantIDs(ctx)
			if err != nil {
				logger.Error("account deletion scan failed", "error_class", telemetry.ErrorClass(err))
				continue
			}
			for _, tenantID := range ids {
				if _, err := service.RunOnce(ctx, tenantID); err != nil {
					logger.Error("account deletion failed", "tenant_id", tenantID, "error_class", telemetry.ErrorClass(err))
				}
			}
		}
	}
}

type deletionTenantLister interface {
	PendingTenantIDs(context.Context) ([]string, error)
}

func runDeletionWorker(ctx context.Context, tenants deletionTenantLister, service *deletion.Service, logger *slog.Logger) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := tenants.PendingTenantIDs(ctx)
			if err != nil {
				logger.Error("deletion tenant scan failed", "error_class", telemetry.ErrorClass(err))
				continue
			}
			for _, tenantID := range ids {
				if _, err := service.RunOnce(ctx, tenantID); err != nil {
					logger.Error("deletion cycle failed", "tenant_id", tenantID, "error_class", telemetry.ErrorClass(err))
				}
			}
		}
	}
}
