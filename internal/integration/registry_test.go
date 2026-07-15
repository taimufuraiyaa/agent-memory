package integration

import (
	"context"
	"testing"
)

type fakeAdapter struct{ name string }

func (f fakeAdapter) Name() string                                  { return f.name }
func (f fakeAdapter) Detect(context.Context, Options) (bool, error) { return true, nil }
func (f fakeAdapter) Plan(context.Context, Options) (Result, error) {
	return Result{Agent: f.name}, nil
}
func (f fakeAdapter) Connect(context.Context, Options) (Result, error) {
	return Result{Agent: f.name, Applied: []string{"managed"}}, nil
}
func (f fakeAdapter) Disconnect(context.Context, Options) (Result, error) {
	return Result{Agent: f.name, Removed: []string{"managed"}}, nil
}
func (f fakeAdapter) Verify(context.Context, Options) (Result, error) {
	return Result{Agent: f.name, Verified: true}, nil
}

func TestRegistryRejectsDuplicateAndUnknownAdapters(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(fakeAdapter{name: "codex"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := registry.Register(fakeAdapter{name: "codex"}); err == nil {
		t.Fatal("expected duplicate registration error")
	}
	if _, err := registry.Adapter("unknown"); err == nil {
		t.Fatal("expected unknown adapter error")
	}
}

func TestRegistryRunsStructuredAdapterOperations(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(fakeAdapter{name: "codex"})
	result, err := registry.Connect(context.Background(), "CODEX", Options{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if result.Agent != "codex" || len(result.Applied) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}
