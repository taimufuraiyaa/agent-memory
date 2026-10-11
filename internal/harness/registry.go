package harness

import (
	"fmt"
	"sort"
	"sync"
)

type Factory func() (Provider, error)

type registration struct {
	manifest Manifest
	factory  Factory
}

// Registry holds immutable static declarations and opens scoped provider sessions.
// Registration belongs at the composition root, not inside the run loop.
type Registry struct {
	mu        sync.RWMutex
	providers map[ProviderID]registration
	live      map[liveKey]*Session
}

// liveKey identifies the one open session a provider may have per workspace.
type liveKey struct {
	provider  ProviderID
	workspace string
	run       string
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[ProviderID]registration), live: make(map[liveKey]*Session)}
}

func (r *Registry) Register(manifest Manifest, factory Factory) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	if factory == nil {
		return fmt.Errorf("%w: nil provider factory", ErrInvalid)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers == nil {
		r.providers = make(map[ProviderID]registration)
	}
	if _, exists := r.providers[manifest.ID]; exists {
		return ErrDuplicate
	}
	copyManifest := manifest
	copyManifest.Capabilities = append([]CapabilityID(nil), manifest.Capabilities...)
	r.providers[manifest.ID] = registration{manifest: copyManifest, factory: factory}
	return nil
}

func (r *Registry) Manifests() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	items := make([]Manifest, 0, len(r.providers))
	for _, registered := range r.providers {
		manifest := registered.manifest
		manifest.Capabilities = append([]CapabilityID(nil), manifest.Capabilities...)
		items = append(items, manifest)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (r *Registry) Open(id ProviderID) (Manifest, Provider, error) {
	r.mu.RLock()
	registered, ok := r.providers[id]
	r.mu.RUnlock()
	if !ok {
		return Manifest{}, nil, ErrUnknown
	}
	provider, err := registered.factory()
	if err != nil {
		return Manifest{}, nil, err
	}
	if provider == nil {
		return Manifest{}, nil, fmt.Errorf("%w: factory returned nil", ErrInvalid)
	}
	switch registered.manifest.Kind {
	case KindDecision:
		_, ok = provider.(DecisionProvider)
	case KindModel:
		_, ok = provider.(ModelProvider)
	case KindTool:
		_, ok = provider.(ToolProvider)
	}
	if !ok {
		_ = provider.Close()
		return Manifest{}, nil, fmt.Errorf("%w: factory does not implement declared kind", ErrInvalid)
	}
	manifest := registered.manifest
	manifest.Capabilities = append([]CapabilityID(nil), manifest.Capabilities...)
	return manifest, provider, nil
}
