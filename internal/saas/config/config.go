// Package config loads and validates the hosted service configuration.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Environment string

const (
	Development Environment = "development"
	Staging     Environment = "staging"
	Production  Environment = "production"
)

type Service string

const (
	API        Service = "api"
	Worker     Service = "worker"
	Reconciler Service = "reconciler"
	Migration  Service = "migration"
)

type IdentityMode string

const (
	IdentityDevelopment IdentityMode = "development"
	IdentityOIDC        IdentityMode = "oidc"
)

type Config struct {
	Environment               Environment
	Service                   Service
	ListenAddr                string
	TelemetryAddr             string
	TracingEnabled            bool
	TracingSampleRate         float64
	PostgresURL               string
	ObjectEndpoint            string
	ObjectAccessKey           string
	ObjectSecretKey           string
	ExportEncryptionKey       string
	VaultEncryptionKey        string
	ModelProvider             string
	ModelDirectory            string
	ModelRetention            string
	ModelEndpoint             string
	ModelAPIKey               string
	ModelVersion              string
	ModelDimension            int
	QueryPlannerEnabled       bool
	QueryPlannerEndpoint      string
	QueryPlannerModel         string
	QueryPlannerAPIKey        string
	QueryPlannerTimeout       time.Duration
	QueryPlannerWarmupEnabled bool
	QueryPlannerWarmupTimeout time.Duration
	QueryPlannerKeepAlive     time.Duration
	QueryPlannerCacheTTL      time.Duration
	QueryPlannerCacheCapacity int
	RerankerEnabled           bool
	RerankerEndpoint          string
	RerankerModel             string
	RerankerAPIKey            string
	RerankerTimeout           time.Duration
	RerankerMinRelevance      float64
	QueueURL                  string
	SecretRef                 string
	DevAuthToken              string
	DevSubject                string
	DevEmail                  string
	DevDisplayName            string
	LocalOnboardingEnabled    bool
	IdentityMode              IdentityMode
	OIDCIssuer                string
	OIDCAudience              string
	EdgeCountrySecret         string
	ShutdownTimeout           time.Duration
}

func Load() (Config, error) {
	environment := Environment(envOr("AGENT_MEMORY_SAAS_ENV", string(Development)))
	identityMode := IdentityMode(strings.TrimSpace(os.Getenv("AGENT_MEMORY_IDENTITY_MODE")))
	if identityMode == "" {
		if environment == Development {
			identityMode = IdentityDevelopment
		} else {
			identityMode = IdentityOIDC
		}
	}
	cfg := Config{
		Environment:               environment,
		Service:                   Service(strings.TrimSpace(os.Getenv("AGENT_MEMORY_SAAS_SERVICE"))),
		ListenAddr:                envOr("AGENT_MEMORY_SAAS_LISTEN_ADDR", ":8080"),
		TelemetryAddr:             envOr("AGENT_MEMORY_TELEMETRY_LISTEN_ADDR", ":9090"),
		TracingEnabled:            strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_MEMORY_TRACING_ENABLED")), "true"),
		TracingSampleRate:         0.1,
		PostgresURL:               strings.TrimSpace(os.Getenv("AGENT_MEMORY_POSTGRES_URL")),
		ObjectEndpoint:            strings.TrimSpace(os.Getenv("AGENT_MEMORY_OBJECT_ENDPOINT")),
		ObjectAccessKey:           strings.TrimSpace(os.Getenv("AGENT_MEMORY_OBJECT_ACCESS_KEY")),
		ObjectSecretKey:           strings.TrimSpace(os.Getenv("AGENT_MEMORY_OBJECT_SECRET_KEY")),
		ExportEncryptionKey:       strings.TrimSpace(os.Getenv("AGENT_MEMORY_EXPORT_ENCRYPTION_KEY")),
		VaultEncryptionKey:        strings.TrimSpace(os.Getenv("AGENT_MEMORY_VAULT_ENCRYPTION_KEY")),
		ModelProvider:             envOr("AGENT_MEMORY_MODEL_PROVIDER", "local-minilm-scaffold"),
		ModelDirectory:            envOr("AGENT_MEMORY_MODEL_DIR", "/tmp/agent-memory-models"),
		ModelRetention:            envOr("AGENT_MEMORY_MODEL_RETENTION", "local-only"),
		ModelEndpoint:             strings.TrimRight(strings.TrimSpace(os.Getenv("AGENT_MEMORY_MODEL_ENDPOINT")), "/"),
		ModelAPIKey:               strings.TrimSpace(os.Getenv("AGENT_MEMORY_MODEL_API_KEY")),
		ModelVersion:              envOr("AGENT_MEMORY_MODEL_VERSION", "local-hash-v1"),
		ModelDimension:            384,
		QueryPlannerEnabled:       strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_MEMORY_QUERY_PLANNER_ENABLED")), "true"),
		QueryPlannerEndpoint:      strings.TrimRight(strings.TrimSpace(os.Getenv("AGENT_MEMORY_QUERY_PLANNER_ENDPOINT")), "/"),
		QueryPlannerModel:         strings.TrimSpace(os.Getenv("AGENT_MEMORY_QUERY_PLANNER_MODEL")),
		QueryPlannerAPIKey:        strings.TrimSpace(os.Getenv("AGENT_MEMORY_QUERY_PLANNER_API_KEY")),
		QueryPlannerTimeout:       8 * time.Second,
		QueryPlannerWarmupTimeout: 30 * time.Second,
		QueryPlannerKeepAlive:     30 * time.Minute,
		QueryPlannerCacheTTL:      10 * time.Minute,
		QueryPlannerCacheCapacity: 256,
		RerankerEnabled:           strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_MEMORY_RERANKER_ENABLED")), "true"),
		RerankerEndpoint:          strings.TrimRight(strings.TrimSpace(os.Getenv("AGENT_MEMORY_RERANKER_ENDPOINT")), "/"),
		RerankerModel:             strings.TrimSpace(os.Getenv("AGENT_MEMORY_RERANKER_MODEL")),
		RerankerAPIKey:            strings.TrimSpace(os.Getenv("AGENT_MEMORY_RERANKER_API_KEY")),
		RerankerTimeout:           8 * time.Second,
		RerankerMinRelevance:      0.5,
		QueueURL:                  strings.TrimSpace(os.Getenv("AGENT_MEMORY_QUEUE_URL")),
		SecretRef:                 strings.TrimSpace(os.Getenv("AGENT_MEMORY_SECRET_REF")),
		DevAuthToken:              strings.TrimSpace(os.Getenv("AGENT_MEMORY_DEV_AUTH_TOKEN")),
		DevSubject:                strings.TrimSpace(os.Getenv("AGENT_MEMORY_DEV_SUBJECT")),
		DevEmail:                  strings.TrimSpace(os.Getenv("AGENT_MEMORY_DEV_EMAIL")),
		DevDisplayName:            strings.TrimSpace(os.Getenv("AGENT_MEMORY_DEV_DISPLAY_NAME")),
		LocalOnboardingEnabled:    strings.EqualFold(strings.TrimSpace(os.Getenv("AGENT_MEMORY_LOCAL_ONBOARDING")), "true"),
		IdentityMode:              identityMode,
		OIDCIssuer:                strings.TrimRight(strings.TrimSpace(os.Getenv("AGENT_MEMORY_OIDC_ISSUER")), "/"),
		OIDCAudience:              strings.TrimSpace(os.Getenv("AGENT_MEMORY_OIDC_AUDIENCE")),
		EdgeCountrySecret:         strings.TrimSpace(os.Getenv("AGENT_MEMORY_EDGE_COUNTRY_SECRET")),
		ShutdownTimeout:           10 * time.Second,
	}
	warmupEnabled := strings.TrimSpace(os.Getenv("AGENT_MEMORY_QUERY_PLANNER_WARMUP_ENABLED"))
	if warmupEnabled != "" {
		value, err := strconv.ParseBool(warmupEnabled)
		if err != nil {
			return Config{}, fmt.Errorf("AGENT_MEMORY_QUERY_PLANNER_WARMUP_ENABLED must be true or false")
		}
		cfg.QueryPlannerWarmupEnabled = cfg.QueryPlannerEnabled && value
	}
	if raw := strings.TrimSpace(os.Getenv("AGENT_MEMORY_MODEL_DIMENSION")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 65536 {
			return Config{}, fmt.Errorf("AGENT_MEMORY_MODEL_DIMENSION must be between 1 and 65536")
		}
		cfg.ModelDimension = value
	}
	for name, destination := range map[string]*time.Duration{
		"AGENT_MEMORY_QUERY_PLANNER_TIMEOUT": &cfg.QueryPlannerTimeout,
		"AGENT_MEMORY_RERANKER_TIMEOUT":      &cfg.RerankerTimeout,
	} {
		if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil || value < time.Second || value > 30*time.Second {
				return Config{}, fmt.Errorf("%s must be between 1s and 30s", name)
			}
			*destination = value
		}
	}
	for name, bounds := range map[string]struct {
		destination *time.Duration
		minimum     time.Duration
		maximum     time.Duration
	}{
		"AGENT_MEMORY_QUERY_PLANNER_WARMUP_TIMEOUT": {&cfg.QueryPlannerWarmupTimeout, time.Second, time.Minute},
		"AGENT_MEMORY_QUERY_PLANNER_KEEP_ALIVE":     {&cfg.QueryPlannerKeepAlive, time.Minute, 24 * time.Hour},
		"AGENT_MEMORY_QUERY_PLANNER_CACHE_TTL":      {&cfg.QueryPlannerCacheTTL, time.Second, 24 * time.Hour},
	} {
		if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil || value < bounds.minimum || value > bounds.maximum {
				return Config{}, fmt.Errorf("%s must be between %s and %s", name, bounds.minimum, bounds.maximum)
			}
			*bounds.destination = value
		}
	}
	if raw := strings.TrimSpace(os.Getenv("AGENT_MEMORY_QUERY_PLANNER_CACHE_CAPACITY")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 4096 {
			return Config{}, fmt.Errorf("AGENT_MEMORY_QUERY_PLANNER_CACHE_CAPACITY must be between 0 and 4096")
		}
		cfg.QueryPlannerCacheCapacity = value
	}
	if raw := strings.TrimSpace(os.Getenv("AGENT_MEMORY_RERANKER_MIN_RELEVANCE")); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value <= 0 || value >= 1 {
			return Config{}, fmt.Errorf("AGENT_MEMORY_RERANKER_MIN_RELEVANCE must be between zero and one")
		}
		cfg.RerankerMinRelevance = value
	}
	if raw := strings.TrimSpace(os.Getenv("AGENT_MEMORY_SAAS_SHUTDOWN_TIMEOUT")); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 || value > time.Minute {
			return Config{}, fmt.Errorf("AGENT_MEMORY_SAAS_SHUTDOWN_TIMEOUT must be between 1ns and 1m")
		}
		cfg.ShutdownTimeout = value
	}
	if raw := strings.TrimSpace(os.Getenv("AGENT_MEMORY_TRACING_SAMPLE_RATE")); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || value < 0 || value > 1 {
			return Config{}, fmt.Errorf("AGENT_MEMORY_TRACING_SAMPLE_RATE must be between 0 and 1")
		}
		cfg.TracingSampleRate = value
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadFor loads configuration and binds it to the identity compiled into a
// deployable service. This prevents an environment variable from relabeling a
// workload and therefore changing its policy or telemetry identity.
func LoadFor(service Service) (Config, error) {
	cfg, err := Load()
	if err != nil {
		return Config{}, err
	}
	if cfg.Service != service {
		return Config{}, fmt.Errorf("AGENT_MEMORY_SAAS_SERVICE=%s does not match binary service %s", cfg.Service, service)
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if !oneOf(string(c.Environment), string(Development), string(Staging), string(Production)) {
		return fmt.Errorf("AGENT_MEMORY_SAAS_ENV must be development, staging, or production")
	}
	if !oneOf(string(c.Service), string(API), string(Worker), string(Reconciler), string(Migration)) {
		return fmt.Errorf("AGENT_MEMORY_SAAS_SERVICE must be api, worker, reconciler, or migration")
	}
	if !oneOf(string(c.IdentityMode), string(IdentityDevelopment), string(IdentityOIDC)) {
		return fmt.Errorf("AGENT_MEMORY_IDENTITY_MODE must be development or oidc")
	}
	if c.LocalOnboardingEnabled && (c.Environment != Development || c.IdentityMode != IdentityDevelopment || c.Service != API) {
		return fmt.Errorf("AGENT_MEMORY_LOCAL_ONBOARDING requires the development API with development identity")
	}
	if c.PostgresURL == "" {
		return fmt.Errorf("AGENT_MEMORY_POSTGRES_URL is required")
	}
	if c.Environment != Development {
		if c.DevAuthToken != "" || c.DevSubject != "" || c.DevEmail != "" || c.DevDisplayName != "" {
			return fmt.Errorf("staging or production configuration cannot enable development identity")
		}
	}
	if c.Environment == Production {
		if strings.Contains(c.PostgresURL, "sslmode=disable") {
			return fmt.Errorf("production configuration requires TLS PostgreSQL")
		}
		if c.Service != Migration && (!isHTTPS(c.ObjectEndpoint) || c.SecretRef == "" || strings.Contains(c.SecretRef, "local")) {
			return fmt.Errorf("production configuration requires TLS endpoints and a managed secret reference")
		}
	}
	if c.Service == API || c.Service == Worker || c.Service == Reconciler {
		for name, value := range map[string]string{
			"AGENT_MEMORY_OBJECT_ENDPOINT":   c.ObjectEndpoint,
			"AGENT_MEMORY_OBJECT_ACCESS_KEY": c.ObjectAccessKey,
			"AGENT_MEMORY_OBJECT_SECRET_KEY": c.ObjectSecretKey,
			"AGENT_MEMORY_SECRET_REF":        c.SecretRef,
		} {
			if value == "" {
				return fmt.Errorf("%s is required", name)
			}
		}
	}
	if c.Service == Worker && c.QueueURL == "" {
		return fmt.Errorf("AGENT_MEMORY_QUEUE_URL is required for the worker")
	}
	if c.Service == API {
		switch c.IdentityMode {
		case IdentityDevelopment:
			if c.Environment != Development {
				return fmt.Errorf("staging and production API must use OIDC identity")
			}
			for name, value := range map[string]string{
				"AGENT_MEMORY_DEV_AUTH_TOKEN": c.DevAuthToken,
				"AGENT_MEMORY_DEV_SUBJECT":    c.DevSubject,
				"AGENT_MEMORY_DEV_EMAIL":      c.DevEmail,
			} {
				if value == "" {
					return fmt.Errorf("%s is required for the development API", name)
				}
			}
		case IdentityOIDC:
			if c.DevAuthToken != "" || c.DevSubject != "" || c.DevEmail != "" || c.DevDisplayName != "" {
				return fmt.Errorf("OIDC identity cannot include development credentials")
			}
			if c.OIDCIssuer == "" || c.OIDCAudience == "" {
				return fmt.Errorf("AGENT_MEMORY_OIDC_ISSUER and AGENT_MEMORY_OIDC_AUDIENCE are required for OIDC API identity")
			}
			if c.Environment != Development && !isHTTPS(c.OIDCIssuer) {
				return fmt.Errorf("AGENT_MEMORY_OIDC_ISSUER must be an HTTPS issuer for staging and production API")
			}
		}
	}
	if c.Service == API && len(c.EdgeCountrySecret) < 32 {
		return fmt.Errorf("AGENT_MEMORY_EDGE_COUNTRY_SECRET must contain at least 32 characters for the API")
	}
	if !strings.HasPrefix(c.TelemetryAddr, ":") || len(c.TelemetryAddr) < 2 {
		return fmt.Errorf("AGENT_MEMORY_TELEMETRY_LISTEN_ADDR must be a port listener such as :9090")
	}
	if c.Service == Worker || c.Service == API {
		for name, value := range map[string]string{"AGENT_MEMORY_EXPORT_ENCRYPTION_KEY": c.ExportEncryptionKey} {
			if value == "" {
				return fmt.Errorf("%s is required", name)
			}
		}
	}
	if c.Service == Worker && c.VaultEncryptionKey == "" {
		return fmt.Errorf("AGENT_MEMORY_VAULT_ENCRYPTION_KEY is required for the worker")
	}
	if c.Service == Worker || c.Service == API {
		if c.ModelProvider == "" || c.ModelRetention == "" {
			return fmt.Errorf("model provider and retention policy are required")
		}
		switch c.ModelProvider {
		case "local-minilm-scaffold":
			if c.ModelDirectory == "" {
				return fmt.Errorf("local model directory is required")
			}
			if c.Environment == Production {
				return fmt.Errorf("production model-serving service cannot use the local embedding scaffold")
			}
		case "openai-compatible":
			if c.ModelEndpoint == "" || c.ModelAPIKey == "" || c.ModelVersion == "" || c.ModelDimension <= 0 {
				return fmt.Errorf("managed model endpoint, API key, version, and dimension are required")
			}
			if c.Environment != Development && !isHTTPS(c.ModelEndpoint) {
				return fmt.Errorf("staging and production model endpoint must use HTTPS")
			}
		default:
			return fmt.Errorf("unsupported hosted model provider %q", c.ModelProvider)
		}
	}
	if c.QueryPlannerEnabled {
		if c.QueryPlannerEndpoint == "" || c.QueryPlannerModel == "" {
			return fmt.Errorf("AGENT_MEMORY_QUERY_PLANNER_ENDPOINT and AGENT_MEMORY_QUERY_PLANNER_MODEL are required when the planner is enabled")
		}
		if !approvedLocalSemanticEndpoint(c.QueryPlannerEndpoint, c.Environment) {
			return fmt.Errorf("AGENT_MEMORY_QUERY_PLANNER_ENDPOINT is outside the installation boundary")
		}
	}
	if c.RerankerEnabled {
		if c.RerankerEndpoint == "" || c.RerankerModel == "" {
			return fmt.Errorf("AGENT_MEMORY_RERANKER_ENDPOINT and AGENT_MEMORY_RERANKER_MODEL are required when the reranker is enabled")
		}
		if c.RerankerMinRelevance <= 0 || c.RerankerMinRelevance >= 1 {
			return fmt.Errorf("AGENT_MEMORY_RERANKER_MIN_RELEVANCE must be between zero and one")
		}
		if !approvedLocalSemanticEndpoint(c.RerankerEndpoint, c.Environment) {
			return fmt.Errorf("AGENT_MEMORY_RERANKER_ENDPOINT is outside the installation boundary")
		}
	}
	return nil
}

func (c Config) Summary() string {
	return fmt.Sprintf("environment=%s service=%s identity=%s listen=%s postgres=%s object=%s queue=%s secret_ref=[redacted]",
		c.Environment, c.Service, c.IdentityMode, c.ListenAddr, endpointHost(c.PostgresURL), endpointHost(c.ObjectEndpoint), endpointHost(c.QueueURL))
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func isHTTPS(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}

func endpointHost(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "[configured]"
	}
	return parsed.Scheme + "://" + parsed.Hostname()
}

func approvedLocalSemanticEndpoint(raw string, environment Environment) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "ollama" || host == "reranker" {
		return true
	}
	return environment == Development && host == "host.docker.internal"
}
