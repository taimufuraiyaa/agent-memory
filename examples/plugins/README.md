# Plugin Examples

This directory contains example plugin implementations for agent-memory.

## Available Examples

### 1. Audit Logger (`audit-logger/`)

A lifecycle plugin that logs all memory operations for audit and debugging purposes.

**Features:**
- Logs write, retrieve, delete, and decay events
- Supports text and JSON output formats
- Configurable log file or stdout
- Integration examples for Splunk, ELK, CloudWatch

**Use Cases:**
- Audit compliance and regulatory requirements
- Debugging memory access patterns
- Performance monitoring and analytics
- Security incident investigation

[View README](./audit-logger/README.md)

### 2. Custom Embedder (`custom-embedder/`)

An embedding plugin demonstrating custom embedding provider integration.

**Includes:**
- `SimpleHashEmbedder`: Hash-based embedder (demo/testing only)
- `RealWorldEmbedder`: Template for real API integration
- Integration examples for OpenAI, Cohere, Ollama

**Use Cases:**
- Integrate different AI models (OpenAI, Anthropic, Cohere)
- Use local embedding models (Ollama, llama.cpp)
- Implement custom embedding algorithms
- Support domain-specific embeddings

[View README](./custom-embedder/README.md)

## Using These Examples

### 1. Import and Register

```go
package main

import (
    "context"
    "github.com/taimufuraiyaa/agent-memory/examples/plugins/audit-logger"
    "github.com/taimufuraiyaa/agent-memory/internal/plugin"
)

func main() {
    // Create plugin
    auditPlugin := auditlogger.NewAuditLogPlugin()
    
    // Register
    registry := plugin.GetRegistry()
    err := registry.Register(auditPlugin, plugin.PluginMetadata{
        Name:        "audit-logger",
        Version:     "1.0.0",
        Type:        plugin.PluginTypeLifecycle,
        Description: "Logs all memory operations",
        Author:      "agent-memory",
        License:     "MIT",
    })
    if err != nil {
        panic(err)
    }
    
    // Initialize with config
    config := map[string]any{
        "logFile":  "/var/log/agent-memory/audit.log",
        "jsonMode": true,
    }
    err = auditPlugin.Initialize(context.Background(), config)
    if err != nil {
        panic(err)
    }
    
    // Plugin is now active
}
```

### 2. Auto-Registration Pattern

```go
// plugin/init.go
package plugin

import (
    _ "github.com/taimufuraiyaa/agent-memory/examples/plugins/audit-logger"
)

// Plugins auto-register on import
```

### 3. Dynamic Configuration

```go
// Load plugin config from file
config, err := loadPluginConfig("config.yaml")
if err != nil {
    panic(err)
}

// Initialize all plugins
registry := plugin.GetRegistry()
err = registry.InitializeAll(context.Background(), config)
```

## Creating Your Own Plugin

Follow these steps to create a custom plugin:

1. **Choose Plugin Type**: Embedding, Lifecycle, Storage, etc.
2. **Implement Interface**: Implement required plugin interface
3. **Add Logic**: Implement your custom functionality
4. **Test**: Write comprehensive unit tests
5. **Document**: Add README and godoc comments
6. **Register**: Register with plugin registry

See the [Plugin Development Guide](../../docs/plugin-development.md) for detailed instructions.

## Plugin Types

### Embedding Plugins

Provide custom embedding generation for memory content.

**Interface:**
```go
type EmbeddingPlugin interface {
    Plugin
    Provider() embeddings.Provider
}
```

### Lifecycle Plugins

Hook into memory lifecycle events.

**Interface:**
```go
type LifecyclePlugin interface {
    Plugin
    OnWrite(ctx context.Context, mem *core.MemoryEntry) error
    OnWriteComplete(ctx context.Context, mem *core.MemoryEntry) error
    OnRetrieve(ctx context.Context, query, workspace string) error
    OnRetrieveComplete(ctx context.Context, query string, hits int) error
    OnDelete(ctx context.Context, memoryID string) error
    OnDecay(ctx context.Context, workspace string, count int) error
}
```

## Testing Plugins

```bash
# Test all plugins
go test ./examples/plugins/...

# Test specific plugin
go test ./examples/plugins/audit-logger

# Run with coverage
go test -cover ./examples/plugins/...

# Benchmark plugins
go test -bench=. ./examples/plugins/...
```

## Plugin Ideas

Looking for inspiration? Here are some plugin ideas:

### Lifecycle Plugins
- **Metrics Collector**: Send metrics to Prometheus/StatsD
- **Slack Notifier**: Post notifications to Slack
- **Rate Limiter**: Enforce rate limits per workspace
- **Access Controller**: Implement RBAC or ACLs
- **Change Tracker**: Track all memory changes
- **Backup Manager**: Auto-backup on writes

### Embedding Plugins
- **OpenAI Integration**: Use OpenAI embeddings
- **Cohere Integration**: Use Cohere embeddings
- **Local Models**: Ollama, llama.cpp, FastEmbed
- **Multi-Model**: Combine multiple embedding models
- **Cached Embedder**: Cache embeddings in Redis
- **Batch Embedder**: Batch API calls for efficiency

### Storage Plugins (Future)
- **PostgreSQL Backend**: Use PostgreSQL instead of SQLite
- **Redis Backend**: Use Redis for fast access
- **S3 Backend**: Store memories in S3
- **Multi-Tier**: Hot/warm/cold storage tiers

### Middleware Plugins (Future)
- **Request Validator**: Validate all inputs
- **Response Filter**: Filter sensitive data
- **Encryption**: Encrypt content at rest
- **Compression**: Compress large content
- **Deduplication**: Detect duplicate memories

## Resources

- [Plugin Development Guide](../../docs/plugin-development.md)
- [API Documentation](../../internal/plugin/doc.go)
- [Contributing Guide](../../CONTRIBUTING.md)

## License

These examples are licensed under the MIT License. See [LICENSE](../../LICENSE) for details.
