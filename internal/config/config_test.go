package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Enabled {
		t.Error("expected Enabled to be true by default")
	}
	if cfg.DataDir == "" {
		t.Error("expected DataDir to be set")
	}
	if cfg.Storage.DefaultTier != "vector" {
		t.Errorf("expected default tier 'vector', got %q", cfg.Storage.DefaultTier)
	}
	if cfg.Embeddings.Provider != "local" {
		t.Errorf("expected default provider 'local', got %q", cfg.Embeddings.Provider)
	}
	if cfg.Retrieval.DefaultTopK != 8 {
		t.Errorf("expected default top-k 8, got %d", cfg.Retrieval.DefaultTopK)
	}
	if cfg.Dashboard.Port != 3042 {
		t.Errorf("expected dashboard port 3042, got %d", cfg.Dashboard.Port)
	}
}

func TestLoadFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Write test config
	configYAML := `
enabled: false
data_dir: /tmp/test-data
run_label: test-run

storage:
  default_tier: markdown

embeddings:
  provider: openai
  openai_key: test-key-123

retrieval:
  default_top_k: 10
  semantic_weight: 0.60

dashboard:
  port: 4000
  auto_launch: true

observe:
  log_level: debug
`

	if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg := DefaultConfig()
	if err := cfg.loadFromFile(configPath); err != nil {
		t.Fatalf("loading config: %v", err)
	}

	// Verify loaded values
	if cfg.Enabled {
		t.Error("expected Enabled to be false")
	}
	if cfg.DataDir != "/tmp/test-data" {
		t.Errorf("expected DataDir '/tmp/test-data', got %q", cfg.DataDir)
	}
	if cfg.RunLabel != "test-run" {
		t.Errorf("expected RunLabel 'test-run', got %q", cfg.RunLabel)
	}
	if cfg.Storage.DefaultTier != "markdown" {
		t.Errorf("expected tier 'markdown', got %q", cfg.Storage.DefaultTier)
	}
	if cfg.Embeddings.Provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", cfg.Embeddings.Provider)
	}
	if cfg.Embeddings.OpenAIKey != "test-key-123" {
		t.Errorf("expected key 'test-key-123', got %q", cfg.Embeddings.OpenAIKey)
	}
	if cfg.Retrieval.DefaultTopK != 10 {
		t.Errorf("expected top-k 10, got %d", cfg.Retrieval.DefaultTopK)
	}
	if cfg.Retrieval.SemanticWeight != 0.60 {
		t.Errorf("expected semantic weight 0.60, got %f", cfg.Retrieval.SemanticWeight)
	}
	if cfg.Dashboard.Port != 4000 {
		t.Errorf("expected port 4000, got %d", cfg.Dashboard.Port)
	}
	if !cfg.Dashboard.AutoLaunch {
		t.Error("expected AutoLaunch to be true")
	}
	if cfg.Observe.LogLevel != "debug" {
		t.Errorf("expected log level 'debug', got %q", cfg.Observe.LogLevel)
	}
}

func TestConfigPrecedence(t *testing.T) {
	tmpDir := t.TempDir()

	// Create user config
	userConfigPath := filepath.Join(tmpDir, "user-config.yaml")
	userYAML := `
run_label: user-label
retrieval:
  default_top_k: 5
dashboard:
  port: 3000
`
	if err := os.WriteFile(userConfigPath, []byte(userYAML), 0644); err != nil {
		t.Fatalf("writing user config: %v", err)
	}

	// Create workspace config (should override user config)
	workspaceConfigPath := filepath.Join(tmpDir, "workspace-config.yaml")
	workspaceYAML := `
run_label: workspace-label
retrieval:
  default_top_k: 15
`
	if err := os.WriteFile(workspaceConfigPath, []byte(workspaceYAML), 0644); err != nil {
		t.Fatalf("writing workspace config: %v", err)
	}

	cfg := DefaultConfig()

	// Load user config first
	if err := cfg.loadFromFile(userConfigPath); err != nil {
		t.Fatalf("loading user config: %v", err)
	}

	// Verify user config applied
	if cfg.RunLabel != "user-label" {
		t.Errorf("expected RunLabel 'user-label', got %q", cfg.RunLabel)
	}
	if cfg.Retrieval.DefaultTopK != 5 {
		t.Errorf("expected top-k 5, got %d", cfg.Retrieval.DefaultTopK)
	}
	if cfg.Dashboard.Port != 3000 {
		t.Errorf("expected port 3000, got %d", cfg.Dashboard.Port)
	}

	// Load workspace config (should override)
	if err := cfg.loadFromFile(workspaceConfigPath); err != nil {
		t.Fatalf("loading workspace config: %v", err)
	}

	// Verify workspace config overrode user config
	if cfg.RunLabel != "workspace-label" {
		t.Errorf("expected RunLabel 'workspace-label', got %q", cfg.RunLabel)
	}
	if cfg.Retrieval.DefaultTopK != 15 {
		t.Errorf("expected top-k 15, got %d", cfg.Retrieval.DefaultTopK)
	}
	// Dashboard port should remain from user config (not in workspace config)
	if cfg.Dashboard.Port != 3000 {
		t.Errorf("expected port 3000 (from user config), got %d", cfg.Dashboard.Port)
	}
}

func TestEnvOverrides(t *testing.T) {
	// Set environment variables
	t.Setenv("AGENT_MEMORY_ENABLED", "0")
	t.Setenv("AGENT_MEMORY_RUN_LABEL", "env-label")
	t.Setenv("AGENT_MEMORY_DATA_DIR", "/tmp/env-data")
	t.Setenv("AGENT_MEMORY_DASHBOARD_PORT", "5000")
	t.Setenv("AGENT_MEMORY_LOG_LEVEL", "error")
	t.Setenv("AGENT_MEMORY_ONNX_RUNTIME_PATH", "/custom/runtime.so")

	cfg := DefaultConfig()
	cfg.applyEnvOverrides()

	if cfg.Enabled {
		t.Error("expected Enabled to be false from env")
	}
	if cfg.RunLabel != "env-label" {
		t.Errorf("expected RunLabel 'env-label', got %q", cfg.RunLabel)
	}
	if cfg.DataDir != "/tmp/env-data" {
		t.Errorf("expected DataDir '/tmp/env-data', got %q", cfg.DataDir)
	}
	if cfg.Dashboard.Port != 5000 {
		t.Errorf("expected port 5000, got %d", cfg.Dashboard.Port)
	}
	if cfg.Observe.LogLevel != "error" {
		t.Errorf("expected log level 'error', got %q", cfg.Observe.LogLevel)
	}
	if cfg.Embeddings.RuntimePath != "/custom/runtime.so" {
		t.Errorf("expected runtime path '/custom/runtime.so', got %q", cfg.Embeddings.RuntimePath)
	}
}

func TestValidateSuccess(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected validation to succeed, got: %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name      string
		modify    func(*Config)
		wantError string
	}{
		{
			name: "empty data dir",
			modify: func(c *Config) {
				c.DataDir = ""
			},
			wantError: "data_dir cannot be empty",
		},
		{
			name: "invalid storage tier",
			modify: func(c *Config) {
				c.Storage.DefaultTier = "invalid"
			},
			wantError: "invalid storage.default_tier",
		},
		{
			name: "invalid embedding provider",
			modify: func(c *Config) {
				c.Embeddings.Provider = "invalid"
			},
			wantError: "invalid embeddings.provider",
		},
		{
			name: "missing openai key",
			modify: func(c *Config) {
				c.Embeddings.Provider = "openai"
				c.Embeddings.OpenAIKey = ""
			},
			wantError: "embeddings.openai_key is required",
		},
		{
			name: "invalid dimensions",
			modify: func(c *Config) {
				c.Embeddings.Dimensions = 0
			},
			wantError: "embeddings.dimensions must be positive",
		},
		{
			name: "invalid weight sum",
			modify: func(c *Config) {
				c.Retrieval.SemanticWeight = 0.50
				c.Retrieval.RecencyWeight = 0.20
				c.Retrieval.OutcomeWeight = 0.10
				c.Retrieval.DecayWeight = 0.05
				c.Retrieval.TierBiasWeight = 0.05 // Sum = 0.90, not 1.0
			},
			wantError: "retrieval weights must sum to 1.0",
		},
		{
			name: "invalid dashboard port",
			modify: func(c *Config) {
				c.Dashboard.Port = 99999
			},
			wantError: "invalid dashboard.port",
		},
		{
			name: "invalid server port",
			modify: func(c *Config) {
				c.Server.Port = 0
			},
			wantError: "invalid server.port",
		},
		{
			name: "invalid log level",
			modify: func(c *Config) {
				c.Observe.LogLevel = "invalid"
			},
			wantError: "invalid observe.log_level",
		},
		{
			name: "invalid cooldown duration",
			modify: func(c *Config) {
				c.Adaptive.FeedbackCooldowns.Rejected = "invalid"
			},
			wantError: "invalid adaptive.feedback_cooldowns.rejected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.modify(cfg)

			err := cfg.Validate()
			if err == nil {
				t.Error("expected validation to fail")
				return
			}
			if tt.wantError != "" && !containsString(err.Error(), tt.wantError) {
				t.Errorf("expected error containing %q, got %q", tt.wantError, err.Error())
			}
		})
	}
}

func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "test-config.yaml")

	// Create and modify config
	cfg := DefaultConfig()
	cfg.RunLabel = "test-save"
	cfg.Retrieval.DefaultTopK = 12
	cfg.Dashboard.Port = 4500
	cfg.Observe.LogLevel = "debug"

	// Save config
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("saving config: %v", err)
	}

	// Load config
	loaded := DefaultConfig()
	if err := loaded.loadFromFile(configPath); err != nil {
		t.Fatalf("loading config: %v", err)
	}

	// Verify values
	if loaded.RunLabel != "test-save" {
		t.Errorf("expected RunLabel 'test-save', got %q", loaded.RunLabel)
	}
	if loaded.Retrieval.DefaultTopK != 12 {
		t.Errorf("expected top-k 12, got %d", loaded.Retrieval.DefaultTopK)
	}
	if loaded.Dashboard.Port != 4500 {
		t.Errorf("expected port 4500, got %d", loaded.Dashboard.Port)
	}
	if loaded.Observe.LogLevel != "debug" {
		t.Errorf("expected log level 'debug', got %q", loaded.Observe.LogLevel)
	}
}

func TestEnvBool(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"1", true},
		{"true", true},
		{"True", true},
		{"TRUE", true},
		{"yes", true},
		{"Yes", true},
		{"YES", true},
		{"on", true},
		{"On", true},
		{"ON", true},
		{"0", false},
		{"false", false},
		{"no", false},
		{"off", false},
		{"", false},
		{"invalid", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := envBool(tt.input)
			if got != tt.want {
				t.Errorf("envBool(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestEnvInt(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"0", 0},
		{"1", 1},
		{"42", 42},
		{"1000", 1000},
		{"-5", -5},
		{"invalid", 0},
		{"", 0},
		{"3.14", 3}, // Should parse integer part
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := envInt(tt.input)
			if got != tt.want {
				t.Errorf("envInt(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestMergePreservesDefaults(t *testing.T) {
	cfg := DefaultConfig()
	originalPort := cfg.Dashboard.Port
	originalTopK := cfg.Retrieval.DefaultTopK

	// Merge empty config with no presence information (simulates a layer
	// that did not set any keys at all).
	other := &Config{}
	cfg.merge(other, nil)

	// Verify defaults preserved
	if cfg.Dashboard.Port != originalPort {
		t.Errorf("expected port %d preserved, got %d", originalPort, cfg.Dashboard.Port)
	}
	if cfg.Retrieval.DefaultTopK != originalTopK {
		t.Errorf("expected top-k %d preserved, got %d", originalTopK, cfg.Retrieval.DefaultTopK)
	}
	// Zero-ambiguous boolean defaults must also be preserved when nothing
	// was explicitly present in the merged-in layer.
	if !cfg.Enabled {
		t.Error("expected Enabled to remain true after merging an empty layer")
	}
	if !cfg.Storage.AutoVacuum {
		t.Error("expected Storage.AutoVacuum to remain true after merging an empty layer")
	}
	if !cfg.Embeddings.CacheEnabled {
		t.Error("expected Embeddings.CacheEnabled to remain true after merging an empty layer")
	}
	if !cfg.Dashboard.Enabled {
		t.Error("expected Dashboard.Enabled to remain true after merging an empty layer")
	}
	if !cfg.Server.EnableCORS {
		t.Error("expected Server.EnableCORS to remain true after merging an empty layer")
	}
	if !cfg.Observe.Enabled {
		t.Error("expected Observe.Enabled to remain true after merging an empty layer")
	}
	if !cfg.Adaptive.Enabled {
		t.Error("expected Adaptive.Enabled to remain true after merging an empty layer")
	}
}

func TestLoadFromFile_PreservesUnspecifiedBooleanDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// A realistic minimal config file: it only sets one unrelated field.
	// None of the true-by-default booleans are mentioned, and loading this
	// file must not flip any of them to false.
	configYAML := `
dashboard:
  port: 9999
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg := DefaultConfig()
	if err := cfg.loadFromFile(configPath); err != nil {
		t.Fatalf("loading config: %v", err)
	}

	if cfg.Dashboard.Port != 9999 {
		t.Errorf("expected dashboard port 9999, got %d", cfg.Dashboard.Port)
	}
	if !cfg.Enabled {
		t.Error("expected Enabled to remain true when not specified in config file")
	}
	if !cfg.Storage.AutoVacuum {
		t.Error("expected Storage.AutoVacuum to remain true when not specified in config file")
	}
	if !cfg.Embeddings.CacheEnabled {
		t.Error("expected Embeddings.CacheEnabled to remain true when not specified in config file")
	}
	if !cfg.Dashboard.Enabled {
		t.Error("expected Dashboard.Enabled to remain true when not specified in config file")
	}
	if !cfg.Server.EnableCORS {
		t.Error("expected Server.EnableCORS to remain true when not specified in config file")
	}
	if !cfg.Observe.Enabled {
		t.Error("expected Observe.Enabled to remain true when not specified in config file")
	}
	if !cfg.Adaptive.Enabled {
		t.Error("expected Adaptive.Enabled to remain true when not specified in config file")
	}
}

func TestLoadFromFile_CanExplicitlyDisableTrueDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Explicitly setting these booleans to false must still take effect.
	configYAML := `
enabled: false
storage:
  auto_vacuum: false
embeddings:
  cache_enabled: false
dashboard:
  enabled: false
server:
  enable_cors: false
observe:
  enabled: false
adaptive:
  enabled: false
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg := DefaultConfig()
	if err := cfg.loadFromFile(configPath); err != nil {
		t.Fatalf("loading config: %v", err)
	}

	if cfg.Enabled {
		t.Error("expected Enabled to be false")
	}
	if cfg.Storage.AutoVacuum {
		t.Error("expected Storage.AutoVacuum to be false")
	}
	if cfg.Embeddings.CacheEnabled {
		t.Error("expected Embeddings.CacheEnabled to be false")
	}
	if cfg.Dashboard.Enabled {
		t.Error("expected Dashboard.Enabled to be false")
	}
	if cfg.Server.EnableCORS {
		t.Error("expected Server.EnableCORS to be false")
	}
	if cfg.Observe.Enabled {
		t.Error("expected Observe.Enabled to be false")
	}
	if cfg.Adaptive.Enabled {
		t.Error("expected Adaptive.Enabled to be false")
	}
}

func TestLoadFromFile_ReenableAfterUserConfigDisabled(t *testing.T) {
	tmpDir := t.TempDir()

	// User-level config disables the system entirely.
	userConfigPath := filepath.Join(tmpDir, "user-config.yaml")
	userYAML := `
enabled: false
`
	if err := os.WriteFile(userConfigPath, []byte(userYAML), 0644); err != nil {
		t.Fatalf("writing user config: %v", err)
	}

	// Workspace-level config re-enables it for this project.
	workspaceConfigPath := filepath.Join(tmpDir, "workspace-config.yaml")
	workspaceYAML := `
enabled: true
`
	if err := os.WriteFile(workspaceConfigPath, []byte(workspaceYAML), 0644); err != nil {
		t.Fatalf("writing workspace config: %v", err)
	}

	cfg := DefaultConfig()
	if err := cfg.loadFromFile(userConfigPath); err != nil {
		t.Fatalf("loading user config: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("expected Enabled to be false after user config")
	}

	if err := cfg.loadFromFile(workspaceConfigPath); err != nil {
		t.Fatalf("loading workspace config: %v", err)
	}
	if !cfg.Enabled {
		t.Error("expected Enabled to be true after workspace config explicitly re-enables it")
	}
}

func TestAdaptivePolicyMerge(t *testing.T) {
	cfg := DefaultConfig()

	// Set custom policies
	other := &Config{
		Adaptive: AdaptiveConfig{
			PolicyDefaults: map[string]PolicyDefaults{
				"search": {
					MinSemanticScore: 0.40,
					MinTotalScore:    0.05,
				},
				"custom": {
					MinSemanticScore: 0.50,
				},
			},
		},
	}

	cfg.merge(other, nil)

	// Verify search policy was updated
	if cfg.Adaptive.PolicyDefaults["search"].MinSemanticScore != 0.40 {
		t.Errorf("expected search min_semantic_score 0.40, got %f",
			cfg.Adaptive.PolicyDefaults["search"].MinSemanticScore)
	}

	// Verify custom policy was added
	if cfg.Adaptive.PolicyDefaults["custom"].MinSemanticScore != 0.50 {
		t.Errorf("expected custom min_semantic_score 0.50, got %f",
			cfg.Adaptive.PolicyDefaults["custom"].MinSemanticScore)
	}

	// Verify other policies preserved
	if _, exists := cfg.Adaptive.PolicyDefaults["recall"]; !exists {
		t.Error("expected recall policy to be preserved")
	}
}

func containsString(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 &&
		(s == substr || len(s) >= len(substr) &&
			(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
				containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
