package plugin

import (
	"context"

	"github.com/time/timebooks/agent-memory/internal/embeddings"
)

// EmbeddingPlugin extends Plugin with embedding capabilities.
type EmbeddingPlugin interface {
	Plugin
	
	// Provider returns the embeddings provider.
	Provider() embeddings.Provider
}

// BaseEmbeddingPlugin provides a base implementation for embedding plugins.
type BaseEmbeddingPlugin struct {
	name        string
	version     string
	description string
	provider    embeddings.Provider
	config      map[string]any
}

// NewBaseEmbeddingPlugin creates a new base embedding plugin.
func NewBaseEmbeddingPlugin(name, version, description string, provider embeddings.Provider) *BaseEmbeddingPlugin {
	return &BaseEmbeddingPlugin{
		name:        name,
		version:     version,
		description: description,
		provider:    provider,
	}
}

// Name returns the plugin name.
func (p *BaseEmbeddingPlugin) Name() string {
	return p.name
}

// Version returns the plugin version.
func (p *BaseEmbeddingPlugin) Version() string {
	return p.version
}

// Description returns the plugin description.
func (p *BaseEmbeddingPlugin) Description() string {
	return p.description
}

// Initialize initializes the plugin.
func (p *BaseEmbeddingPlugin) Initialize(ctx context.Context, config map[string]any) error {
	p.config = config
	return nil
}

// Shutdown shuts down the plugin.
func (p *BaseEmbeddingPlugin) Shutdown(ctx context.Context) error {
	return nil
}

// Provider returns the embeddings provider.
func (p *BaseEmbeddingPlugin) Provider() embeddings.Provider {
	return p.provider
}

// GetEmbeddingPlugin retrieves an embedding plugin by name.
func GetEmbeddingPlugin(name string) (EmbeddingPlugin, error) {
	plugin, err := GetRegistry().Get(name)
	if err != nil {
		return nil, err
	}
	
	embPlugin, ok := plugin.(EmbeddingPlugin)
	if !ok {
		return nil, ErrInvalidPluginType
	}
	
	return embPlugin, nil
}
