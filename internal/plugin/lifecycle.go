package plugin

import (
	"context"

	"github.com/time/timebooks/agent-memory/internal/core"
)

// LifecyclePlugin provides hooks into memory lifecycle events.
type LifecyclePlugin interface {
	Plugin
	
	// OnWrite is called before a memory is written.
	OnWrite(ctx context.Context, mem *core.MemoryEntry) error
	
	// OnWriteComplete is called after a memory is successfully written.
	OnWriteComplete(ctx context.Context, mem *core.MemoryEntry) error
	
	// OnRetrieve is called before memories are retrieved.
	OnRetrieve(ctx context.Context, query string, workspace string) error
	
	// OnRetrieveComplete is called after memories are retrieved.
	OnRetrieveComplete(ctx context.Context, query string, hits int) error
	
	// OnDelete is called before a memory is deleted.
	OnDelete(ctx context.Context, memoryID string) error
	
	// OnDecay is called when decay scores are updated.
	OnDecay(ctx context.Context, workspace string, count int) error
}

// BaseLifecyclePlugin provides a no-op base implementation.
type BaseLifecyclePlugin struct {
	name        string
	version     string
	description string
}

// NewBaseLifecyclePlugin creates a new base lifecycle plugin.
func NewBaseLifecyclePlugin(name, version, description string) *BaseLifecyclePlugin {
	return &BaseLifecyclePlugin{
		name:        name,
		version:     version,
		description: description,
	}
}

// Name returns the plugin name.
func (p *BaseLifecyclePlugin) Name() string {
	return p.name
}

// Version returns the plugin version.
func (p *BaseLifecyclePlugin) Version() string {
	return p.version
}

// Description returns the plugin description.
func (p *BaseLifecyclePlugin) Description() string {
	return p.description
}

// Initialize initializes the plugin.
func (p *BaseLifecyclePlugin) Initialize(ctx context.Context, config map[string]any) error {
	return nil
}

// Shutdown shuts down the plugin.
func (p *BaseLifecyclePlugin) Shutdown(ctx context.Context) error {
	return nil
}

// OnWrite is called before a memory is written.
func (p *BaseLifecyclePlugin) OnWrite(ctx context.Context, mem *core.MemoryEntry) error {
	return nil
}

// OnWriteComplete is called after a memory is written.
func (p *BaseLifecyclePlugin) OnWriteComplete(ctx context.Context, mem *core.MemoryEntry) error {
	return nil
}

// OnRetrieve is called before memories are retrieved.
func (p *BaseLifecyclePlugin) OnRetrieve(ctx context.Context, query string, workspace string) error {
	return nil
}

// OnRetrieveComplete is called after memories are retrieved.
func (p *BaseLifecyclePlugin) OnRetrieveComplete(ctx context.Context, query string, hits int) error {
	return nil
}

// OnDelete is called before a memory is deleted.
func (p *BaseLifecyclePlugin) OnDelete(ctx context.Context, memoryID string) error {
	return nil
}

// OnDecay is called when decay scores are updated.
func (p *BaseLifecyclePlugin) OnDecay(ctx context.Context, workspace string, count int) error {
	return nil
}

// LifecycleManager manages lifecycle plugin execution.
type LifecycleManager struct {
	registry *Registry
}

// NewLifecycleManager creates a new lifecycle manager.
func NewLifecycleManager(registry *Registry) *LifecycleManager {
	return &LifecycleManager{registry: registry}
}

// TriggerOnWrite triggers OnWrite hooks for all lifecycle plugins.
func (m *LifecycleManager) TriggerOnWrite(ctx context.Context, mem *core.MemoryEntry) error {
	plugins := m.registry.ListByType(PluginTypeLifecycle)
	for _, name := range plugins {
		plugin, err := m.registry.Get(name)
		if err != nil {
			continue
		}
		
		if lp, ok := plugin.(LifecyclePlugin); ok {
			if err := lp.OnWrite(ctx, mem); err != nil {
				return err
			}
		}
	}
	return nil
}

// TriggerOnWriteComplete triggers OnWriteComplete hooks.
func (m *LifecycleManager) TriggerOnWriteComplete(ctx context.Context, mem *core.MemoryEntry) error {
	plugins := m.registry.ListByType(PluginTypeLifecycle)
	for _, name := range plugins {
		plugin, err := m.registry.Get(name)
		if err != nil {
			continue
		}
		
		if lp, ok := plugin.(LifecyclePlugin); ok {
			if err := lp.OnWriteComplete(ctx, mem); err != nil {
				return err
			}
		}
	}
	return nil
}

// TriggerOnRetrieve triggers OnRetrieve hooks.
func (m *LifecycleManager) TriggerOnRetrieve(ctx context.Context, query, workspace string) error {
	plugins := m.registry.ListByType(PluginTypeLifecycle)
	for _, name := range plugins {
		plugin, err := m.registry.Get(name)
		if err != nil {
			continue
		}
		
		if lp, ok := plugin.(LifecyclePlugin); ok {
			if err := lp.OnRetrieve(ctx, query, workspace); err != nil {
				return err
			}
		}
	}
	return nil
}

// TriggerOnRetrieveComplete triggers OnRetrieveComplete hooks.
func (m *LifecycleManager) TriggerOnRetrieveComplete(ctx context.Context, query string, hits int) error {
	plugins := m.registry.ListByType(PluginTypeLifecycle)
	for _, name := range plugins {
		plugin, err := m.registry.Get(name)
		if err != nil {
			continue
		}
		
		if lp, ok := plugin.(LifecyclePlugin); ok {
			if err := lp.OnRetrieveComplete(ctx, query, hits); err != nil {
				return err
			}
		}
	}
	return nil
}

// TriggerOnDelete triggers OnDelete hooks.
func (m *LifecycleManager) TriggerOnDelete(ctx context.Context, memoryID string) error {
	plugins := m.registry.ListByType(PluginTypeLifecycle)
	for _, name := range plugins {
		plugin, err := m.registry.Get(name)
		if err != nil {
			continue
		}
		
		if lp, ok := plugin.(LifecyclePlugin); ok {
			if err := lp.OnDelete(ctx, memoryID); err != nil {
				return err
			}
		}
	}
	return nil
}

// TriggerOnDecay triggers OnDecay hooks.
func (m *LifecycleManager) TriggerOnDecay(ctx context.Context, workspace string, count int) error {
	plugins := m.registry.ListByType(PluginTypeLifecycle)
	for _, name := range plugins {
		plugin, err := m.registry.Get(name)
		if err != nil {
			continue
		}
		
		if lp, ok := plugin.(LifecyclePlugin); ok {
			if err := lp.OnDecay(ctx, workspace, count); err != nil {
				return err
			}
		}
	}
	return nil
}
