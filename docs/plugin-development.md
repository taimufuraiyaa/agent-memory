# Plugin Development Guide

This guide covers everything you need to know to create plugins for agent-memory.

## Table of Contents

1. [Overview](#overview)
2. [Plugin Types](#plugin-types)
3. [Core Interfaces](#core-interfaces)
4. [Creating Plugins](#creating-plugins)
5. [Plugin Registry](#plugin-registry)
6. [Testing Plugins](#testing-plugins)
7. [Best Practices](#best-practices)
8. [Examples](#examples)
9. [Distribution](#distribution)

## Overview

The agent-memory plugin system allows developers to extend functionality without modifying the core codebase. Plugins are Go packages that implement specific interfaces and register with the plugin registry.

### Key Concepts

- **Plugin Interface**: Base interface all plugins must implement
- **Plugin Types**: Specialized interfaces for different capabilities
- **Registry**: Central registry for plugin registration and discovery
- **Lifecycle**: Plugin initialization, operation, and shutdown
- **Metadata**: Descriptive information about plugins

### Plugin Architecture

```
┌─────────────────────────────────────┐
│        Application Code             │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│         Plugin Registry             │
│  ┌──────────────────────────────┐   │
│  │  Registered Plugins          │   │
│  │  - embedding/openai-v1       │   │
│  │  - lifecycle/audit-log       │   │
│  │  - lifecycle/metrics         │   │
│  └──────────────────────────────┘   │
└──────────────┬──────────────────────┘
               │
               ▼
┌─────────────────────────────────────┐
│      Individual Plugins             │
│  ┌────────────┐  ┌───────────────┐  │
│  │ Embedding  │  │  Lifecycle    │  │
│  │  Plugin    │  │    Plugin     │  │
│  └────────────┘  └───────────────┘  │
└─────────────────────────────────────┘
```

## Plugin Types

### 1. Embedding Plugins

Provide custom embedding generation for memory content.

**Use Cases:**
- Integrate different AI models (OpenAI, Cohere, Anthropic)
- Use local models (Ollama, llama.cpp)
- Implement custom embedding algorithms
- Support domain-specific embeddings

**Interface:**
```go
type EmbeddingPlugin interface {
    Plugin
    Provider() embeddings.Provider
}
```

### 2. Lifecycle Plugins

Hook into memory lifecycle events for logging, monitoring, validation, etc.

**Use Cases:**
- Audit logging
- Metrics collection
- Access control
- Data validation
- External notifications
- Change tracking

**Interface:**
```go
type LifecyclePlugin interface {
    Plugin
    OnWrite(ctx context.Context, mem *core.MemoryEntry) error
    OnWriteComplete(ctx context.Context, mem *core.MemoryEntry) error
    OnRetrieve(ctx context.Context, query string, workspace string) error
    OnRetrieveComplete(ctx context.Context, query string, hits int) error
    OnDelete(ctx context.Context, memoryID string) error
    OnDecay(ctx context.Context, workspace string, count int) error
}
```

### 3. Storage Plugins (Future)

Alternative storage backends beyond SQLite.

**Planned:**
- PostgreSQL backend
- Redis backend
- Cloud storage (S3, GCS)
- Distributed databases

### 4. Middleware Plugins (Future)

Request/response processing and transformation.

**Planned:**
- Request validation
- Response filtering
- Rate limiting
- Caching layers

### 5. Extension Plugins (Future)

General-purpose extensions and utilities.

**Planned:**
- Export formats
- Data transformations
- Integrations
- Custom commands

## Core Interfaces

### Base Plugin Interface

All plugins must implement:

```go
type Plugin interface {
    // Name returns the plugin name (unique identifier)
    Name() string
    
    // Version returns semantic version (e.g., "1.0.0")
    Version() string
    
    // Description returns human-readable description
    Description() string
    
    // Initialize sets up the plugin with configuration
    Initialize(ctx context.Context, config map[string]any) error
    
    // Shutdown gracefully stops the plugin
    Shutdown(ctx context.Context) error
}
```

### Plugin Metadata

Descriptive information registered with plugins:

```go
type PluginMetadata struct {
    Name        string     // Plugin name (must match Plugin.Name())
    Version     string     // Semantic version
    Type        PluginType // Plugin type
    Description string     // Human-readable description
    Author      string     // Author name or organization
    License     string     // License (MIT, Apache, etc.)
    Repository  string     // Source code repository URL
}
```

## Creating Plugins

### Step 1: Implement the Plugin Interface

```go
package myplugin

import (
    "context"
    "github.com/time/timebooks/agent-memory/internal/plugin"
)

type MyPlugin struct {
    name        string
    version     string
    description string
    config      map[string]any
    // ... plugin-specific fields
}

func NewMyPlugin() *MyPlugin {
    return &MyPlugin{
        name:        "my-plugin",
        version:     "1.0.0",
        description: "My custom plugin",
    }
}

func (p *MyPlugin) Name() string {
    return p.name
}

func (p *MyPlugin) Version() string {
    return p.version
}

func (p *MyPlugin) Description() string {
    return p.description
}

func (p *MyPlugin) Initialize(ctx context.Context, config map[string]any) error {
    p.config = config
    // Perform initialization (connect to services, load resources, etc.)
    return nil
}

func (p *MyPlugin) Shutdown(ctx context.Context) error {
    // Clean up resources
    return nil
}
```

### Step 2: Implement Type-Specific Interface

#### Embedding Plugin Example

```go
package myplugin

import (
    "context"
    "github.com/time/timebooks/agent-memory/internal/embeddings"
    "github.com/time/timebooks/agent-memory/internal/plugin"
)

type MyEmbedder struct {
    // provider implementation
}

func (e *MyEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
    // Generate embeddings
    return embeddings, nil
}

func (e *MyEmbedder) Dimensions() int {
    return 384
}

func (e *MyEmbedder) Model() string {
    return "my-model-v1"
}

type MyEmbeddingPlugin struct {
    *plugin.BaseEmbeddingPlugin
}

func NewMyEmbeddingPlugin() *MyEmbeddingPlugin {
    embedder := &MyEmbedder{}
    base := plugin.NewBaseEmbeddingPlugin(
        "my-embedder",
        "1.0.0",
        "My custom embedder",
        embedder,
    )
    return &MyEmbeddingPlugin{BaseEmbeddingPlugin: base}
}
```

#### Lifecycle Plugin Example

```go
package myplugin

import (
    "context"
    "github.com/time/timebooks/agent-memory/internal/core"
    "github.com/time/timebooks/agent-memory/internal/plugin"
)

type MyLifecyclePlugin struct {
    *plugin.BaseLifecyclePlugin
    // custom fields
}

func NewMyLifecyclePlugin() *MyLifecyclePlugin {
    return &MyLifecyclePlugin{
        BaseLifecyclePlugin: plugin.NewBaseLifecyclePlugin(
            "my-lifecycle",
            "1.0.0",
            "My lifecycle plugin",
        ),
    }
}

func (p *MyLifecyclePlugin) OnWrite(ctx context.Context, mem *core.MemoryEntry) error {
    // Hook logic before write
    return nil
}

func (p *MyLifecyclePlugin) OnWriteComplete(ctx context.Context, mem *core.MemoryEntry) error {
    // Hook logic after write
    return nil
}

// Implement other lifecycle methods...
```

### Step 3: Register the Plugin

```go
package main

import (
    "github.com/time/timebooks/agent-memory/internal/plugin"
    "myplugin"
)

func init() {
    // Auto-register on import
    registry := plugin.GetRegistry()
    p := myplugin.NewMyPlugin()
    
    err := registry.Register(p, plugin.PluginMetadata{
        Name:        "my-plugin",
        Version:     "1.0.0",
        Type:        plugin.PluginTypeLifecycle,
        Description: "My custom plugin",
        Author:      "Your Name",
        License:     "MIT",
        Repository:  "https://github.com/yourorg/my-plugin",
    })
    if err != nil {
        panic(err)
    }
}
```

## Plugin Registry

### Global Registry

Access the singleton registry:

```go
registry := plugin.GetRegistry()
```

### Registration

```go
err := registry.Register(myPlugin, metadata)
if err != nil {
    // Handle error (duplicate name, invalid plugin, etc.)
}
```

### Discovery

```go
// Get plugin by name
plugin, err := registry.Get("my-plugin")

// Get metadata
metadata, err := registry.GetMetadata("my-plugin")

// List all plugins
names := registry.List()

// List by type
lifecyclePlugins := registry.ListByType(plugin.PluginTypeLifecycle)

// Count plugins
total := registry.Count()
typeCount := registry.CountByType(plugin.PluginTypeEmbedding)
```

### Initialization

```go
// Initialize all plugins
config := map[string]any{
    "apiKey": "sk-...",
    "timeout": 30,
}
err := registry.InitializeAll(ctx, config)

// Initialize single plugin
plugin, _ := registry.Get("my-plugin")
err := plugin.Initialize(ctx, config)
```

### Shutdown

```go
// Shutdown all plugins
err := registry.ShutdownAll(ctx)

// Unregister and shutdown single plugin
err := registry.Unregister("my-plugin")
```

## Testing Plugins

### Unit Tests

```go
package myplugin

import (
    "context"
    "testing"
    "github.com/stretchr/testify/require"
)

func TestMyPlugin_Initialize(t *testing.T) {
    plugin := NewMyPlugin()
    
    config := map[string]any{
        "key": "value",
    }
    
    err := plugin.Initialize(context.Background(), config)
    require.NoError(t, err)
}

func TestMyPlugin_Functionality(t *testing.T) {
    plugin := NewMyPlugin()
    plugin.Initialize(context.Background(), nil)
    
    // Test plugin functionality
    result, err := plugin.SomeMethod()
    require.NoError(t, err)
    require.NotNil(t, result)
}

func TestMyPlugin_Shutdown(t *testing.T) {
    plugin := NewMyPlugin()
    plugin.Initialize(context.Background(), nil)
    
    err := plugin.Shutdown(context.Background())
    require.NoError(t, err)
}
```

### Integration Tests

```go
func TestPluginIntegration(t *testing.T) {
    // Create registry
    registry := plugin.NewRegistry()
    
    // Register plugin
    p := NewMyPlugin()
    err := registry.Register(p, plugin.PluginMetadata{
        Name:    "my-plugin",
        Version: "1.0.0",
        Type:    plugin.PluginTypeLifecycle,
    })
    require.NoError(t, err)
    
    // Initialize
    err = registry.InitializeAll(context.Background(), nil)
    require.NoError(t, err)
    
    // Test plugin operations
    plugin, err := registry.Get("my-plugin")
    require.NoError(t, err)
    require.NotNil(t, plugin)
    
    // Cleanup
    err = registry.ShutdownAll(context.Background())
    require.NoError(t, err)
}
```

### Mock Plugins

```go
type MockPlugin struct {
    InitializeCalled bool
    ShutdownCalled   bool
}

func (m *MockPlugin) Name() string        { return "mock" }
func (m *MockPlugin) Version() string     { return "1.0.0" }
func (m *MockPlugin) Description() string { return "mock plugin" }

func (m *MockPlugin) Initialize(ctx context.Context, config map[string]any) error {
    m.InitializeCalled = true
    return nil
}

func (m *MockPlugin) Shutdown(ctx context.Context) error {
    m.ShutdownCalled = true
    return nil
}
```

## Best Practices

### 1. Naming Conventions

- Use descriptive, unique names (e.g., "openai-embedder", "audit-logger")
- Include provider or functionality in name
- Use lowercase with hyphens for consistency
- Avoid generic names like "plugin1" or "test"

### 2. Versioning

- Follow semantic versioning (MAJOR.MINOR.PATCH)
- Increment MAJOR for breaking changes
- Increment MINOR for new features
- Increment PATCH for bug fixes
- Document changes in CHANGELOG.md

### 3. Configuration

- Accept configuration via `Initialize()`
- Validate configuration values
- Provide sensible defaults
- Document all configuration options
- Support environment variables for secrets

```go
func (p *MyPlugin) Initialize(ctx context.Context, config map[string]any) error {
    // Extract and validate config
    apiKey, ok := config["apiKey"].(string)
    if !ok || apiKey == "" {
        return fmt.Errorf("apiKey is required")
    }
    
    // Use defaults
    timeout := 30
    if t, ok := config["timeout"].(int); ok {
        timeout = t
    }
    
    // Initialize with config
    return p.setup(apiKey, timeout)
}
```

### 4. Error Handling

- Return descriptive errors with context
- Use `fmt.Errorf()` with `%w` for wrapping
- Don't panic in plugins (return errors instead)
- Log errors appropriately
- Handle partial failures gracefully

```go
func (p *MyPlugin) OnWrite(ctx context.Context, mem *core.MemoryEntry) error {
    if err := p.validate(mem); err != nil {
        return fmt.Errorf("validation failed: %w", err)
    }
    
    if err := p.process(mem); err != nil {
        return fmt.Errorf("processing failed: %w", err)
    }
    
    return nil
}
```

### 5. Resource Management

- Clean up resources in `Shutdown()`
- Close files, connections, and handles
- Cancel background goroutines
- Flush buffers and caches
- Handle shutdown timeouts with context

```go
func (p *MyPlugin) Shutdown(ctx context.Context) error {
    // Close resources
    if p.file != nil {
        p.file.Close()
    }
    
    // Cancel background work
    if p.cancel != nil {
        p.cancel()
    }
    
    // Wait for goroutines with timeout
    done := make(chan struct{})
    go func() {
        p.wg.Wait()
        close(done)
    }()
    
    select {
    case <-done:
        return nil
    case <-ctx.Done():
        return ctx.Err()
    }
}
```

### 6. Thread Safety

- Use mutexes for shared state
- Avoid data races
- Document thread safety guarantees
- Consider concurrent access patterns

```go
type MyPlugin struct {
    mu    sync.RWMutex
    state map[string]any
}

func (p *MyPlugin) Get(key string) any {
    p.mu.RLock()
    defer p.mu.RUnlock()
    return p.state[key]
}

func (p *MyPlugin) Set(key string, value any) {
    p.mu.Lock()
    defer p.mu.Unlock()
    p.state[key] = value
}
```

### 7. Performance

- Keep lifecycle hooks fast (< 1ms)
- Use async processing for expensive operations
- Cache frequently accessed data
- Batch operations when possible
- Profile and benchmark critical paths

```go
func (p *MyPlugin) OnWrite(ctx context.Context, mem *core.MemoryEntry) error {
    // Quick synchronous validation
    if err := p.validateFast(mem); err != nil {
        return err
    }
    
    // Async expensive processing
    go p.processAsync(mem)
    
    return nil
}
```

### 8. Documentation

- Document package with `doc.go`
- Add godoc comments to exported types
- Document configuration options
- Provide usage examples
- Include README.md with plugin

### 9. Testing

- Write comprehensive unit tests
- Test error conditions
- Mock external dependencies
- Test concurrent access
- Benchmark performance-critical code

### 10. Security

- Validate all inputs
- Sanitize file paths
- Secure API keys and secrets
- Use HTTPS for API calls
- Implement rate limiting
- Avoid command injection

## Examples

See the `examples/plugins/` directory for complete examples:

- **audit-logger**: Lifecycle plugin that logs all memory operations
- **custom-embedder**: Embedding plugin with hash-based and API templates

## Distribution

### As Go Module

```bash
# Create module
go mod init github.com/yourorg/my-plugin

# Publish to GitHub
git tag v1.0.0
git push origin v1.0.0
```

### Import in Applications

```go
import "github.com/yourorg/my-plugin"

func init() {
    // Auto-registers on import
    _ "github.com/yourorg/my-plugin"
}
```

### As Shared Library (Future)

Dynamic loading from `.so` files (planned feature).

## Troubleshooting

### Plugin Not Found

```go
// Check registration
names := registry.List()
fmt.Println("Registered plugins:", names)
```

### Initialization Fails

```go
// Check error details
if err := plugin.Initialize(ctx, config); err != nil {
    fmt.Printf("Init failed: %v\n", err)
}
```

### Type Assertion Fails

```go
// Verify plugin type
plugin, _ := registry.Get("my-plugin")
if _, ok := plugin.(plugin.EmbeddingPlugin); !ok {
    fmt.Println("Not an embedding plugin")
}
```

## Resources

- [Package Documentation](../internal/plugin/doc.go)
- [Example Plugins](../examples/plugins/)
- [API Reference](https://pkg.go.dev/github.com/time/timebooks/agent-memory/internal/plugin)
- [Contributing Guide](../CONTRIBUTING.md)

## Support

For questions and issues:
- Open an issue on GitHub
- Check existing documentation
- Review example plugins
- Join community discussions
