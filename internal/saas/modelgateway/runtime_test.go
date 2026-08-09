package modelgateway

import "testing"

func TestNewRuntimeProviderSeparatesDevelopmentAndManagedConfiguration(t *testing.T) {
	local, err := NewRuntimeProvider(RuntimeProviderConfig{Name: "local-minilm-scaffold", Retention: "local-only"})
	if err != nil || local.Name() != "local-minilm-scaffold" {
		t.Fatalf("local provider = %#v, %v", local, err)
	}
	remote, err := NewRuntimeProvider(RuntimeProviderConfig{
		Name: RemoteProviderName, Endpoint: "https://models.example.test", APIKey: "secret",
		Model: "private-route-v1", Dimension: 1536, Retention: "zero-retention",
	})
	if err != nil || remote.Name() != RemoteProviderName || remote.Dimension() != 1536 {
		t.Fatalf("remote provider = %#v, %v", remote, err)
	}
	if _, err := NewRuntimeProvider(RuntimeProviderConfig{Name: "arbitrary-provider"}); err == nil {
		t.Fatal("unknown providers must fail closed")
	}
}
