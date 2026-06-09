package plugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// MockPlugin is a test plugin implementation.
type MockPlugin struct {
	name           string
	version        string
	description    string
	initCalled     bool
	shutdownCalled bool
	initError      error
	shutdownError  error
}

func NewMockPlugin(name, version, description string) *MockPlugin {
	return &MockPlugin{
		name:        name,
		version:     version,
		description: description,
	}
}

func (m *MockPlugin) Name() string {
	return m.name
}

func (m *MockPlugin) Version() string {
	return m.version
}

func (m *MockPlugin) Description() string {
	return m.description
}

func (m *MockPlugin) Initialize(ctx context.Context, config map[string]any) error {
	m.initCalled = true
	return m.initError
}

func (m *MockPlugin) Shutdown(ctx context.Context) error {
	m.shutdownCalled = true
	return m.shutdownError
}

func TestNewRegistry(t *testing.T) {
	registry := NewRegistry()
	require.NotNil(t, registry)
	require.Equal(t, 0, registry.Count())
}

func TestGetRegistry(t *testing.T) {
	registry1 := GetRegistry()
	registry2 := GetRegistry()
	require.NotNil(t, registry1)
	require.Equal(t, registry1, registry2, "GetRegistry should return singleton")
}

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry()
	plugin := NewMockPlugin("test-plugin", "1.0.0", "Test plugin")
	
	metadata := PluginMetadata{
		Name:        "test-plugin",
		Version:     "1.0.0",
		Type:        PluginTypeLifecycle,
		Description: "Test plugin",
		Author:      "Test Author",
		License:     "MIT",
	}
	
	err := registry.Register(plugin, metadata)
	require.NoError(t, err)
	require.Equal(t, 1, registry.Count())
	
	// Verify plugin is registered
	registered, err := registry.Get("test-plugin")
	require.NoError(t, err)
	require.Equal(t, plugin, registered)
}

func TestRegistry_Register_EmptyName(t *testing.T) {
	registry := NewRegistry()
	plugin := NewMockPlugin("", "1.0.0", "Test")
	
	err := registry.Register(plugin, PluginMetadata{Type: PluginTypeLifecycle})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be empty")
}

func TestRegistry_Register_Duplicate(t *testing.T) {
	registry := NewRegistry()
	plugin1 := NewMockPlugin("test", "1.0.0", "Test")
	plugin2 := NewMockPlugin("test", "2.0.0", "Test 2")
	
	metadata := PluginMetadata{
		Name: "test",
		Type: PluginTypeLifecycle,
	}
	
	err := registry.Register(plugin1, metadata)
	require.NoError(t, err)
	
	err = registry.Register(plugin2, metadata)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already registered")
}

func TestRegistry_Get(t *testing.T) {
	registry := NewRegistry()
	plugin := NewMockPlugin("test", "1.0.0", "Test")
	
	err := registry.Register(plugin, PluginMetadata{
		Name: "test",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)
	
	retrieved, err := registry.Get("test")
	require.NoError(t, err)
	require.Equal(t, plugin, retrieved)
}

func TestRegistry_Get_NotFound(t *testing.T) {
	registry := NewRegistry()
	
	_, err := registry.Get("nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestRegistry_GetMetadata(t *testing.T) {
	registry := NewRegistry()
	plugin := NewMockPlugin("test", "1.0.0", "Test")
	
	metadata := PluginMetadata{
		Name:        "test",
		Version:     "1.0.0",
		Type:        PluginTypeLifecycle,
		Description: "Test plugin",
		Author:      "Test Author",
		License:     "MIT",
		Repository:  "https://github.com/test/test",
	}
	
	err := registry.Register(plugin, metadata)
	require.NoError(t, err)
	
	retrieved, err := registry.GetMetadata("test")
	require.NoError(t, err)
	require.Equal(t, metadata, retrieved)
}

func TestRegistry_List(t *testing.T) {
	registry := NewRegistry()
	
	plugins := []*MockPlugin{
		NewMockPlugin("plugin1", "1.0.0", "Plugin 1"),
		NewMockPlugin("plugin2", "1.0.0", "Plugin 2"),
		NewMockPlugin("plugin3", "1.0.0", "Plugin 3"),
	}
	
	for _, p := range plugins {
		err := registry.Register(p, PluginMetadata{
			Name: p.Name(),
			Type: PluginTypeLifecycle,
		})
		require.NoError(t, err)
	}
	
	names := registry.List()
	require.Len(t, names, 3)
	require.Contains(t, names, "plugin1")
	require.Contains(t, names, "plugin2")
	require.Contains(t, names, "plugin3")
}

func TestRegistry_ListByType(t *testing.T) {
	registry := NewRegistry()
	
	// Register different plugin types
	err := registry.Register(
		NewMockPlugin("embedder1", "1.0.0", "Embedder 1"),
		PluginMetadata{Name: "embedder1", Type: PluginTypeEmbedding},
	)
	require.NoError(t, err)
	
	err = registry.Register(
		NewMockPlugin("embedder2", "1.0.0", "Embedder 2"),
		PluginMetadata{Name: "embedder2", Type: PluginTypeEmbedding},
	)
	require.NoError(t, err)
	
	err = registry.Register(
		NewMockPlugin("lifecycle1", "1.0.0", "Lifecycle 1"),
		PluginMetadata{Name: "lifecycle1", Type: PluginTypeLifecycle},
	)
	require.NoError(t, err)
	
	// Test ListByType
	embedders := registry.ListByType(PluginTypeEmbedding)
	require.Len(t, embedders, 2)
	require.Contains(t, embedders, "embedder1")
	require.Contains(t, embedders, "embedder2")
	
	lifecycle := registry.ListByType(PluginTypeLifecycle)
	require.Len(t, lifecycle, 1)
	require.Contains(t, lifecycle, "lifecycle1")
}

func TestRegistry_Count(t *testing.T) {
	registry := NewRegistry()
	require.Equal(t, 0, registry.Count())
	
	err := registry.Register(
		NewMockPlugin("test1", "1.0.0", "Test 1"),
		PluginMetadata{Name: "test1", Type: PluginTypeLifecycle},
	)
	require.NoError(t, err)
	require.Equal(t, 1, registry.Count())
	
	err = registry.Register(
		NewMockPlugin("test2", "1.0.0", "Test 2"),
		PluginMetadata{Name: "test2", Type: PluginTypeLifecycle},
	)
	require.NoError(t, err)
	require.Equal(t, 2, registry.Count())
}

func TestRegistry_CountByType(t *testing.T) {
	registry := NewRegistry()
	
	err := registry.Register(
		NewMockPlugin("embedder1", "1.0.0", "Embedder 1"),
		PluginMetadata{Name: "embedder1", Type: PluginTypeEmbedding},
	)
	require.NoError(t, err)
	
	err = registry.Register(
		NewMockPlugin("embedder2", "1.0.0", "Embedder 2"),
		PluginMetadata{Name: "embedder2", Type: PluginTypeEmbedding},
	)
	require.NoError(t, err)
	
	err = registry.Register(
		NewMockPlugin("lifecycle1", "1.0.0", "Lifecycle 1"),
		PluginMetadata{Name: "lifecycle1", Type: PluginTypeLifecycle},
	)
	require.NoError(t, err)
	
	require.Equal(t, 2, registry.CountByType(PluginTypeEmbedding))
	require.Equal(t, 1, registry.CountByType(PluginTypeLifecycle))
	require.Equal(t, 0, registry.CountByType(PluginTypeStorage))
}

func TestRegistry_InitializeAll(t *testing.T) {
	registry := NewRegistry()
	
	plugin1 := NewMockPlugin("test1", "1.0.0", "Test 1")
	plugin2 := NewMockPlugin("test2", "1.0.0", "Test 2")
	
	err := registry.Register(plugin1, PluginMetadata{
		Name: "test1",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)
	
	err = registry.Register(plugin2, PluginMetadata{
		Name: "test2",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)
	
	config := map[string]any{"key": "value"}
	err = registry.InitializeAll(context.Background(), config)
	require.NoError(t, err)
	
	require.True(t, plugin1.initCalled)
	require.True(t, plugin2.initCalled)
}

func TestRegistry_ShutdownAll(t *testing.T) {
	registry := NewRegistry()
	
	plugin1 := NewMockPlugin("test1", "1.0.0", "Test 1")
	plugin2 := NewMockPlugin("test2", "1.0.0", "Test 2")
	
	err := registry.Register(plugin1, PluginMetadata{
		Name: "test1",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)
	
	err = registry.Register(plugin2, PluginMetadata{
		Name: "test2",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)
	
	err = registry.ShutdownAll(context.Background())
	require.NoError(t, err)
	
	require.True(t, plugin1.shutdownCalled)
	require.True(t, plugin2.shutdownCalled)
}

func TestRegistry_Unregister(t *testing.T) {
	registry := NewRegistry()
	plugin := NewMockPlugin("test", "1.0.0", "Test")
	
	err := registry.Register(plugin, PluginMetadata{
		Name: "test",
		Type: PluginTypeLifecycle,
	})
	require.NoError(t, err)
	require.Equal(t, 1, registry.Count())
	
	err = registry.Unregister("test")
	require.NoError(t, err)
	require.Equal(t, 0, registry.Count())
	require.True(t, plugin.shutdownCalled, "Shutdown should be called on unregister")
	
	// Verify plugin is gone
	_, err = registry.Get("test")
	require.Error(t, err)
}

func TestRegistry_Unregister_NotFound(t *testing.T) {
	registry := NewRegistry()
	
	err := registry.Unregister("nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestRegistry_Concurrent(t *testing.T) {
	registry := NewRegistry()
	
	// Test concurrent registration
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(n int) {
			plugin := NewMockPlugin(
				"plugin-"+string(rune('0'+n)),
				"1.0.0",
				"Test plugin",
			)
			err := registry.Register(plugin, PluginMetadata{
				Name: plugin.Name(),
				Type: PluginTypeLifecycle,
			})
			require.NoError(t, err)
			done <- true
		}(i)
	}
	
	for i := 0; i < 10; i++ {
		<-done
	}
	
	require.Equal(t, 10, registry.Count())
}

func TestPluginType_Constants(t *testing.T) {
	require.Equal(t, PluginType("embedding"), PluginTypeEmbedding)
	require.Equal(t, PluginType("storage"), PluginTypeStorage)
	require.Equal(t, PluginType("lifecycle"), PluginTypeLifecycle)
	require.Equal(t, PluginType("middleware"), PluginTypeMiddleware)
	require.Equal(t, PluginType("extension"), PluginTypeExtension)
}
