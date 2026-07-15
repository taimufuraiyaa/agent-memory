// Package integration manages reversible coding-agent configuration adapters.
package integration

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type Options struct {
	Root      string
	DataDir   string
	Workspace string
	DryRun    bool
	Force     bool
}

type Result struct {
	Agent    string   `json:"agent"`
	Planned  []string `json:"planned,omitempty"`
	Applied  []string `json:"applied,omitempty"`
	Removed  []string `json:"removed,omitempty"`
	Skipped  []string `json:"skipped,omitempty"`
	Backups  []string `json:"backups,omitempty"`
	Verified bool     `json:"verified"`
}

type Adapter interface {
	Name() string
	Detect(context.Context, Options) (bool, error)
	Plan(context.Context, Options) (Result, error)
	Connect(context.Context, Options) (Result, error)
	Disconnect(context.Context, Options) (Result, error)
	Verify(context.Context, Options) (Result, error)
}

type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewRegistry() *Registry { return &Registry{adapters: make(map[string]Adapter)} }

func (r *Registry) Register(adapter Adapter) error {
	name := normalizeName(adapter.Name())
	if name == "" {
		return fmt.Errorf("adapter name is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[name]; exists {
		return fmt.Errorf("adapter already registered: %s", name)
	}
	r.adapters[name] = adapter
	return nil
}

func (r *Registry) Adapter(name string) (Adapter, error) {
	name = normalizeName(name)
	r.mu.RLock()
	adapter, ok := r.adapters[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown agent adapter: %s", name)
	}
	return adapter, nil
}

func (r *Registry) Connect(ctx context.Context, name string, options Options) (Result, error) {
	adapter, err := r.Adapter(name)
	if err != nil {
		return Result{}, err
	}
	if options.DryRun {
		return adapter.Plan(ctx, options)
	}
	return adapter.Connect(ctx, options)
}

func (r *Registry) Disconnect(ctx context.Context, name string, options Options) (Result, error) {
	adapter, err := r.Adapter(name)
	if err != nil {
		return Result{}, err
	}
	if options.DryRun {
		return adapter.Plan(ctx, options)
	}
	return adapter.Disconnect(ctx, options)
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
