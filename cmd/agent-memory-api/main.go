package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/nats-io/nats.go"
	"github.com/taimufuraiyaa/agent-memory/internal/attestation"
	baseobservability "github.com/taimufuraiyaa/agent-memory/internal/observability"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/api"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/attestationstore"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/audit"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/auth"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/billing"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/config"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/control"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/credential"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/deletion"
	exportservice "github.com/taimufuraiyaa/agent-memory/internal/saas/export"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/importer"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/launch"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/memory"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/modelgateway"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/privacy"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retention"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/retrieval"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/review"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/runtime"
	searchservice "github.com/taimufuraiyaa/agent-memory/internal/saas/search"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/security"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/semantic"
	sourceservice "github.com/taimufuraiyaa/agent-memory/internal/saas/source"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/telemetry"
)

type readinessPinger interface {
	Ping(context.Context) error
}

type readinessBucketChecker interface {
	BucketExists(context.Context, string) (bool, error)
}

type readinessQueue interface {
	FlushWithContext(context.Context) error
}

func main() {
	cfg, err := config.LoadFor(config.API)
	if err == nil {
		err = run(cfg)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(cfg config.Config) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	shutdownTracing, err := baseobservability.InitTracing(baseobservability.TracingConfig{Enabled: cfg.TracingEnabled, ServiceName: string(cfg.Service), Environment: string(cfg.Environment), SampleRate: cfg.TracingSampleRate})
	if err != nil {
		return fmt.Errorf("initialize tracing: %w", err)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()
	observer := telemetry.New(string(cfg.Service), logger)
	identityAuthenticator, profiles, err := newIdentityBoundary(context.Background(), cfg)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(context.Background(), cfg.PostgresURL)
	if err != nil {
		return fmt.Errorf("open hosted PostgreSQL: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(context.Background()); err != nil {
		return fmt.Errorf("ping hosted PostgreSQL: %w", err)
	}
	observer.RecordComponent("database", "connect", "success", 0)
	objectEndpoint, err := url.Parse(cfg.ObjectEndpoint)
	if err != nil || objectEndpoint.Host == "" {
		return errors.New("object readiness endpoint is invalid")
	}
	readinessObjects, err := minio.New(objectEndpoint.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.ObjectAccessKey, cfg.ObjectSecretKey, ""),
		Secure: objectEndpoint.Scheme == "https",
	})
	if err != nil {
		return fmt.Errorf("open object readiness client: %w", err)
	}
	queueConnection, err := nats.Connect(cfg.QueueURL,
		nats.Name("agent-memory-api-readiness"),
		nats.Timeout(2*time.Second),
		nats.ReconnectWait(250*time.Millisecond),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return fmt.Errorf("open queue readiness connection: %w", err)
	}
	defer queueConnection.Close()
	accounts := control.NewPostgresStore(pool)
	memoryRepository := memory.NewPostgresRepository(pool)
	credentials := credential.NewService(credential.NewPostgresRepository(pool), nil)
	objects, err := exportservice.NewMinIOStore(cfg.ObjectEndpoint, cfg.ObjectAccessKey, cfg.ObjectSecretKey)
	if err != nil {
		return fmt.Errorf("open export object store: %w", err)
	}
	observer.RecordComponent("object_storage", "connect", "success", 0)
	exports, err := exportservice.NewService(exportservice.NewPostgresRepository(pool), objects, cfg.ExportEncryptionKey, nil)
	if err != nil {
		return err
	}
	requestAuthenticator := auth.NewCompositeAuthenticator(identityAuthenticator, credential.NewTokenAuthenticator(credentials))
	attestations := attestation.NewService(attestationstore.NewPostgresStore(pool))
	quarantine, err := sourceservice.NewMinIOQuarantine(cfg.ObjectEndpoint, cfg.ObjectAccessKey, cfg.ObjectSecretKey)
	if err != nil {
		return fmt.Errorf("open quarantine object store: %w", err)
	}
	sourceUploads := sourceservice.NewService(sourceservice.NewPostgresRepository(pool), attestations, quarantine, nil)
	sourceCatalog := sourceservice.NewCatalogService(sourceservice.NewPostgresRepository(pool), nil)
	gatewayProvider, err := modelgateway.NewRuntimeProvider(modelgateway.RuntimeProviderConfig{
		Name: cfg.ModelProvider, Endpoint: cfg.ModelEndpoint, APIKey: cfg.ModelAPIKey,
		Model: cfg.ModelVersion, Dimension: cfg.ModelDimension, Retention: cfg.ModelRetention,
	})
	if err != nil {
		return err
	}
	usageSink := modelgateway.NewObservedPostgresUsageSink(pool, func(usage modelgateway.Usage) {
		observer.RecordComponent("model_gateway", string(usage.Operation), usage.Outcome, usage.EstimatedCostMicros)
	})
	gateway, err := modelgateway.New(modelgateway.Config{Providers: []modelgateway.Provider{gatewayProvider}, Policies: []modelgateway.ProviderPolicy{{Provider: gatewayProvider.Name(), Models: []string{gatewayProvider.ModelVersion()}, RetentionPolicies: []string{cfg.ModelRetention}, MaxInputTokens: 32768, Timeout: 30 * time.Second, MaxRetries: 1, FailureThreshold: 3, Cooldown: time.Minute}}, Quota: billing.NewQuota(pool)}, usageSink, modelgateway.ContentRedactor{}, nil)
	if err != nil {
		return err
	}
	retrievalRepository := retrieval.NewPostgresRepository(pool)
	semanticOptions, err := semanticRetrievalOptions(cfg)
	if err != nil {
		return err
	}
	sourceQueries, err := retrieval.NewService(retrievalRepository, searchservice.NewPostgresRepository(pool), gateway, nil, semanticOptions...)
	if err != nil {
		return err
	}
	memoryReviews := review.NewService(review.NewPostgresRepository(pool), nil)
	billingRepository := billing.NewRepository(pool, nil)
	hostedMemories := memory.NewService(memoryRepository, nil)
	hostedMemorySearch := memory.NewSearchService(memory.NewPostgresSearchRepository(pool))
	hostedWorkflows := memory.NewWorkflowService(memoryRepository, nil)
	retentionRegistry := retention.NewRegistry(pool)
	deletionRepository := deletion.NewPostgresRepository(pool, retentionRegistry)
	launchControls := launch.NewService(pool, nil)
	var localOwner *control.LocalOwnerService
	if cfg.LocalOnboardingEnabled {
		localOwner, err = control.NewLocalOwnerServiceWithInitializer(accounts, cfg.DevSubject, launchControls, nil)
		if err != nil {
			return fmt.Errorf("initialize local owner onboarding: %w", err)
		}
	}
	sourceUploads.SetRolloutGate(launchControls)
	handler, err := api.NewHandler(api.Dependencies{
		Readiness: func(ctx context.Context) error {
			return checkDependencies(ctx, pool, readinessObjects, queueConnection)
		},
		Authenticator:     requestAuthenticator,
		Profiles:          profiles,
		Memberships:       accounts,
		Signup:            control.NewSignupServiceWithAdmission(accounts, launchControls, nil),
		Attestations:      attestations,
		Memories:          hostedMemories,
		MemorySearch:      hostedMemorySearch,
		Credentials:       credentials,
		Workflows:         hostedWorkflows,
		Exports:           exports,
		SourceUploads:     sourceUploads,
		SourceCatalog:     sourceCatalog,
		SourceQueries:     sourceQueries,
		MemoryReviews:     memoryReviews,
		Audit:             audit.NewService(pool, nil),
		Deletions:         deletion.NewService(deletionRepository, nil, nil),
		AccountDeletion:   deletion.NewAccountService(pool, deletionRepository, retentionRegistry, nil),
		SecurityGate:      security.NewGate(pool),
		Privacy:           privacy.NewService(pool),
		Billing:           billing.NewService(billingRepository),
		Imports:           importer.NewService(pool, hostedMemories, hostedWorkflows, sourceUploads, attestations, billingRepository, nil),
		CountryVerifier:   launch.NewCountryVerifier(cfg.EdgeCountrySecret, nil),
		Telemetry:         observer,
		LocalOwner:        localOwner,
		LocalSessionToken: cfg.DevAuthToken,
	})
	if err != nil {
		return err
	}
	return runtime.RunHTTP(cfg, handler)
}

func semanticRetrievalOptions(cfg config.Config) ([]retrieval.Option, error) {
	options := []retrieval.Option{}
	if cfg.QueryPlannerEnabled {
		planner, err := semantic.NewHTTPPlanner(semantic.PlannerConfig{
			Endpoint: cfg.QueryPlannerEndpoint, Model: cfg.QueryPlannerModel, APIKey: cfg.QueryPlannerAPIKey,
			Timeout: cfg.QueryPlannerTimeout, WarmupTimeout: cfg.QueryPlannerWarmupTimeout,
			CacheCapacity: cfg.QueryPlannerCacheCapacity, CacheTTL: cfg.QueryPlannerCacheTTL,
			AllowLoopback: true, AllowInstallationHost: true,
		})
		if err != nil {
			return nil, fmt.Errorf("configure local query planner: %w", err)
		}
		if cfg.QueryPlannerWarmupEnabled {
			warmupContext, cancel := context.WithTimeout(context.Background(), cfg.QueryPlannerWarmupTimeout)
			_ = planner.Warm(warmupContext, cfg.QueryPlannerKeepAlive)
			cancel()
		}
		options = append(options, retrieval.WithQueryPlanner(planner))
	}
	if cfg.RerankerEnabled {
		reranker, err := semantic.NewHTTPReranker(semantic.RerankerConfig{
			Endpoint: cfg.RerankerEndpoint, Model: cfg.RerankerModel, APIKey: cfg.RerankerAPIKey,
			Timeout: cfg.RerankerTimeout, AllowLoopback: true, AllowInstallationHost: true,
		})
		if err != nil {
			return nil, fmt.Errorf("configure local window reranker: %w", err)
		}
		options = append(options, retrieval.WithWindowReranker(reranker, cfg.RerankerMinRelevance))
	}
	return options, nil
}

func checkDependencies(ctx context.Context, database readinessPinger, objects readinessBucketChecker, queue readinessQueue) error {
	if database == nil || objects == nil || queue == nil {
		return errors.New("readiness dependencies are incomplete")
	}
	if err := database.Ping(ctx); err != nil {
		return errors.New("database is unavailable")
	}
	exists, err := objects.BucketExists(ctx, "agent-memory-quarantine")
	if err != nil || !exists {
		return errors.New("object storage is unavailable")
	}
	if err := queue.FlushWithContext(ctx); err != nil {
		return errors.New("queue is unavailable")
	}
	return nil
}

func newIdentityBoundary(ctx context.Context, cfg config.Config) (auth.Authenticator, api.ProfileVerifier, error) {
	switch cfg.IdentityMode {
	case config.IdentityDevelopment:
		if cfg.Environment != config.Development {
			return nil, nil, errors.New("hosted API cannot use development identity")
		}
		development, err := auth.NewDevelopmentAuthenticator(cfg.DevAuthToken, cfg.DevSubject, cfg.DevEmail, cfg.DevDisplayName)
		if err != nil {
			return nil, nil, err
		}
		return development, development, nil
	case config.IdentityOIDC:
		managed, err := auth.NewOIDCAuthenticator(ctx, cfg.OIDCIssuer, cfg.OIDCAudience)
		if err != nil {
			return nil, nil, fmt.Errorf("initialize managed identity: %w", err)
		}
		return managed, managed, nil
	default:
		return nil, nil, errors.New("API identity mode is invalid")
	}
}
