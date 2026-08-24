// Package plugin provides a flexible plugin system for extending agent-memory.
//
// # Overview
//
// The plugin system allows developers to extend agent-memory's functionality
// without modifying the core codebase. Plugins can add new embedding providers,
// storage backends, lifecycle hooks, middleware, and custom extensions.
//
// # Plugin Types
//
// The system supports five plugin types:
//
//   - Embedding: Custom embedding providers (e.g., different AI models)
//   - Storage: Alternative storage backends (e.g., PostgreSQL, Redis)
//   - Lifecycle: Hooks into memory lifecycle events (write, retrieve, delete, decay)
//   - Middleware: Request/response processing and transformation
//   - Extension: General-purpose extensions and utilities
//
// # Core Interfaces
//
// All plugins must implement the base Plugin interface:
//
//	type Plugin interface {
//	    Name() string
//	    Version() string
//	    Description() string
//	    Initialize(ctx context.Context, config map[string]any) error
//	    Shutdown(ctx context.Context) error
//	}
//
// Additional interfaces extend Plugin for specific capabilities:
//
//   - EmbeddingPlugin: Provides custom embedding generation
//   - LifecyclePlugin: Hooks into memory lifecycle events
//   - StoragePlugin: Alternative storage backends (future)
//   - MiddlewarePlugin: Request/response processing (future)
//
// # Plugin Registry
//
// The Registry manages plugin registration and lifecycle:
//
//	// Get global registry
//	registry := plugin.GetRegistry()
//
//	// Register a plugin
//	err := registry.Register(myPlugin, plugin.PluginMetadata{
//	    Name:        "my-plugin",
//	    Version:     "1.0.0",
//	    Type:        plugin.PluginTypeEmbedding,
//	    Description: "Custom embedding provider",
//	    Author:      "Your Name",
//	    License:     "MIT",
//	})
//
//	// Initialize all plugins
//	err = registry.InitializeAll(ctx, config)
//
//	// Retrieve a plugin
//	plugin, err := registry.Get("my-plugin")
//
//	// List plugins by type
//	embedders := registry.ListByType(plugin.PluginTypeEmbedding)
//
//	// Shutdown all plugins
//	err = registry.ShutdownAll(ctx)
//
// # Creating an Embedding Plugin
//
// Embedding plugins provide custom embedding generation:
//
//	type MyEmbeddingProvider struct {
//	    // implementation fields
//	}
//
//	func (p *MyEmbeddingProvider) Embed(ctx context.Context, text string) ([]float32, error) {
//	    // custom embedding logic
//	    return embeddings, nil
//	}
//
//	func (p *MyEmbeddingProvider) Dimensions() int {
//	    return 384
//	}
//
//	func (p *MyEmbeddingProvider) Model() string {
//	    return "my-model-v1"
//	}
//
//	// Create and register plugin
//	provider := &MyEmbeddingProvider{}
//	plugin := plugin.NewBaseEmbeddingPlugin(
//	    "my-embedder",
//	    "1.0.0",
//	    "Custom embedding provider",
//	    provider,
//	)
//
//	err := plugin.GetRegistry().Register(plugin, plugin.PluginMetadata{
//	    Name:    "my-embedder",
//	    Version: "1.0.0",
//	    Type:    plugin.PluginTypeEmbedding,
//	})
//
// # Creating a Lifecycle Plugin
//
// Lifecycle plugins hook into memory operations:
//
//	type AuditLogPlugin struct {
//	    *plugin.BaseLifecyclePlugin
//	    logger *log.Logger
//	}
//
//	func NewAuditLogPlugin() *AuditLogPlugin {
//	    return &AuditLogPlugin{
//	        BaseLifecyclePlugin: plugin.NewBaseLifecyclePlugin(
//	            "audit-log",
//	            "1.0.0",
//	            "Logs all memory operations",
//	        ),
//	        logger: log.New(os.Stdout, "[AUDIT] ", log.LstdFlags),
//	    }
//	}
//
//	func (p *AuditLogPlugin) OnWrite(ctx context.Context, mem *core.MemoryEntry) error {
//	    p.logger.Printf("Writing memory: %s (workspace: %s)", mem.ID, mem.Workspace)
//	    return nil
//	}
//
//	func (p *AuditLogPlugin) OnRetrieve(ctx context.Context, query string, workspace string) error {
//	    p.logger.Printf("Retrieving: query=%s workspace=%s", query, workspace)
//	    return nil
//	}
//
//	// Register plugin
//	plugin := NewAuditLogPlugin()
//	err := plugin.GetRegistry().Register(plugin, plugin.PluginMetadata{
//	    Name:    "audit-log",
//	    Version: "1.0.0",
//	    Type:    plugin.PluginTypeLifecycle,
//	})
//
// # Lifecycle Manager
//
// The LifecycleManager triggers lifecycle hooks:
//
//	manager := plugin.NewLifecycleManager(registry)
//
//	// Before writing
//	err := manager.TriggerOnWrite(ctx, memory)
//
//	// After writing
//	err = manager.TriggerOnWriteComplete(ctx, memory)
//
//	// Before retrieval
//	err = manager.TriggerOnRetrieve(ctx, query, workspace)
//
//	// After retrieval
//	err = manager.TriggerOnRetrieveComplete(ctx, query, hitCount)
//
//	// Before deletion
//	err = manager.TriggerOnDelete(ctx, memoryID)
//
//	// After decay
//	err = manager.TriggerOnDecay(ctx, workspace, count)
//
// # Error Handling
//
// The plugin package defines standard errors:
//
//   - ErrPluginNotFound: Plugin doesn't exist in registry
//   - ErrPluginAlreadyRegistered: Duplicate plugin name
//   - ErrInvalidPluginType: Plugin doesn't implement expected interface
//   - ErrPluginInitFailed: Plugin initialization failed
//   - ErrPluginShutdownFailed: Plugin shutdown failed
//
// # Best Practices
//
//  1. Plugin Naming: Use descriptive, unique names (e.g., "openai-embedder", "audit-logger")
//  2. Versioning: Follow semantic versioning (major.minor.patch)
//  3. Configuration: Accept configuration via Initialize() method
//  4. Resource Cleanup: Release resources in Shutdown() method
//  5. Error Handling: Return descriptive errors with context
//  6. Thread Safety: Use mutexes if maintaining shared state
//  7. Testing: Write comprehensive unit tests for plugins
//  8. Documentation: Document configuration options and behavior
//
// # Plugin Discovery
//
// Plugins can be registered in several ways:
//
//   - Programmatically: Direct registration in application code
//   - Init Functions: Use init() to auto-register plugins
//   - Configuration: Load plugins from configuration files
//   - Dynamic Loading: Load plugins from shared libraries (future)
//
// # Example: Auto-Registration
//
//	// plugin/myplugin/plugin.go
//	package myplugin
//
//	import "github.com/taimufuraiyaa/agent-memory/internal/plugin"
//
//	func init() {
//	    p := NewMyPlugin()
//	    plugin.GetRegistry().Register(p, plugin.PluginMetadata{
//	        Name:    "my-plugin",
//	        Version: "1.0.0",
//	        Type:    plugin.PluginTypeLifecycle,
//	    })
//	}
//
// # Thread Safety
//
// The Registry is thread-safe and can be safely accessed from multiple goroutines.
// Plugin implementations should ensure their own thread safety if maintaining state.
//
// # Performance Considerations
//
//   - Lifecycle hooks are called synchronously; keep operations fast
//   - For expensive operations, consider async processing with goroutines
//   - Use buffered channels or queues for high-throughput logging
//   - Cache expensive computations in plugin state
//   - Profile plugin performance with benchmarks
//
// # Future Extensions
//
// Planned plugin types for future releases:
//
//   - Storage plugins for alternative backends (PostgreSQL, Redis)
//   - Middleware plugins for request/response processing
//   - Transform plugins for data transformation pipelines
//   - Export plugins for custom export formats
//   - Notification plugins for external integrations
//
// # See Also
//
// For detailed plugin development guides, see:
//   - docs/plugin-development.md: Plugin development guide
//   - examples/plugins/: Example plugin implementations
package plugin
