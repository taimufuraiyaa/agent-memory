// Package plugin provides a plugin system for extending agent-memory.
package plugin

import (
	"context"
	"fmt"
	"sync"
)

// Plugin represents a loadable plugin.
type Plugin interface {
	// Name returns the plugin name.
	Name() string
	
	// Version returns the plugin version.
	Version() string
	
	// Description returns a human-readable description.
	Description() string
	
	// Initialize initializes the plugin with configuration.
	Initialize(ctx context.Context, config map[string]any) error
	
	// Shutdown gracefully shuts down the plugin.
	Shutdown(ctx context.Context) error
}

// PluginType represents the type of plugin.
type PluginType string

const (
	PluginTypeEmbedding   PluginType = "embedding"
	PluginTypeStorage     PluginType = "storage"
	PluginTypeLifecycle   PluginType = "lifecycle"
	PluginTypeMiddleware  PluginType = "middleware"
	PluginTypeExtension   PluginType = "extension"
)

// PluginMetadata contains plugin metadata.
type PluginMetadata struct {
	Name        string
	Version     string
	Type        PluginType
	Description string
	Author      string
	License     string
	Repository  string
}

// Registry manages plugin registration and lifecycle.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
	types   map[PluginType][]string
	meta    map[string]PluginMetadata
}

// NewRegistry creates a new plugin registry.
func NewRegistry() *Registry {
	return &Registry{
		plugins: make(map[string]Plugin),
		types:   make(map[PluginType][]string),
		meta:    make(map[string]PluginMetadata),
	}
}

var (
	globalRegistry *Registry
	registryOnce   sync.Once
)

// GetRegistry returns the global plugin registry.
func GetRegistry() *Registry {
	registryOnce.Do(func() {
		globalRegistry = NewRegistry()
	})
	return globalRegistry
}

// Register registers a plugin with metadata.
func (r *Registry) Register(plugin Plugin, metadata PluginMetadata) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	name := plugin.Name()
	if name == "" {
		return fmt.Errorf("plugin name cannot be empty")
	}
	
	if _, exists := r.plugins[name]; exists {
		return fmt.Errorf("plugin %q already registered", name)
	}
	
	r.plugins[name] = plugin
	r.meta[name] = metadata
	
	// Track by type
	r.types[metadata.Type] = append(r.types[metadata.Type], name)
	
	return nil
}

// Unregister removes a plugin from the registry.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	plugin, exists := r.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %q not found", name)
	}
	
	// Shutdown the plugin
	if err := plugin.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("shutdown failed: %w", err)
	}
	
	// Remove from registry
	meta := r.meta[name]
	delete(r.plugins, name)
	delete(r.meta, name)
	
	// Remove from type index
	pluginType := meta.Type
	plugins := r.types[pluginType]
	for i, pname := range plugins {
		if pname == name {
			r.types[pluginType] = append(plugins[:i], plugins[i+1:]...)
			break
		}
	}
	
	return nil
}

// Get retrieves a plugin by name.
func (r *Registry) Get(name string) (Plugin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	plugin, exists := r.plugins[name]
	if !exists {
		return nil, fmt.Errorf("plugin %q not found", name)
	}
	
	return plugin, nil
}

// GetMetadata retrieves plugin metadata by name.
func (r *Registry) GetMetadata(name string) (PluginMetadata, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	meta, exists := r.meta[name]
	if !exists {
		return PluginMetadata{}, fmt.Errorf("plugin %q not found", name)
	}
	
	return meta, nil
}

// List returns all registered plugin names.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	names := make([]string, 0, len(r.plugins))
	for name := range r.plugins {
		names = append(names, name)
	}
	return names
}

// ListByType returns plugin names of a specific type.
func (r *Registry) ListByType(pluginType PluginType) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	names := r.types[pluginType]
	result := make([]string, len(names))
	copy(result, names)
	return result
}

// Count returns the total number of registered plugins.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.plugins)
}

// CountByType returns the number of plugins of a specific type.
func (r *Registry) CountByType(pluginType PluginType) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.types[pluginType])
}

// InitializeAll initializes all registered plugins.
func (r *Registry) InitializeAll(ctx context.Context, config map[string]any) error {
	r.mu.RLock()
	plugins := make([]Plugin, 0, len(r.plugins))
	for _, plugin := range r.plugins {
		plugins = append(plugins, plugin)
	}
	r.mu.RUnlock()
	
	for _, plugin := range plugins {
		if err := plugin.Initialize(ctx, config); err != nil {
			return fmt.Errorf("failed to initialize plugin %q: %w", plugin.Name(), err)
		}
	}
	
	return nil
}

// ShutdownAll gracefully shuts down all plugins.
func (r *Registry) ShutdownAll(ctx context.Context) error {
	r.mu.RLock()
	plugins := make([]Plugin, 0, len(r.plugins))
	for _, plugin := range r.plugins {
		plugins = append(plugins, plugin)
	}
	r.mu.RUnlock()
	
	var errs []error
	for _, plugin := range plugins {
		if err := plugin.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("plugin %q shutdown failed: %w", plugin.Name(), err))
		}
	}
	
	if len(errs) > 0 {
		return fmt.Errorf("shutdown errors: %v", errs)
	}
	
	return nil
}
