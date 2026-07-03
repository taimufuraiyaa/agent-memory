package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/taimufuraiyaa/agent-memory/internal/config"
	"gopkg.in/yaml.v3"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage agent-memory configuration",
		Long: `Manage agent-memory configuration.

Configuration is loaded with the following precedence (lowest to highest):
1. Defaults
2. User config file (~/.agent-memory/config.yaml)
3. Workspace config file (.agent-memory.yaml in project root)
4. Environment variables
5. CLI flags

Examples:
  # Show current effective configuration
  agent-memory config show

  # Show configuration as JSON
  agent-memory config show --format json

  # Validate current configuration
  agent-memory config validate

  # Initialize user config file with defaults
  agent-memory config init

  # Initialize workspace config file
  agent-memory config init --workspace
`,
	}

	cmd.AddCommand(newConfigShowCommand())
	cmd.AddCommand(newConfigValidateCommand())
	cmd.AddCommand(newConfigInitCommand())
	cmd.AddCommand(newConfigSetCommand())

	return cmd
}

func newConfigShowCommand() *cobra.Command {
	var format string
	var workspaceDir string

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show effective configuration",
		Long: `Show the effective configuration after merging all sources.

This command displays the final configuration that agent-memory will use,
showing values from defaults, config files, and environment variables.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load configuration
			cfg, err := config.Load(workspaceDir)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// Format output
			switch format {
			case "json":
				encoder := json.NewEncoder(os.Stdout)
				encoder.SetIndent("", "  ")
				return encoder.Encode(cfg)

			case "yaml":
				encoder := yaml.NewEncoder(os.Stdout)
				encoder.SetIndent(2)
				return encoder.Encode(cfg)

			case "text":
				return printConfigText(cfg)

			default:
				return fmt.Errorf("unknown format: %s (use json, yaml, or text)", format)
			}
		},
	}

	cmd.Flags().StringVarP(&format, "format", "f", "yaml", "Output format (json, yaml, text)")
	cmd.Flags().StringVar(&workspaceDir, "workspace", "", "Workspace directory (default: current directory)")

	return cmd
}

func newConfigValidateCommand() *cobra.Command {
	var workspaceDir string

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration",
		Long: `Validate the current configuration and report any errors.

This command loads the configuration from all sources and validates it
against the schema, checking for invalid values, missing required fields,
and logical inconsistencies.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load configuration
			cfg, err := config.Load(workspaceDir)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// Validate configuration
			if err := cfg.Validate(); err != nil {
				fmt.Fprintln(os.Stderr, "❌ Configuration validation failed:")
				fmt.Fprintln(os.Stderr, err.Error())
				return err
			}

			fmt.Fprintln(os.Stderr, "✅ Configuration is valid")

			// Show configuration sources
			homeDir, _ := os.UserHomeDir()
			userConfigPath := filepath.Join(homeDir, ".agent-memory", "config.yaml")
			workspaceConfigPath := ""
			if workspaceDir != "" {
				workspaceConfigPath = filepath.Join(workspaceDir, ".agent-memory.yaml")
			}

			fmt.Fprintln(os.Stderr, "\nConfiguration sources:")
			fmt.Fprintf(os.Stderr, "  User config: %s %s\n", userConfigPath, fileStatus(userConfigPath))
			if workspaceConfigPath != "" {
				fmt.Fprintf(os.Stderr, "  Workspace config: %s %s\n", workspaceConfigPath, fileStatus(workspaceConfigPath))
			}
			fmt.Fprintln(os.Stderr, "  Environment variables: active")

			return nil
		},
	}

	cmd.Flags().StringVar(&workspaceDir, "workspace", "", "Workspace directory (default: current directory)")

	return cmd
}

func newConfigInitCommand() *cobra.Command {
	var workspaceMode bool
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration file",
		Long: `Initialize a configuration file with defaults.

By default, creates a user-level configuration file at:
  ~/.agent-memory/config.yaml

With --workspace flag, creates a workspace-level configuration file at:
  ./.agent-memory.yaml

The generated file includes comments documenting all available options.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var configPath string
			if workspaceMode {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("getting current directory: %w", err)
				}
				configPath = filepath.Join(cwd, ".agent-memory.yaml")
			} else {
				homeDir, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("getting home directory: %w", err)
				}
				configPath = filepath.Join(homeDir, ".agent-memory", "config.yaml")
			}

			// Check if file exists
			if !force {
				if _, err := os.Stat(configPath); err == nil {
					return fmt.Errorf("config file already exists: %s (use --force to overwrite)", configPath)
				}
			}

			// Generate default config with comments
			cfg := config.DefaultConfig()
			configYAML, err := generateConfigWithComments(cfg)
			if err != nil {
				return fmt.Errorf("generating config: %w", err)
			}

			// Ensure directory exists
			dir := filepath.Dir(configPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("creating config directory: %w", err)
			}

			// Write config file
			if err := os.WriteFile(configPath, []byte(configYAML), 0644); err != nil {
				return fmt.Errorf("writing config file: %w", err)
			}

			fmt.Fprintf(os.Stderr, "✅ Created configuration file: %s\n", configPath)
			fmt.Fprintln(os.Stderr, "\nEdit the file to customize your configuration.")
			fmt.Fprintln(os.Stderr, "Run 'agent-memory config validate' to check for errors.")

			return nil
		},
	}

	cmd.Flags().BoolVar(&workspaceMode, "workspace", false, "Create workspace-level config (.agent-memory.yaml)")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing config file")

	return cmd
}

func newConfigSetCommand() *cobra.Command {
	var workspaceMode bool
	var budget int
	var topK int
	var semanticWeight, recencyWeight, outcomeWeight, decayWeight, tierBiasWeight float64

	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set configuration values",
		Long: `Set configuration values and persist them to file.

By default, updates the user-level configuration file at:
  ~/.agent-memory/config.yaml

With --workspace flag, updates the workspace-level configuration file at:
  ./.agent-memory.yaml

Examples:
  # Set default token budget
  agent-memory config set --budget 1200

  # Set workspace-specific budget
  agent-memory config set --workspace --budget 1500

  # Set top-k and budget together
  agent-memory config set --top-k 10 --budget 1000

  # Set retrieval weights (must sum to 1.0)
  agent-memory config set --semantic-weight 0.60 --recency-weight 0.20 \
    --outcome-weight 0.10 --decay-weight 0.05 --tier-bias-weight 0.05`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Determine config path
			var configPath string
			if workspaceMode {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("getting current directory: %w", err)
				}
				configPath = filepath.Join(cwd, ".agent-memory.yaml")
			} else {
				homeDir, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("getting home directory: %w", err)
				}
				configPath = filepath.Join(homeDir, ".agent-memory", "config.yaml")
			}

			// Load existing config or create new one
			var cfg *config.Config
			if _, err := os.Stat(configPath); os.IsNotExist(err) {
				// File doesn't exist, start with defaults
				cfg = config.DefaultConfig()
				fmt.Fprintf(os.Stderr, "Creating new config file: %s\n", configPath)
			} else {
				// Load existing config
				var workspaceDir string
				if workspaceMode {
					workspaceDir, _ = os.Getwd()
				}
				cfg, err = config.Load(workspaceDir)
				if err != nil {
					return fmt.Errorf("loading config: %w", err)
				}
			}

			// Track if any changes were made
			changed := false

			// Apply flag updates
			if cmd.Flags().Changed("budget") {
				if budget <= 0 {
					return fmt.Errorf("budget must be positive, got %d", budget)
				}
				cfg.Retrieval.DefaultBudget = budget
				changed = true
				fmt.Fprintf(os.Stderr, "Set default_budget: %d tokens\n", budget)
			}

			if cmd.Flags().Changed("top-k") {
				if topK <= 0 {
					return fmt.Errorf("top-k must be positive, got %d", topK)
				}
				cfg.Retrieval.DefaultTopK = topK
				changed = true
				fmt.Fprintf(os.Stderr, "Set default_top_k: %d\n", topK)
			}

			// Handle weight updates
			weightsChanged := false
			if cmd.Flags().Changed("semantic-weight") {
				cfg.Retrieval.SemanticWeight = semanticWeight
				weightsChanged = true
			}
			if cmd.Flags().Changed("recency-weight") {
				cfg.Retrieval.RecencyWeight = recencyWeight
				weightsChanged = true
			}
			if cmd.Flags().Changed("outcome-weight") {
				cfg.Retrieval.OutcomeWeight = outcomeWeight
				weightsChanged = true
			}
			if cmd.Flags().Changed("decay-weight") {
				cfg.Retrieval.DecayWeight = decayWeight
				weightsChanged = true
			}
			if cmd.Flags().Changed("tier-bias-weight") {
				cfg.Retrieval.TierBiasWeight = tierBiasWeight
				weightsChanged = true
			}

			if weightsChanged {
				// Validate weights sum to 1.0
				sum := cfg.Retrieval.SemanticWeight + cfg.Retrieval.RecencyWeight +
					cfg.Retrieval.OutcomeWeight + cfg.Retrieval.DecayWeight +
					cfg.Retrieval.TierBiasWeight
				if sum < 0.99 || sum > 1.01 {
					return fmt.Errorf("retrieval weights must sum to 1.0, got %.2f", sum)
				}
				changed = true
				fmt.Fprintf(os.Stderr, "Set retrieval weights (sum: %.2f)\n", sum)
			}

			if !changed {
				return fmt.Errorf("no configuration values provided to set")
			}

			// Validate the updated config
			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("configuration validation failed: %w", err)
			}

			// Save to file
			if err := cfg.Save(configPath); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			fmt.Fprintf(os.Stderr, "✅ Configuration saved to: %s\n", configPath)
			fmt.Fprintln(os.Stderr, "\nRun 'agent-memory config show' to view the updated configuration.")

			return nil
		},
	}

	cmd.Flags().BoolVar(&workspaceMode, "workspace", false, "Update workspace-level config (.agent-memory.yaml)")
	cmd.Flags().IntVar(&budget, "budget", 0, "Set default token budget for recall")
	cmd.Flags().IntVar(&topK, "top-k", 0, "Set default top-k for retrieval")
	cmd.Flags().Float64Var(&semanticWeight, "semantic-weight", 0, "Set semantic similarity weight")
	cmd.Flags().Float64Var(&recencyWeight, "recency-weight", 0, "Set recency weight")
	cmd.Flags().Float64Var(&outcomeWeight, "outcome-weight", 0, "Set outcome success weight")
	cmd.Flags().Float64Var(&decayWeight, "decay-weight", 0, "Set time decay weight")
	cmd.Flags().Float64Var(&tierBiasWeight, "tier-bias-weight", 0, "Set storage tier bias weight")

	return cmd
}

func printConfigText(cfg *config.Config) error {
	fmt.Println("=== Core Settings ===")
	fmt.Printf("  Enabled: %v\n", cfg.Enabled)
	fmt.Printf("  Data Directory: %s\n", cfg.DataDir)
	fmt.Printf("  Run Label: %s\n", cfg.RunLabel)
	if cfg.Workspace != "" {
		fmt.Printf("  Workspace: %s\n", cfg.Workspace)
	}

	fmt.Println("\n=== Storage ===")
	fmt.Printf("  DB Path: %s\n", cfg.Storage.DBPath)
	fmt.Printf("  Default Tier: %s\n", cfg.Storage.DefaultTier)
	fmt.Printf("  Auto Vacuum: %v\n", cfg.Storage.AutoVacuum)

	fmt.Println("\n=== Embeddings ===")
	fmt.Printf("  Provider: %s\n", cfg.Embeddings.Provider)
	fmt.Printf("  Model Path: %s\n", cfg.Embeddings.ModelPath)
	if cfg.Embeddings.RuntimePath != "" {
		fmt.Printf("  Runtime Path: %s\n", cfg.Embeddings.RuntimePath)
	}
	if cfg.Embeddings.Provider == "openai" {
		if cfg.Embeddings.OpenAIKey != "" {
			fmt.Printf("  OpenAI Key: %s***\n", cfg.Embeddings.OpenAIKey[:min(8, len(cfg.Embeddings.OpenAIKey))])
		}
		fmt.Printf("  Model Name: %s\n", cfg.Embeddings.ModelName)
	}
	fmt.Printf("  Dimensions: %d\n", cfg.Embeddings.Dimensions)
	fmt.Printf("  Cache Enabled: %v\n", cfg.Embeddings.CacheEnabled)

	fmt.Println("\n=== Retrieval ===")
	fmt.Printf("  Default Mode: %s\n", cfg.Retrieval.DefaultMode)
	fmt.Printf("  Default Top-K: %d\n", cfg.Retrieval.DefaultTopK)
	fmt.Printf("  Default Budget: %d tokens\n", cfg.Retrieval.DefaultBudget)
	fmt.Printf("  Weights:\n")
	fmt.Printf("    Semantic: %.2f\n", cfg.Retrieval.SemanticWeight)
	fmt.Printf("    Recency: %.2f\n", cfg.Retrieval.RecencyWeight)
	fmt.Printf("    Outcome: %.2f\n", cfg.Retrieval.OutcomeWeight)
	fmt.Printf("    Decay: %.2f\n", cfg.Retrieval.DecayWeight)
	fmt.Printf("    Tier Bias: %.2f\n", cfg.Retrieval.TierBiasWeight)

	fmt.Println("\n=== Dashboard ===")
	fmt.Printf("  Enabled: %v\n", cfg.Dashboard.Enabled)
	fmt.Printf("  Directory: %s\n", cfg.Dashboard.Dir)
	fmt.Printf("  Port: %d\n", cfg.Dashboard.Port)
	fmt.Printf("  Auto Launch: %v\n", cfg.Dashboard.AutoLaunch)

	fmt.Println("\n=== Server ===")
	fmt.Printf("  Host: %s\n", cfg.Server.Host)
	fmt.Printf("  Port: %d\n", cfg.Server.Port)
	fmt.Printf("  CORS Enabled: %v\n", cfg.Server.EnableCORS)

	fmt.Println("\n=== Observability ===")
	fmt.Printf("  Enabled: %v\n", cfg.Observe.Enabled)
	fmt.Printf("  Log Level: %s\n", cfg.Observe.LogLevel)
	fmt.Printf("  Log Format: %s\n", cfg.Observe.LogFormat)

	fmt.Println("\n=== Adaptive Tuning ===")
	fmt.Printf("  Enabled: %v\n", cfg.Adaptive.Enabled)
	fmt.Printf("  Feedback Cooldowns:\n")
	fmt.Printf("    Rejected: %s\n", cfg.Adaptive.FeedbackCooldowns.Rejected)
	fmt.Printf("    Harmful: %s\n", cfg.Adaptive.FeedbackCooldowns.Harmful)
	fmt.Printf("    Contradicted: %s\n", cfg.Adaptive.FeedbackCooldowns.Contradicted)

	return nil
}

func generateConfigWithComments(cfg *config.Config) (string, error) {
	return `# agent-memory configuration file
# 
# This file contains all available configuration options with their defaults.
# Uncomment and modify values as needed.

# Core settings
enabled: true                      # Enable/disable agent-memory globally
# data_dir: ~/.agent-memory        # Data directory (default: ~/.agent-memory)
# run_label: ""                    # Label for grouping operations in metrics
# workspace: ""                    # Workspace identifier (auto-detected)

# Storage configuration
storage:
  # db_path: ~/.agent-memory/agent-memory.db  # SQLite database path
  default_tier: vector            # Default storage tier (markdown, vector, archive)
  auto_vacuum: true               # Enable automatic database vacuuming
  # vacuum_interval_ms: 3600000   # Vacuum interval in milliseconds (1 hour)

# Embedding configuration
embeddings:
  provider: local                 # Embedding provider (local, openai)
  # model_path: ~/.agent-memory/models/all-MiniLM-L6-v2  # Local model path
  # runtime_path: ""              # ONNX Runtime path (auto-detected)
  # openai_key: ""                # OpenAI API key (or set OPENAI_API_KEY env var)
  # model_name: text-embedding-3-small  # OpenAI model name
  dimensions: 384                 # Embedding dimensions
  max_tokens: 512                 # Maximum tokens per embedding
  cache_enabled: true             # Enable embedding cache
  batch_size: 32                  # Batch size for embedding operations
  timeout_seconds: 30             # Timeout for embedding operations

# Retrieval configuration
retrieval:
  default_mode: search            # Default retrieval mode (search, recall, relate, outcomes)
  default_top_k: 8                # Number of results to return
  default_budget: 800             # Token budget for context
  
  # Scoring weights (must sum to 1.0)
  semantic_weight: 0.55           # Weight for semantic similarity
  recency_weight: 0.20            # Weight for recency
  outcome_weight: 0.10            # Weight for outcome success
  decay_weight: 0.05              # Weight for time decay
  tier_bias_weight: 0.10          # Weight for storage tier bias
  
  enable_reranking: false         # Enable LLM-based reranking
  retrieval_timeout: 10           # Timeout in seconds
  enable_explanation: false       # Include retrieval explanations

# Dashboard configuration
dashboard:
  enabled: true                   # Enable dashboard
  # dir: ~/.agent-memory/dashboard  # Dashboard installation directory
  port: 3042                      # Dashboard port
  auto_launch: false              # Auto-launch dashboard in browser

# API server configuration
server:
  host: localhost                 # Server host
  port: 8042                      # Server port
  enable_cors: true               # Enable CORS
  allowed_origins: "*"            # Allowed CORS origins
  read_timeout: 30                # Read timeout in seconds
  write_timeout: 30               # Write timeout in seconds

# Observability configuration
observe:
  enabled: true                   # Enable observability
  # metrics_port: 9042            # Metrics port
  # tracing_backend: ""           # Tracing backend (jaeger, zipkin, etc.)
  log_level: info                 # Log level (debug, info, warn, error)
  log_format: text                # Log format (json, text)

# Upgrade configuration
upgrade:
  auto_upgrade: false             # Enable automatic upgrades
  check_interval: "24h"           # Upgrade check interval
  # source_dir: ""                # Source directory for upgrades

# Adaptive tuning configuration
adaptive:
  enabled: true                   # Enable adaptive tuning
  
  # Policy defaults for different retrieval modes
  policy_defaults:
    search:
      min_semantic_score: 0.30
      min_total_score: 0.02
      relative_score_cutoff: 0.01
    recall:
      min_semantic_score: 0.30
      min_total_score: 0.02
      relative_score_cutoff: 0.01
    relate:
      min_semantic_score: 0.30
      min_total_score: 0.02
      relative_score_cutoff: 0.01
    outcomes:
      min_semantic_score: 0.30
      min_total_score: 0.02
      relative_score_cutoff: 0.01
  
  # Feedback cooldown periods
  feedback_cooldowns:
    rejected_cooldown: "6h"       # Cooldown for rejected memories
    harmful_cooldown: "24h"       # Cooldown for harmful memories
    contradicted_cooldown: "30m"  # Cooldown for contradicted memories
`, nil
}

func fileStatus(path string) string {
	if _, err := os.Stat(path); err == nil {
		return "✓ exists"
	}
	return "✗ not found"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
