package graphworker

import (
	"testing"
	"time"
)

func TestGraphWorkerRuntimeConfigurationKeepsDatabaseOutOfCapabilitySet(t *testing.T) {
	configuration := RuntimeConfig{Enabled: true, QueueURL: "nats://queue:4222", ObjectEndpoint: "https://objects.internal", ObjectAccessKey: "worker", ObjectSecretKey: "secret", AdapterExecutable: "/opt/adapter/bin/adapter", JobRoot: "/graph-job", CompletionProvider: "openai", CompletionModel: "completion-v1", EmbeddingProvider: "openai", EmbeddingModel: "embedding-v1", CompletionAPIKey: "a", EmbeddingAPIKey: "b", BuildDigest: "sha256:build", AttestationSignature: "signature", BundlePublicKey: "base64-public-key", TelemetryAddr: ":9090", WorkerIdentity: "worker-a", Lease: 6 * time.Hour, AdapterTimeout: 4 * time.Hour, PollInterval: time.Second}
	if err := configuration.Validate(); err != nil {
		t.Fatal(err)
	}
	// RuntimeConfig deliberately has no PostgreSQL URL or database credential.
}

func TestDisabledGraphWorkerNeedsNoExternalCredentials(t *testing.T) {
	if err := (RuntimeConfig{}).Validate(); err != nil {
		t.Fatal(err)
	}
}
