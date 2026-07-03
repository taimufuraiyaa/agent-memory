package plugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// MockLifecyclePlugin is a test lifecycle plugin.
type MockLifecyclePlugin struct {
	*BaseLifecyclePlugin
	onWriteCalled            bool
	onWriteCompleteCalled    bool
	onRetrieveCalled         bool
	onRetrieveCompleteCalled bool
	onDeleteCalled           bool
	onDecayCalled            bool
	shouldError              bool
}

func NewMockLifecyclePlugin() *MockLifecyclePlugin {
	return &MockLifecyclePlugin{
		BaseLifecyclePlugin: NewBaseLifecyclePlugin(
			"mock-lifecycle",
			"1.0.0",
			"Mock lifecycle plugin",
		),
	}
}

func (m *MockLifecyclePlugin) OnWrite(ctx context.Context, mem *core.MemoryEntry) error {
	m.onWriteCalled = true
	if m.shouldError {
		return ErrPluginInitFailed
	}
	return nil
}

func (m *MockLifecyclePlugin) OnWriteComplete(ctx context.Context, mem *core.MemoryEntry) error {
	m.onWriteCompleteCalled = true
	if m.shouldError {
		return ErrPluginInitFailed
	}
	return nil
}

func (m *MockLifecyclePlugin) OnRetrieve(ctx context.Context, query string, workspace string) error {
	m.onRetrieveCalled = true
	if m.shouldError {
		return ErrPluginInitFailed
	}
	return nil
}

func (m *MockLifecyclePlugin) OnRetrieveComplete(ctx context.Context, query string, hits int) error {
	m.onRetrieveCompleteCalled = true
	if m.shouldError {
		return ErrPluginInitFailed
	}
	return nil
}

func (m *MockLifecyclePlugin) OnDelete(ctx context.Context, memoryID string) error {
	m.onDeleteCalled = true
	if m.shouldError {
		return ErrPluginInitFailed
	}
	return nil
}

func (m *MockLifecyclePlugin) OnDecay(ctx context.Context, workspace string, count int) error {
	m.onDecayCalled = true
	if m.shouldError {
		return ErrPluginInitFailed
	}
	return nil
}

func TestBaseLifecyclePlugin(t *testing.T) {
	plugin := NewBaseLifecyclePlugin("test", "1.0.0", "Test lifecycle plugin")

	require.Equal(t, "test", plugin.Name())
	require.Equal(t, "1.0.0", plugin.Version())
	require.Equal(t, "Test lifecycle plugin", plugin.Description())

	// Test Initialize
	err := plugin.Initialize(context.Background(), nil)
	require.NoError(t, err)

	// Test Shutdown
	err = plugin.Shutdown(context.Background())
	require.NoError(t, err)

	// Test lifecycle methods (should be no-ops)
	mem := &core.MemoryEntry{ID: "test"}
	err = plugin.OnWrite(context.Background(), mem)
	require.NoError(t, err)

	err = plugin.OnWriteComplete(context.Background(), mem)
	require.NoError(t, err)

	err = plugin.OnRetrieve(context.Background(), "query", "workspace")
	require.NoError(t, err)

	err = plugin.OnRetrieveComplete(context.Background(), "query", 5)
	require.NoError(t, err)

	err = plugin.OnDelete(context.Background(), "mem-id")
	require.NoError(t, err)

	err = plugin.OnDecay(context.Background(), "workspace", 10)
	require.NoError(t, err)
}

func TestNewLifecycleManager(t *testing.T) {
	registry := NewRegistry()
	manager := NewLifecycleManager(registry)

	require.NotNil(t, manager)
	require.Equal(t, registry, manager.registry)
}

func TestLifecycleManager_TriggerOnWrite(t *testing.T) {
	registry := NewRegistry()
	manager := NewLifecycleManager(registry)

	plugin := NewMockLifecyclePlugin()
	err := registry.Register(plugin, PluginMetadata{
		Name: "mock-lifecycle",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)

	mem := &core.MemoryEntry{
		ID:        "test-mem",
		Content:   "test content",
		Workspace: "default",
	}

	err = manager.TriggerOnWrite(context.Background(), mem)
	require.NoError(t, err)
	require.True(t, plugin.onWriteCalled)
}

func TestLifecycleManager_TriggerOnWrite_Error(t *testing.T) {
	registry := NewRegistry()
	manager := NewLifecycleManager(registry)

	plugin := NewMockLifecyclePlugin()
	plugin.shouldError = true

	err := registry.Register(plugin, PluginMetadata{
		Name: "mock-lifecycle",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)

	mem := &core.MemoryEntry{ID: "test"}
	err = manager.TriggerOnWrite(context.Background(), mem)
	require.Error(t, err)
}

func TestLifecycleManager_TriggerOnWriteComplete(t *testing.T) {
	registry := NewRegistry()
	manager := NewLifecycleManager(registry)

	plugin := NewMockLifecyclePlugin()
	err := registry.Register(plugin, PluginMetadata{
		Name: "mock-lifecycle",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)

	mem := &core.MemoryEntry{ID: "test"}
	err = manager.TriggerOnWriteComplete(context.Background(), mem)
	require.NoError(t, err)
	require.True(t, plugin.onWriteCompleteCalled)
}

func TestLifecycleManager_TriggerOnRetrieve(t *testing.T) {
	registry := NewRegistry()
	manager := NewLifecycleManager(registry)

	plugin := NewMockLifecyclePlugin()
	err := registry.Register(plugin, PluginMetadata{
		Name: "mock-lifecycle",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)

	err = manager.TriggerOnRetrieve(context.Background(), "test query", "default")
	require.NoError(t, err)
	require.True(t, plugin.onRetrieveCalled)
}

func TestLifecycleManager_TriggerOnRetrieveComplete(t *testing.T) {
	registry := NewRegistry()
	manager := NewLifecycleManager(registry)

	plugin := NewMockLifecyclePlugin()
	err := registry.Register(plugin, PluginMetadata{
		Name: "mock-lifecycle",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)

	err = manager.TriggerOnRetrieveComplete(context.Background(), "test query", 5)
	require.NoError(t, err)
	require.True(t, plugin.onRetrieveCompleteCalled)
}

func TestLifecycleManager_TriggerOnDelete(t *testing.T) {
	registry := NewRegistry()
	manager := NewLifecycleManager(registry)

	plugin := NewMockLifecyclePlugin()
	err := registry.Register(plugin, PluginMetadata{
		Name: "mock-lifecycle",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)

	err = manager.TriggerOnDelete(context.Background(), "mem-123")
	require.NoError(t, err)
	require.True(t, plugin.onDeleteCalled)
}

func TestLifecycleManager_TriggerOnDecay(t *testing.T) {
	registry := NewRegistry()
	manager := NewLifecycleManager(registry)

	plugin := NewMockLifecyclePlugin()
	err := registry.Register(plugin, PluginMetadata{
		Name: "mock-lifecycle",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)

	err = manager.TriggerOnDecay(context.Background(), "default", 42)
	require.NoError(t, err)
	require.True(t, plugin.onDecayCalled)
}

func TestLifecycleManager_MultiplePlugins(t *testing.T) {
	registry := NewRegistry()
	manager := NewLifecycleManager(registry)

	// Register multiple lifecycle plugins
	plugin1 := NewMockLifecyclePlugin()
	plugin2 := NewMockLifecyclePlugin()

	err := registry.Register(plugin1, PluginMetadata{
		Name: "mock-lifecycle",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)

	// Use different name for second plugin
	plugin2.BaseLifecyclePlugin = NewBaseLifecyclePlugin(
		"mock-lifecycle-2",
		"1.0.0",
		"Mock lifecycle plugin 2",
	)

	err = registry.Register(plugin2, PluginMetadata{
		Name: "mock-lifecycle-2",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)

	// Trigger OnWrite
	mem := &core.MemoryEntry{ID: "test"}
	err = manager.TriggerOnWrite(context.Background(), mem)
	require.NoError(t, err)

	// Both plugins should be called
	require.True(t, plugin1.onWriteCalled)
	require.True(t, plugin2.onWriteCalled)
}

func TestLifecycleManager_SkipsNonLifecyclePlugins(t *testing.T) {
	registry := NewRegistry()
	manager := NewLifecycleManager(registry)

	// Register a non-lifecycle plugin
	mockPlugin := NewMockPlugin("test", "1.0.0", "Test")
	err := registry.Register(mockPlugin, PluginMetadata{
		Name: "test",
		Type: PluginTypeEmbedding, // Not lifecycle
	})
	require.NoError(t, err)

	// Register a lifecycle plugin
	lifecyclePlugin := NewMockLifecyclePlugin()
	err = registry.Register(lifecyclePlugin, PluginMetadata{
		Name: "mock-lifecycle",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)

	// Trigger should only call lifecycle plugin
	mem := &core.MemoryEntry{ID: "test"}
	err = manager.TriggerOnWrite(context.Background(), mem)
	require.NoError(t, err)

	require.True(t, lifecyclePlugin.onWriteCalled)
}
