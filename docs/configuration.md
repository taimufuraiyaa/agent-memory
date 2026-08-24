# Configuration Guide

agent-memory provides a flexible configuration system that supports multiple sources with clear precedence rules.

## Configuration Sources

Configuration is loaded from multiple sources in the following order (lowest to highest precedence):

1. **Defaults** - Built-in default values
2. **User Config** - `~/.agent-memory/config.yaml`
3. **Workspace Config** - `.agent-memory.yaml` in your project root
4. **Environment Variables** - `AGENT_MEMORY_*` variables
5. **CLI Flags** - Command-line arguments

Later sources override earlier ones. For example, an environment variable will override a value from the user config file.

## Quick Start

### View Current Configuration

```bash
# Show current effective configuration
agent-memory config show

# Show as JSON
agent-memory config show --format json

# Show as YAML (default)
agent-memory config show --format yaml
```

### Validate Configuration

```bash
# Validate current configuration
agent-memory config validate
```

### Initialize Configuration Files

```bash
# Create user-level config file
agent-memory config init

# Create workspace-level config file (in current directory)
agent-memory config init --workspace
```

## Configuration File Format

Configuration files use YAML format. Here's an example with commonly used options:

```yaml
# Core settings
enabled: true
data_dir: ~/.agent-memory
run_label: my-project

# Storage configuration
storage:
  default_tier: vector      # Options: markdown, vector, archive
  auto_vacuum: true

# Embedding configuration
embeddings:
  provider: local           # Options: local, openai
  dimensions: 384
  cache_enabled: true

# Retrieval configuration
retrieval:
  default_mode: search      # Options: search, recall, relate, outcomes
  default_top_k: 8
  default_budget: 800
  
  # Scoring weights (must sum to 1.0)
  semantic_weight: 0.55
  recency_weight: 0.20
  outcome_weight: 0.10
  decay_weight: 0.05
  tier_bias_weight: 0.10

# Dashboard configuration
dashboard:
  enabled: true
  port: 3042
  auto_launch: false

# Observability configuration
observe:
  log_level: info           # Options: debug, info, warn, error
  log_format: text          # Options: text, json
```

## Configuration Options

### Core Settings

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | bool | `true` | Enable/disable agent-memory globally |
| `data_dir` | string | `~/.agent-memory` | Data directory for all agent-memory files |
| `run_label` | string | `""` | Label for grouping operations in metrics |
| `workspace` | string | auto-detected | Workspace identifier |

### Storage Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `storage.db_path` | string | `~/.agent-memory/agent-memory.db` | SQLite database path |
| `storage.default_tier` | string | `vector` | Default storage tier (markdown, vector, archive) |
| `storage.auto_vacuum` | bool | `true` | Enable automatic database vacuuming |
| `storage.vacuum_interval_ms` | int | `3600000` | Vacuum interval in milliseconds (1 hour) |

### Embedding Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `embeddings.provider` | string | `local` | Embedding provider (local, openai) |
| `embeddings.model_path` | string | `~/.agent-memory/models/all-MiniLM-L6-v2` | Path to local ONNX model |
| `embeddings.runtime_path` | string | auto-detected | Path to ONNX Runtime library |
| `embeddings.openai_key` | string | `""` | OpenAI API key (or use `OPENAI_API_KEY` env var) |
| `embeddings.model_name` | string | `text-embedding-3-small` | Model name for cloud providers |
| `embeddings.dimensions` | int | `384` | Embedding dimensions |
| `embeddings.max_tokens` | int | `512` | Maximum tokens per embedding |
| `embeddings.cache_enabled` | bool | `true` | Enable embedding cache |
| `embeddings.batch_size` | int | `32` | Batch size for embedding operations |
| `embeddings.timeout_seconds` | int | `30` | Timeout for embedding operations |

### Retrieval Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `retrieval.default_mode` | string | `search` | Default retrieval mode (search, recall, relate, outcomes) |
| `retrieval.default_top_k` | int | `8` | Number of results to return |
| `retrieval.default_budget` | int | `800` | Token budget for context |
| `retrieval.semantic_weight` | float | `0.55` | Weight for semantic similarity (must sum to 1.0 with other weights) |
| `retrieval.recency_weight` | float | `0.20` | Weight for recency |
| `retrieval.outcome_weight` | float | `0.10` | Weight for outcome success |
| `retrieval.decay_weight` | float | `0.05` | Weight for time decay |
| `retrieval.tier_bias_weight` | float | `0.10` | Weight for storage tier bias |
| `retrieval.enable_reranking` | bool | `false` | Enable LLM-based reranking |
| `retrieval.retrieval_timeout` | int | `10` | Timeout in seconds |
| `retrieval.enable_explanation` | bool | `false` | Include retrieval explanations |

### Dashboard Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `dashboard.enabled` | bool | `true` | Enable dashboard |
| `dashboard.dir` | string | `~/.agent-memory/dashboard` | Dashboard installation directory |
| `dashboard.port` | int | `3042` | Dashboard port |
| `dashboard.auto_launch` | bool | `false` | Auto-launch dashboard in browser |

### Server Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `server.host` | string | `localhost` | API server host |
| `server.port` | int | `8042` | API server port |
| `server.enable_cors` | bool | `true` | Enable CORS |
| `server.allowed_origins` | string | `*` | Allowed CORS origins |
| `server.read_timeout` | int | `30` | Read timeout in seconds |
| `server.write_timeout` | int | `30` | Write timeout in seconds |

### Observability Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `observe.enabled` | bool | `true` | Enable observability |
| `observe.metrics_port` | int | `9042` | Metrics port |
| `observe.tracing_backend` | string | `""` | Tracing backend (jaeger, zipkin, etc.) |
| `observe.log_level` | string | `info` | Log level (debug, info, warn, error) |
| `observe.log_format` | string | `text` | Log format (json, text) |

### Upgrade Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `upgrade.auto_upgrade` | bool | `false` | Enable automatic upgrades |
| `upgrade.check_interval` | string | `24h` | Upgrade check interval |
| `upgrade.source_dir` | string | `""` | Source directory for upgrades |

### Adaptive Tuning Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `adaptive.enabled` | bool | `true` | Enable adaptive tuning |
| `adaptive.policy_defaults` | map | see below | Policy thresholds per mode |
| `adaptive.feedback_cooldowns.rejected_cooldown` | string | `6h` | Cooldown for rejected memories |
| `adaptive.feedback_cooldowns.harmful_cooldown` | string | `24h` | Cooldown for harmful memories |
| `adaptive.feedback_cooldowns.contradicted_cooldown` | string | `30m` | Cooldown for contradicted memories |

## Environment Variables

All configuration options can be set via environment variables using the `AGENT_MEMORY_` prefix:

```bash
# Core settings
export AGENT_MEMORY_ENABLED=1
export AGENT_MEMORY_DATA_DIR=/custom/data/dir
export AGENT_MEMORY_RUN_LABEL=my-session

# Embeddings
export AGENT_MEMORY_EMBEDDING_PROVIDER=openai
export AGENT_MEMORY_ONNX_RUNTIME_PATH=/path/to/libonnxruntime.so
export OPENAI_API_KEY=sk-...

# Dashboard
export AGENT_MEMORY_DASHBOARD_DIR=/custom/dashboard
export AGENT_MEMORY_DASHBOARD_PORT=4000

# Observability
export AGENT_MEMORY_OBSERVE_ENABLED=1
export AGENT_MEMORY_LOG_LEVEL=debug

# Upgrade
export AGENT_MEMORY_SRC_DIR=/path/to/source

# Storage
export AGENT_MEMORY_DB_PATH=/custom/db/path.db
```

## Common Workflows

### Per-Project Configuration

Create a workspace config file in your project:

```bash
cd my-project
agent-memory config init --workspace
```

Edit `.agent-memory.yaml` to customize settings for this project:

```yaml
run_label: my-project
retrieval:
  default_top_k: 10
  default_budget: 1000
observe:
  log_level: debug
```

### Using OpenAI Embeddings

Configure OpenAI as your embedding provider:

```yaml
embeddings:
  provider: openai
  openai_key: sk-...  # Or set OPENAI_API_KEY env var
  model_name: text-embedding-3-small
  dimensions: 1536
```

Or use environment variables:

```bash
export AGENT_MEMORY_EMBEDDING_PROVIDER=openai
export OPENAI_API_KEY=sk-...
```

### Custom Retrieval Weights

Adjust retrieval scoring weights (must sum to 1.0):

```yaml
retrieval:
  semantic_weight: 0.60   # Emphasize semantic similarity
  recency_weight: 0.25    # More weight on recent memories
  outcome_weight: 0.05
  decay_weight: 0.05
  tier_bias_weight: 0.05
```

### Debug Mode

Enable debug logging for troubleshooting:

```yaml
observe:
  log_level: debug
  log_format: json  # Structured logging
```

Or use environment variable:

```bash
export AGENT_MEMORY_LOG_LEVEL=debug
```

## Validation

The configuration system includes comprehensive validation:

```bash
agent-memory config validate
```

Common validation errors:

- **Invalid storage tier**: Must be `markdown`, `vector`, or `archive`
- **Invalid embedding provider**: Must be `local` or `openai`
- **Weights don't sum to 1.0**: Retrieval weights must sum to exactly 1.0
- **Invalid port**: Ports must be between 1 and 65535
- **Invalid log level**: Must be `debug`, `info`, `warn`, or `error`
- **Invalid cooldown duration**: Must be a valid Go duration (e.g., `6h`, `30m`, `2h30m`)

## Best Practices

1. **Use workspace configs for project-specific settings**: Keep global settings in `~/.agent-memory/config.yaml` and project-specific overrides in `.agent-memory.yaml`

2. **Store secrets in environment variables**: Don't commit API keys to version control. Use environment variables or a secrets manager.

3. **Validate after changes**: Always run `agent-memory config validate` after editing config files.

4. **Document custom settings**: Add comments to your config files explaining why you changed defaults.

5. **Version control workspace configs**: Commit `.agent-memory.yaml` to share project settings with your team.

6. **Use run labels for organization**: Set `run_label` to group related operations in metrics and logs.

## Troubleshooting

### Config file not being loaded

Check the file path and permissions:

```bash
# Check user config
ls -la ~/.agent-memory/config.yaml

# Check workspace config
ls -la .agent-memory.yaml

# Verify configuration sources
agent-memory config validate
```

### Validation errors

Run validation to see detailed error messages:

```bash
agent-memory config validate
```

### Environment variables not working

Ensure variables are exported and use the correct prefix:

```bash
# Check if variable is set
echo $AGENT_MEMORY_ENABLED

# Set and export
export AGENT_MEMORY_ENABLED=1
```

### Conflicting settings

Remember the precedence order: defaults < user config < workspace config < environment variables < CLI flags.

View effective configuration to see which value won:

```bash
agent-memory config show --format text
```

## Migration from Environment Variables

If you're currently using environment variables, you can migrate to config files:

1. Create a config file:
   ```bash
   agent-memory config init
   ```

2. Add your settings to `~/.agent-memory/config.yaml`:
   ```yaml
   # Instead of: export AGENT_MEMORY_ENABLED=1
   enabled: true
   
   # Instead of: export AGENT_MEMORY_DATA_DIR=/custom/dir
   data_dir: /custom/dir
   ```

3. Remove environment variables (optional - they will still override config files if present)

4. Validate:
   ```bash
   agent-memory config validate
   ```

## See Also

- [Adaptive Tuning](./adaptive-tuning.md) - Details on adaptive policy configuration
- [CLI Reference](./cli-reference.md) - Complete CLI command documentation
- [Architecture](./architecture.md) - System architecture and design
