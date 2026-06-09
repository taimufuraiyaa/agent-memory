package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the unified configuration for agent-memory.
// Configuration precedence (lowest to highest):
// 1. Defaults
// 2. User config file (~/.agent-memory/config.yaml)
// 3. Workspace config file (.agent-memory.yaml in project root)
// 4. Environment variables
// 5. CLI flags
type Config struct {
	// Core settings
	Enabled   bool   `yaml:"enabled"`
	DataDir   string `yaml:"data_dir"`
	RunLabel  string `yaml:"run_label"`
	Workspace string `yaml:"workspace"`

	// Storage settings
	Storage StorageConfig `yaml:"storage"`

	// Embedding settings
	Embeddings EmbeddingConfig `yaml:"embeddings"`

	// Retrieval settings
	Retrieval RetrievalConfig `yaml:"retrieval"`

	// Dashboard settings
	Dashboard DashboardConfig `yaml:"dashboard"`

	// API server settings
	Server ServerConfig `yaml:"server"`

	// Observability settings
	Observe ObserveConfig `yaml:"observe"`

	// Upgrade settings
	Upgrade UpgradeConfig `yaml:"upgrade"`

	// Adaptive tuning settings
	Adaptive AdaptiveConfig `yaml:"adaptive"`
}

// StorageConfig contains storage-related configuration.
type StorageConfig struct {
	DBPath           string `yaml:"db_path"`
	DefaultTier      string `yaml:"default_tier"`
	AutoVacuum       bool   `yaml:"auto_vacuum"`
	VacuumIntervalMs int    `yaml:"vacuum_interval_ms"`
}

// EmbeddingConfig contains embedding-related configuration.
type EmbeddingConfig struct {
	Provider       string `yaml:"provider"`        // "local" or "openai"
	ModelPath      string `yaml:"model_path"`      // Path to local ONNX model
	RuntimePath    string `yaml:"runtime_path"`    // Path to ONNX Runtime library
	OpenAIKey      string `yaml:"openai_key"`      // OpenAI API key (if using OpenAI)
	ModelName      string `yaml:"model_name"`      // Model name for cloud providers
	Dimensions     int    `yaml:"dimensions"`      // Embedding dimensions
	MaxTokens      int    `yaml:"max_tokens"`      // Max tokens per embedding
	CacheEnabled   bool   `yaml:"cache_enabled"`   // Enable embedding cache
	BatchSize      int    `yaml:"batch_size"`      // Batch size for embedding
	TimeoutSeconds int    `yaml:"timeout_seconds"` // Timeout for embedding operations
}

// RetrievalConfig contains retrieval-related configuration.
type RetrievalConfig struct {
	DefaultMode       string  `yaml:"default_mode"`        // "search", "recall", "relate", "outcomes"
	DefaultTopK       int     `yaml:"default_top_k"`       // Number of results to return
	DefaultBudget     int     `yaml:"default_budget"`      // Token budget for context
	SemanticWeight    float64 `yaml:"semantic_weight"`     // Weight for semantic similarity
	RecencyWeight     float64 `yaml:"recency_weight"`      // Weight for recency
	OutcomeWeight     float64 `yaml:"outcome_weight"`      // Weight for outcome success
	DecayWeight       float64 `yaml:"decay_weight"`        // Weight for time decay
	TierBiasWeight    float64 `yaml:"tier_bias_weight"`    // Weight for storage tier bias
	EnableReranking   bool    `yaml:"enable_reranking"`    // Enable LLM reranking
	RetrievalTimeout  int     `yaml:"retrieval_timeout"`   // Timeout in seconds
	EnableExplanation bool    `yaml:"enable_explanation"`  // Include retrieval explanations
}

// DashboardConfig contains dashboard-related configuration.
type DashboardConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Dir        string `yaml:"dir"`
	Port       int    `yaml:"port"`
	AutoLaunch bool   `yaml:"auto_launch"`
}

// ServerConfig contains API server configuration.
type ServerConfig struct {
	Host           string `yaml:"host"`
	Port           int    `yaml:"port"`
	EnableCORS     bool   `yaml:"enable_cors"`
	AllowedOrigins string `yaml:"allowed_origins"`
	ReadTimeout    int    `yaml:"read_timeout"`
	WriteTimeout   int    `yaml:"write_timeout"`
}

// ObserveConfig contains observability configuration.
type ObserveConfig struct {
	Enabled       bool   `yaml:"enabled"`
	MetricsPort   int    `yaml:"metrics_port"`
	TracingBackend string `yaml:"tracing_backend"` // "jaeger", "zipkin", etc.
	LogLevel      string `yaml:"log_level"`        // "debug", "info", "warn", "error"
	LogFormat     string `yaml:"log_format"`       // "json", "text"
}

// UpgradeConfig contains upgrade-related configuration.
type UpgradeConfig struct {
	AutoUpgrade   bool   `yaml:"auto_upgrade"`
	CheckInterval string `yaml:"check_interval"` // e.g., "24h"
	SourceDir     string `yaml:"source_dir"`     // Source directory for upgrades
}

// AdaptiveConfig contains adaptive tuning configuration.
type AdaptiveConfig struct {
	Enabled           bool                       `yaml:"enabled"`
	PolicyDefaults    map[string]PolicyDefaults  `yaml:"policy_defaults"`
	FeedbackCooldowns FeedbackCooldowns          `yaml:"feedback_cooldowns"`
}

// PolicyDefaults represents adaptive policy thresholds.
type PolicyDefaults struct {
	MinSemanticScore    float64 `yaml:"min_semantic_score"`
	MinTotalScore       float64 `yaml:"min_total_score"`
	RelativeScoreCutoff float64 `yaml:"relative_score_cutoff"`
	WeakSemanticScore   float64 `yaml:"weak_semantic_score"`
	WeakTotalScore      float64 `yaml:"weak_total_score"`
	WeakRelativeCutoff  float64 `yaml:"weak_relative_cutoff"`
}

// FeedbackCooldowns represents cooldown periods for feedback signals.
type FeedbackCooldowns struct {
	Rejected     string `yaml:"rejected_cooldown"`
	Harmful      string `yaml:"harmful_cooldown"`
	Contradicted string `yaml:"contradicted_cooldown"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	dataDir := filepath.Join(homeDir, ".agent-memory")

	return &Config{
		Enabled:   true,
		DataDir:   dataDir,
		RunLabel:  "",
		Workspace: "",
		Storage: StorageConfig{
			DBPath:           filepath.Join(dataDir, "agent-memory.db"),
			DefaultTier:      "vector",
			AutoVacuum:       true,
			VacuumIntervalMs: 3600000, // 1 hour
		},
		Embeddings: EmbeddingConfig{
			Provider:       "local",
			ModelPath:      filepath.Join(dataDir, "models", "all-MiniLM-L6-v2"),
			RuntimePath:    "", // Auto-detected
			OpenAIKey:      "",
			ModelName:      "text-embedding-3-small",
			Dimensions:     384,
			MaxTokens:      512,
			CacheEnabled:   true,
			BatchSize:      32,
			TimeoutSeconds: 30,
		},
		Retrieval: RetrievalConfig{
			DefaultMode:       "search",
			DefaultTopK:       8,
			DefaultBudget:     800,
			SemanticWeight:    0.55,
			RecencyWeight:     0.20,
			OutcomeWeight:     0.10,
			DecayWeight:       0.05,
			TierBiasWeight:    0.10,
			EnableReranking:   false,
			RetrievalTimeout:  10,
			EnableExplanation: false,
		},
		Dashboard: DashboardConfig{
			Enabled:    true,
			Dir:        filepath.Join(dataDir, "dashboard"),
			Port:       3042,
			AutoLaunch: false,
		},
		Server: ServerConfig{
			Host:           "localhost",
			Port:           8042,
			EnableCORS:     true,
			AllowedOrigins: "*",
			ReadTimeout:    30,
			WriteTimeout:   30,
		},
		Observe: ObserveConfig{
			Enabled:       true,
			MetricsPort:   9042,
			TracingBackend: "",
			LogLevel:      "info",
			LogFormat:     "text",
		},
		Upgrade: UpgradeConfig{
			AutoUpgrade:   false,
			CheckInterval: "24h",
			SourceDir:     "",
		},
		Adaptive: AdaptiveConfig{
			Enabled: true,
			PolicyDefaults: map[string]PolicyDefaults{
				"search": {
					MinSemanticScore:    0.30,
					MinTotalScore:       0.02,
					RelativeScoreCutoff: 0.01,
					WeakSemanticScore:   0.00,
					WeakTotalScore:      0.00,
					WeakRelativeCutoff:  0.00,
				},
				"recall": {
					MinSemanticScore:    0.30,
					MinTotalScore:       0.02,
					RelativeScoreCutoff: 0.01,
					WeakSemanticScore:   0.00,
					WeakTotalScore:      0.00,
					WeakRelativeCutoff:  0.00,
				},
				"relate": {
					MinSemanticScore:    0.30,
					MinTotalScore:       0.02,
					RelativeScoreCutoff: 0.01,
					WeakSemanticScore:   0.00,
					WeakTotalScore:      0.00,
					WeakRelativeCutoff:  0.00,
				},
				"outcomes": {
					MinSemanticScore:    0.30,
					MinTotalScore:       0.02,
					RelativeScoreCutoff: 0.01,
					WeakSemanticScore:   0.00,
					WeakTotalScore:      0.00,
					WeakRelativeCutoff:  0.00,
				},
			},
			FeedbackCooldowns: FeedbackCooldowns{
				Rejected:     "6h",
				Harmful:      "24h",
				Contradicted: "30m",
			},
		},
	}
}

// Load loads configuration with proper precedence:
// defaults < user config < workspace config < env vars < explicit overrides
func Load(workspaceDir string) (*Config, error) {
	cfg := DefaultConfig()

	// Load user-level config
	homeDir, err := os.UserHomeDir()
	if err == nil {
		userConfigPath := filepath.Join(homeDir, ".agent-memory", "config.yaml")
		if err := cfg.loadFromFile(userConfigPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("loading user config: %w", err)
		}
	}

	// Load workspace-level config
	if workspaceDir != "" {
		workspaceConfigPath := filepath.Join(workspaceDir, ".agent-memory.yaml")
		if err := cfg.loadFromFile(workspaceConfigPath); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("loading workspace config: %w", err)
		}
		cfg.Workspace = workspaceDir
	}

	// Apply environment variable overrides
	cfg.applyEnvOverrides()

	return cfg, nil
}

// loadFromFile loads configuration from a YAML file and merges with existing config.
func (c *Config) loadFromFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Parse YAML into a temporary config
	var fileConfig Config
	if err := yaml.Unmarshal(data, &fileConfig); err != nil {
		return fmt.Errorf("parsing YAML: %w", err)
	}

	// Merge non-zero values from file into current config
	c.merge(&fileConfig)
	return nil
}

// merge merges non-zero values from other config into this config.
func (c *Config) merge(other *Config) {
	if other.Enabled != c.Enabled && other.Enabled == false {
		c.Enabled = other.Enabled
	}
	if other.DataDir != "" {
		c.DataDir = other.DataDir
	}
	if other.RunLabel != "" {
		c.RunLabel = other.RunLabel
	}
	if other.Workspace != "" {
		c.Workspace = other.Workspace
	}

	// Merge nested structs
	c.mergeStorage(&other.Storage)
	c.mergeEmbeddings(&other.Embeddings)
	c.mergeRetrieval(&other.Retrieval)
	c.mergeDashboard(&other.Dashboard)
	c.mergeServer(&other.Server)
	c.mergeObserve(&other.Observe)
	c.mergeUpgrade(&other.Upgrade)
	c.mergeAdaptive(&other.Adaptive)
}

func (c *Config) mergeStorage(other *StorageConfig) {
	if other.DBPath != "" {
		c.Storage.DBPath = other.DBPath
	}
	if other.DefaultTier != "" {
		c.Storage.DefaultTier = other.DefaultTier
	}
	if other.AutoVacuum != c.Storage.AutoVacuum {
		c.Storage.AutoVacuum = other.AutoVacuum
	}
	if other.VacuumIntervalMs > 0 {
		c.Storage.VacuumIntervalMs = other.VacuumIntervalMs
	}
}

func (c *Config) mergeEmbeddings(other *EmbeddingConfig) {
	if other.Provider != "" {
		c.Embeddings.Provider = other.Provider
	}
	if other.ModelPath != "" {
		c.Embeddings.ModelPath = other.ModelPath
	}
	if other.RuntimePath != "" {
		c.Embeddings.RuntimePath = other.RuntimePath
	}
	if other.OpenAIKey != "" {
		c.Embeddings.OpenAIKey = other.OpenAIKey
	}
	if other.ModelName != "" {
		c.Embeddings.ModelName = other.ModelName
	}
	if other.Dimensions > 0 {
		c.Embeddings.Dimensions = other.Dimensions
	}
	if other.MaxTokens > 0 {
		c.Embeddings.MaxTokens = other.MaxTokens
	}
	if other.CacheEnabled != c.Embeddings.CacheEnabled {
		c.Embeddings.CacheEnabled = other.CacheEnabled
	}
	if other.BatchSize > 0 {
		c.Embeddings.BatchSize = other.BatchSize
	}
	if other.TimeoutSeconds > 0 {
		c.Embeddings.TimeoutSeconds = other.TimeoutSeconds
	}
}

func (c *Config) mergeRetrieval(other *RetrievalConfig) {
	if other.DefaultMode != "" {
		c.Retrieval.DefaultMode = other.DefaultMode
	}
	if other.DefaultTopK > 0 {
		c.Retrieval.DefaultTopK = other.DefaultTopK
	}
	if other.DefaultBudget > 0 {
		c.Retrieval.DefaultBudget = other.DefaultBudget
	}
	if other.SemanticWeight > 0 {
		c.Retrieval.SemanticWeight = other.SemanticWeight
	}
	if other.RecencyWeight > 0 {
		c.Retrieval.RecencyWeight = other.RecencyWeight
	}
	if other.OutcomeWeight > 0 {
		c.Retrieval.OutcomeWeight = other.OutcomeWeight
	}
	if other.DecayWeight > 0 {
		c.Retrieval.DecayWeight = other.DecayWeight
	}
	if other.TierBiasWeight > 0 {
		c.Retrieval.TierBiasWeight = other.TierBiasWeight
	}
	if other.EnableReranking != c.Retrieval.EnableReranking {
		c.Retrieval.EnableReranking = other.EnableReranking
	}
	if other.RetrievalTimeout > 0 {
		c.Retrieval.RetrievalTimeout = other.RetrievalTimeout
	}
	if other.EnableExplanation != c.Retrieval.EnableExplanation {
		c.Retrieval.EnableExplanation = other.EnableExplanation
	}
}

func (c *Config) mergeDashboard(other *DashboardConfig) {
	if other.Enabled != c.Dashboard.Enabled {
		c.Dashboard.Enabled = other.Enabled
	}
	if other.Dir != "" {
		c.Dashboard.Dir = other.Dir
	}
	if other.Port > 0 {
		c.Dashboard.Port = other.Port
	}
	if other.AutoLaunch != c.Dashboard.AutoLaunch {
		c.Dashboard.AutoLaunch = other.AutoLaunch
	}
}

func (c *Config) mergeServer(other *ServerConfig) {
	if other.Host != "" {
		c.Server.Host = other.Host
	}
	if other.Port > 0 {
		c.Server.Port = other.Port
	}
	if other.EnableCORS != c.Server.EnableCORS {
		c.Server.EnableCORS = other.EnableCORS
	}
	if other.AllowedOrigins != "" {
		c.Server.AllowedOrigins = other.AllowedOrigins
	}
	if other.ReadTimeout > 0 {
		c.Server.ReadTimeout = other.ReadTimeout
	}
	if other.WriteTimeout > 0 {
		c.Server.WriteTimeout = other.WriteTimeout
	}
}

func (c *Config) mergeObserve(other *ObserveConfig) {
	if other.Enabled != c.Observe.Enabled {
		c.Observe.Enabled = other.Enabled
	}
	if other.MetricsPort > 0 {
		c.Observe.MetricsPort = other.MetricsPort
	}
	if other.TracingBackend != "" {
		c.Observe.TracingBackend = other.TracingBackend
	}
	if other.LogLevel != "" {
		c.Observe.LogLevel = other.LogLevel
	}
	if other.LogFormat != "" {
		c.Observe.LogFormat = other.LogFormat
	}
}

func (c *Config) mergeUpgrade(other *UpgradeConfig) {
	if other.AutoUpgrade != c.Upgrade.AutoUpgrade {
		c.Upgrade.AutoUpgrade = other.AutoUpgrade
	}
	if other.CheckInterval != "" {
		c.Upgrade.CheckInterval = other.CheckInterval
	}
	if other.SourceDir != "" {
		c.Upgrade.SourceDir = other.SourceDir
	}
}

func (c *Config) mergeAdaptive(other *AdaptiveConfig) {
	if other.Enabled != c.Adaptive.Enabled {
		c.Adaptive.Enabled = other.Enabled
	}
	if len(other.PolicyDefaults) > 0 {
		if c.Adaptive.PolicyDefaults == nil {
			c.Adaptive.PolicyDefaults = make(map[string]PolicyDefaults)
		}
		for k, v := range other.PolicyDefaults {
			c.Adaptive.PolicyDefaults[k] = v
		}
	}
	if other.FeedbackCooldowns.Rejected != "" {
		c.Adaptive.FeedbackCooldowns.Rejected = other.FeedbackCooldowns.Rejected
	}
	if other.FeedbackCooldowns.Harmful != "" {
		c.Adaptive.FeedbackCooldowns.Harmful = other.FeedbackCooldowns.Harmful
	}
	if other.FeedbackCooldowns.Contradicted != "" {
		c.Adaptive.FeedbackCooldowns.Contradicted = other.FeedbackCooldowns.Contradicted
	}
}

// applyEnvOverrides applies environment variable overrides to the config.
func (c *Config) applyEnvOverrides() {
	// Core settings
	if v := os.Getenv("AGENT_MEMORY_ENABLED"); v != "" {
		c.Enabled = envBool(v)
	}
	if v := os.Getenv("AGENT_MEMORY_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("AGENT_MEMORY_RUN_LABEL"); v != "" {
		c.RunLabel = v
	}

	// Storage
	if v := os.Getenv("AGENT_MEMORY_DB_PATH"); v != "" {
		c.Storage.DBPath = v
	}

	// Embeddings
	if v := os.Getenv("AGENT_MEMORY_EMBEDDING_PROVIDER"); v != "" {
		c.Embeddings.Provider = v
	}
	if v := os.Getenv("AGENT_MEMORY_MODEL_PATH"); v != "" {
		c.Embeddings.ModelPath = v
	}
	if v := os.Getenv("AGENT_MEMORY_ONNX_RUNTIME_PATH"); v != "" {
		c.Embeddings.RuntimePath = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		c.Embeddings.OpenAIKey = v
	}

	// Dashboard
	if v := os.Getenv("AGENT_MEMORY_DASHBOARD_DIR"); v != "" {
		c.Dashboard.Dir = v
	}
	if v := os.Getenv("AGENT_MEMORY_DASHBOARD_PORT"); v != "" {
		if port := envInt(v); port > 0 {
			c.Dashboard.Port = port
		}
	}

	// Observability
	if v := os.Getenv("AGENT_MEMORY_OBSERVE_ENABLED"); v != "" {
		c.Observe.Enabled = envBool(v)
	}
	if v := os.Getenv("AGENT_MEMORY_LOG_LEVEL"); v != "" {
		c.Observe.LogLevel = v
	}

	// Upgrade
	if v := os.Getenv("AGENT_MEMORY_UPGRADE_YES"); v != "" {
		c.Upgrade.AutoUpgrade = envBool(v)
	}
	if v := os.Getenv("AGENT_MEMORY_SRC_DIR"); v != "" {
		c.Upgrade.SourceDir = v
	}

	// Adaptive tuning - handled by existing adaptive_tuning.go functions
}

// Validate validates the configuration and returns detailed errors.
func (c *Config) Validate() error {
	var errors []string

	// Validate data directory
	if c.DataDir == "" {
		errors = append(errors, "data_dir cannot be empty")
	}

	// Validate storage
	if c.Storage.DefaultTier != "" {
		validTiers := map[string]bool{"markdown": true, "vector": true, "archive": true}
		if !validTiers[c.Storage.DefaultTier] {
			errors = append(errors, fmt.Sprintf("invalid storage.default_tier: %q (must be markdown, vector, or archive)", c.Storage.DefaultTier))
		}
	}

	// Validate embeddings
	if c.Embeddings.Provider != "local" && c.Embeddings.Provider != "openai" {
		errors = append(errors, fmt.Sprintf("invalid embeddings.provider: %q (must be local or openai)", c.Embeddings.Provider))
	}
	if c.Embeddings.Provider == "openai" && c.Embeddings.OpenAIKey == "" {
		errors = append(errors, "embeddings.openai_key is required when provider is openai")
	}
	if c.Embeddings.Dimensions < 1 {
		errors = append(errors, "embeddings.dimensions must be positive")
	}

	// Validate retrieval weights sum to 1.0
	weightSum := c.Retrieval.SemanticWeight + c.Retrieval.RecencyWeight + c.Retrieval.OutcomeWeight + c.Retrieval.DecayWeight + c.Retrieval.TierBiasWeight
	if weightSum < 0.99 || weightSum > 1.01 {
		errors = append(errors, fmt.Sprintf("retrieval weights must sum to 1.0, got %.2f", weightSum))
	}

	// Validate ports
	if c.Dashboard.Port < 1 || c.Dashboard.Port > 65535 {
		errors = append(errors, fmt.Sprintf("invalid dashboard.port: %d (must be 1-65535)", c.Dashboard.Port))
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errors = append(errors, fmt.Sprintf("invalid server.port: %d (must be 1-65535)", c.Server.Port))
	}

	// Validate log level
	validLogLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLogLevels[strings.ToLower(c.Observe.LogLevel)] {
		errors = append(errors, fmt.Sprintf("invalid observe.log_level: %q (must be debug, info, warn, or error)", c.Observe.LogLevel))
	}

	// Validate cooldown durations
	if _, err := time.ParseDuration(c.Adaptive.FeedbackCooldowns.Rejected); err != nil {
		errors = append(errors, fmt.Sprintf("invalid adaptive.feedback_cooldowns.rejected: %v", err))
	}
	if _, err := time.ParseDuration(c.Adaptive.FeedbackCooldowns.Harmful); err != nil {
		errors = append(errors, fmt.Sprintf("invalid adaptive.feedback_cooldowns.harmful: %v", err))
	}
	if _, err := time.ParseDuration(c.Adaptive.FeedbackCooldowns.Contradicted); err != nil {
		errors = append(errors, fmt.Sprintf("invalid adaptive.feedback_cooldowns.contradicted: %v", err))
	}

	if len(errors) > 0 {
		return fmt.Errorf("configuration validation failed:\n  - %s", strings.Join(errors, "\n  - "))
	}

	return nil
}

// Save saves the configuration to a YAML file.
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

// envBool parses a boolean environment variable.
func envBool(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

// envInt parses an integer environment variable.
func envInt(v string) int {
	var i int
	fmt.Sscanf(v, "%d", &i)
	return i
}
