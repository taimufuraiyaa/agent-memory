package config

import (
	"strings"
	"testing"
	"time"
)

func setValidEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("AGENT_MEMORY_SAAS_ENV", "development")
	t.Setenv("AGENT_MEMORY_SAAS_SERVICE", "api")
	t.Setenv("AGENT_MEMORY_SAAS_LISTEN_ADDR", ":8080")
	t.Setenv("AGENT_MEMORY_POSTGRES_URL", "postgres://agent_memory:local@postgres:5432/agent_memory?sslmode=disable")
	t.Setenv("AGENT_MEMORY_OBJECT_ENDPOINT", "http://minio:9000")
	t.Setenv("AGENT_MEMORY_OBJECT_ACCESS_KEY", "local-agent-memory")
	t.Setenv("AGENT_MEMORY_OBJECT_SECRET_KEY", "local-development-only")
	t.Setenv("AGENT_MEMORY_EXPORT_ENCRYPTION_KEY", "local-export-encryption-key-at-least-32-bytes")
	t.Setenv("AGENT_MEMORY_VAULT_ENCRYPTION_KEY", "local-vault-encryption-key-at-least-32-bytes")
	t.Setenv("AGENT_MEMORY_QUEUE_URL", "nats://nats:4222")
	t.Setenv("AGENT_MEMORY_SECRET_REF", "local-development-only")
	t.Setenv("AGENT_MEMORY_DEV_AUTH_TOKEN", "development-token")
	t.Setenv("AGENT_MEMORY_DEV_SUBJECT", "development|member")
	t.Setenv("AGENT_MEMORY_DEV_EMAIL", "member@example.test")
	t.Setenv("AGENT_MEMORY_EDGE_COUNTRY_SECRET", "development-edge-country-secret-32")
}

func TestLoadReadsTypedHostedConfiguration(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_SAAS_SHUTDOWN_TIMEOUT", "12s")
	t.Setenv("AGENT_MEMORY_TRACING_ENABLED", "true")
	t.Setenv("AGENT_MEMORY_TRACING_SAMPLE_RATE", "0.25")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Environment != Development || cfg.Service != API {
		t.Fatalf("unexpected identity: %+v", cfg)
	}
	if cfg.ShutdownTimeout != 12*time.Second {
		t.Fatalf("shutdown timeout = %s, want 12s", cfg.ShutdownTimeout)
	}
	if cfg.TelemetryAddr != ":9090" || !cfg.TracingEnabled || cfg.TracingSampleRate != 0.25 {
		t.Fatalf("unexpected telemetry configuration: %+v", cfg)
	}
	if cfg.PostgresURL == "" || cfg.ObjectEndpoint == "" || cfg.QueueURL == "" {
		t.Fatal("required service endpoints were not loaded")
	}
}

func TestLoadReadsBoundedLocalSemanticRoleConfiguration(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_QUERY_PLANNER_ENABLED", "true")
	t.Setenv("AGENT_MEMORY_QUERY_PLANNER_ENDPOINT", "http://host.docker.internal:11434")
	t.Setenv("AGENT_MEMORY_QUERY_PLANNER_MODEL", "qwen3:8b")
	t.Setenv("AGENT_MEMORY_QUERY_PLANNER_TIMEOUT", "8s")
	t.Setenv("AGENT_MEMORY_RERANKER_ENABLED", "true")
	t.Setenv("AGENT_MEMORY_RERANKER_ENDPOINT", "http://host.docker.internal:11435")
	t.Setenv("AGENT_MEMORY_RERANKER_MODEL", "qwen3-reranker:0.6b")
	t.Setenv("AGENT_MEMORY_RERANKER_TIMEOUT", "12s")
	t.Setenv("AGENT_MEMORY_RERANKER_MIN_RELEVANCE", "0.55")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.QueryPlannerEnabled || cfg.QueryPlannerModel != "qwen3:8b" || cfg.QueryPlannerTimeout != 8*time.Second {
		t.Fatalf("planner config=%+v", cfg)
	}
	if !cfg.RerankerEnabled || cfg.RerankerModel != "qwen3-reranker:0.6b" || cfg.RerankerTimeout != 12*time.Second || cfg.RerankerMinRelevance != 0.55 {
		t.Fatalf("reranker config=%+v", cfg)
	}
}

func TestLoadRejectsIncompleteOrUnboundedLocalSemanticRoles(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_QUERY_PLANNER_ENABLED", "true")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "QUERY_PLANNER") {
		t.Fatalf("missing planner config error=%v", err)
	}

	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_RERANKER_ENABLED", "true")
	t.Setenv("AGENT_MEMORY_RERANKER_ENDPOINT", "http://host.docker.internal:11435")
	t.Setenv("AGENT_MEMORY_RERANKER_MODEL", "reranker")
	t.Setenv("AGENT_MEMORY_RERANKER_MIN_RELEVANCE", "1.1")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "MIN_RELEVANCE") {
		t.Fatalf("invalid reranker threshold error=%v", err)
	}
}

func TestLocalOnboardingRequiresExplicitDevelopmentIdentityOptIn(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_LOCAL_ONBOARDING", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.LocalOnboardingEnabled {
		t.Fatal("expected explicit local onboarding opt-in")
	}

	t.Setenv("AGENT_MEMORY_SAAS_ENV", "staging")
	t.Setenv("AGENT_MEMORY_IDENTITY_MODE", "oidc")
	for _, name := range []string{"AGENT_MEMORY_DEV_AUTH_TOKEN", "AGENT_MEMORY_DEV_SUBJECT", "AGENT_MEMORY_DEV_EMAIL", "AGENT_MEMORY_DEV_DISPLAY_NAME"} {
		t.Setenv(name, "")
	}
	t.Setenv("AGENT_MEMORY_OIDC_ISSUER", "https://identity.example.test")
	t.Setenv("AGENT_MEMORY_OIDC_AUDIENCE", "agent-memory-web")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "LOCAL_ONBOARDING") {
		t.Fatalf("Load() error=%v, want local onboarding boundary failure", err)
	}
}

func TestLocalOnboardingRejectsDevelopmentOIDCMode(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_LOCAL_ONBOARDING", "true")
	t.Setenv("AGENT_MEMORY_IDENTITY_MODE", "oidc")
	for _, name := range []string{"AGENT_MEMORY_DEV_AUTH_TOKEN", "AGENT_MEMORY_DEV_SUBJECT", "AGENT_MEMORY_DEV_EMAIL", "AGENT_MEMORY_DEV_DISPLAY_NAME"} {
		t.Setenv(name, "")
	}
	t.Setenv("AGENT_MEMORY_OIDC_ISSUER", "http://oidc:8082")
	t.Setenv("AGENT_MEMORY_OIDC_AUDIENCE", "agent-memory-local")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "LOCAL_ONBOARDING") {
		t.Fatalf("Load() error=%v, want development OIDC onboarding rejection", err)
	}
}

func TestLoadRejectsInvalidTracingSampleRate(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_TRACING_SAMPLE_RATE", "1.1")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "TRACING_SAMPLE_RATE") {
		t.Fatalf("Load() error = %v, want tracing sample rate failure", err)
	}
}

func TestStagingAPIRequiresManagedOIDCConfiguration(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_SAAS_ENV", "staging")
	t.Setenv("AGENT_MEMORY_DEV_AUTH_TOKEN", "")
	t.Setenv("AGENT_MEMORY_DEV_SUBJECT", "")
	t.Setenv("AGENT_MEMORY_DEV_EMAIL", "")
	t.Setenv("AGENT_MEMORY_DEV_DISPLAY_NAME", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "AGENT_MEMORY_OIDC_ISSUER") {
		t.Fatalf("Load() error = %v, want missing OIDC issuer", err)
	}
}

func TestStagingAPIAcceptsHTTPSOIDCConfiguration(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_SAAS_ENV", "staging")
	t.Setenv("AGENT_MEMORY_DEV_AUTH_TOKEN", "")
	t.Setenv("AGENT_MEMORY_DEV_SUBJECT", "")
	t.Setenv("AGENT_MEMORY_DEV_EMAIL", "")
	t.Setenv("AGENT_MEMORY_DEV_DISPLAY_NAME", "")
	t.Setenv("AGENT_MEMORY_OIDC_ISSUER", "https://identity.example.test")
	t.Setenv("AGENT_MEMORY_OIDC_AUDIENCE", "agent-memory-web")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDCIssuer != "https://identity.example.test" || cfg.OIDCAudience != "agent-memory-web" {
		t.Fatalf("unexpected OIDC configuration: %+v", cfg)
	}
}

func TestDevelopmentOIDCModeRejectsDevelopmentCredentialsThenAcceptsLocalIssuer(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_IDENTITY_MODE", "oidc")
	t.Setenv("AGENT_MEMORY_OIDC_ISSUER", "http://oidc:8082")
	t.Setenv("AGENT_MEMORY_OIDC_AUDIENCE", "agent-memory-local")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "development credentials") {
		t.Fatalf("Load() error = %v, want mixed identity rejection", err)
	}
	for _, name := range []string{"AGENT_MEMORY_DEV_AUTH_TOKEN", "AGENT_MEMORY_DEV_SUBJECT", "AGENT_MEMORY_DEV_EMAIL", "AGENT_MEMORY_DEV_DISPLAY_NAME"} {
		t.Setenv(name, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IdentityMode != IdentityOIDC || cfg.OIDCIssuer != "http://oidc:8082" {
		t.Fatalf("unexpected local OIDC configuration: %+v", cfg)
	}
}

func TestHostedAPIRejectsDevelopmentIdentityMode(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_SAAS_ENV", "staging")
	t.Setenv("AGENT_MEMORY_IDENTITY_MODE", "development")
	for _, name := range []string{"AGENT_MEMORY_DEV_AUTH_TOKEN", "AGENT_MEMORY_DEV_SUBJECT", "AGENT_MEMORY_DEV_EMAIL", "AGENT_MEMORY_DEV_DISPLAY_NAME"} {
		t.Setenv(name, "")
	}
	t.Setenv("AGENT_MEMORY_OIDC_ISSUER", "https://identity.example.test")
	t.Setenv("AGENT_MEMORY_OIDC_AUDIENCE", "agent-memory-web")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "must use OIDC identity") {
		t.Fatalf("Load() error = %v, want hosted identity-mode rejection", err)
	}
}

func TestLoadRejectsMissingRequiredConfiguration(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_POSTGRES_URL", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "AGENT_MEMORY_POSTGRES_URL") {
		t.Fatalf("Load() error = %v, want missing PostgreSQL URL", err)
	}
}

func TestLoadRejectsUnsafeProductionEndpoints(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_SAAS_ENV", "production")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "production") {
		t.Fatalf("Load() error = %v, want production safety failure", err)
	}
}

func TestLoadRejectsDevelopmentIdentityInProduction(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_SAAS_ENV", "production")
	t.Setenv("AGENT_MEMORY_POSTGRES_URL", "postgres://service@database.example.test/db?sslmode=verify-full")
	t.Setenv("AGENT_MEMORY_OBJECT_ENDPOINT", "https://objects.example.test")
	t.Setenv("AGENT_MEMORY_SECRET_REF", "secret-manager://production/api")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "development identity") {
		t.Fatalf("Load() error = %v, want development identity rejection", err)
	}
}

func TestSummaryRedactsSensitiveConfiguration(t *testing.T) {
	setValidEnvironment(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	summary := cfg.Summary()
	for _, secret := range []string{"agent_memory:local", "local-development-only"} {
		if strings.Contains(summary, secret) {
			t.Fatalf("Summary() leaked %q: %s", secret, summary)
		}
	}
}

func TestProductionWorkerRejectsDevelopmentEmbeddingScaffold(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_SAAS_SERVICE", "worker")
	t.Setenv("AGENT_MEMORY_SAAS_ENV", "production")
	t.Setenv("AGENT_MEMORY_POSTGRES_URL", "postgres://service@database.example.test/db?sslmode=verify-full")
	t.Setenv("AGENT_MEMORY_OBJECT_ENDPOINT", "https://objects.example.test")
	t.Setenv("AGENT_MEMORY_SECRET_REF", "secret-manager://production/worker")
	t.Setenv("AGENT_MEMORY_DEV_AUTH_TOKEN", "")
	t.Setenv("AGENT_MEMORY_DEV_SUBJECT", "")
	t.Setenv("AGENT_MEMORY_DEV_EMAIL", "")
	t.Setenv("AGENT_MEMORY_DEV_DISPLAY_NAME", "")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "embedding scaffold") {
		t.Fatalf("Load() error=%v, want production model safety failure", err)
	}
}

func TestProductionWorkerAcceptsCompleteManagedModelRoute(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_SAAS_SERVICE", "worker")
	t.Setenv("AGENT_MEMORY_SAAS_ENV", "production")
	t.Setenv("AGENT_MEMORY_POSTGRES_URL", "postgres://service@database.example.test/db?sslmode=verify-full")
	t.Setenv("AGENT_MEMORY_OBJECT_ENDPOINT", "https://objects.example.test")
	t.Setenv("AGENT_MEMORY_SECRET_REF", "secret-manager://production/worker")
	t.Setenv("AGENT_MEMORY_DEV_AUTH_TOKEN", "")
	t.Setenv("AGENT_MEMORY_DEV_SUBJECT", "")
	t.Setenv("AGENT_MEMORY_DEV_EMAIL", "")
	t.Setenv("AGENT_MEMORY_MODEL_PROVIDER", "openai-compatible")
	t.Setenv("AGENT_MEMORY_MODEL_ENDPOINT", "https://models.example.test")
	t.Setenv("AGENT_MEMORY_MODEL_API_KEY", "provider-secret")
	t.Setenv("AGENT_MEMORY_MODEL_VERSION", "private-route-v1")
	t.Setenv("AGENT_MEMORY_MODEL_DIMENSION", "1536")
	t.Setenv("AGENT_MEMORY_MODEL_RETENTION", "provider-zero-retention")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelDimension != 1536 || cfg.ModelEndpoint != "https://models.example.test" {
		t.Fatalf("unexpected managed model configuration: %+v", cfg)
	}
}

func TestManagedModelRouteRejectsHTTPOutsideDevelopment(t *testing.T) {
	setValidEnvironment(t)
	t.Setenv("AGENT_MEMORY_SAAS_SERVICE", "worker")
	t.Setenv("AGENT_MEMORY_SAAS_ENV", "staging")
	t.Setenv("AGENT_MEMORY_DEV_AUTH_TOKEN", "")
	t.Setenv("AGENT_MEMORY_DEV_SUBJECT", "")
	t.Setenv("AGENT_MEMORY_DEV_EMAIL", "")
	t.Setenv("AGENT_MEMORY_MODEL_PROVIDER", "openai-compatible")
	t.Setenv("AGENT_MEMORY_MODEL_ENDPOINT", "http://models.example.test")
	t.Setenv("AGENT_MEMORY_MODEL_API_KEY", "provider-secret")
	t.Setenv("AGENT_MEMORY_MODEL_VERSION", "private-route-v1")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "must use HTTPS") {
		t.Fatalf("Load() error=%v, want HTTPS model endpoint failure", err)
	}
}

func TestServiceConfigurationRequiresOnlyItsCapabilities(t *testing.T) {
	t.Run("migration only receives database", func(t *testing.T) {
		setValidEnvironment(t)
		t.Setenv("AGENT_MEMORY_SAAS_SERVICE", "migration")
		for _, name := range []string{"AGENT_MEMORY_OBJECT_ENDPOINT", "AGENT_MEMORY_OBJECT_ACCESS_KEY", "AGENT_MEMORY_OBJECT_SECRET_KEY", "AGENT_MEMORY_EXPORT_ENCRYPTION_KEY", "AGENT_MEMORY_VAULT_ENCRYPTION_KEY", "AGENT_MEMORY_QUEUE_URL", "AGENT_MEMORY_SECRET_REF"} {
			t.Setenv(name, "")
		}
		if _, err := Load(); err != nil {
			t.Fatalf("migration should not require unrelated capabilities: %v", err)
		}
	})

	t.Run("reconciler receives database and object store", func(t *testing.T) {
		setValidEnvironment(t)
		t.Setenv("AGENT_MEMORY_SAAS_SERVICE", "reconciler")
		for _, name := range []string{"AGENT_MEMORY_EXPORT_ENCRYPTION_KEY", "AGENT_MEMORY_VAULT_ENCRYPTION_KEY", "AGENT_MEMORY_QUEUE_URL"} {
			t.Setenv(name, "")
		}
		if _, err := Load(); err != nil {
			t.Fatalf("reconciler should not require worker capabilities: %v", err)
		}
	})

	t.Run("API does not receive queue or vault decryption", func(t *testing.T) {
		setValidEnvironment(t)
		t.Setenv("AGENT_MEMORY_QUEUE_URL", "")
		t.Setenv("AGENT_MEMORY_VAULT_ENCRYPTION_KEY", "")
		if _, err := Load(); err != nil {
			t.Fatalf("API should not require worker capabilities: %v", err)
		}
	})
}
