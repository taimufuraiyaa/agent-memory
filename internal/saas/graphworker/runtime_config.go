package graphworker

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type RuntimeConfig struct {
	Enabled              bool
	QueueURL             string
	ObjectEndpoint       string
	ObjectAccessKey      string
	ObjectSecretKey      string
	AdapterExecutable    string
	JobRoot              string
	CompletionProvider   string
	CompletionModel      string
	EmbeddingProvider    string
	EmbeddingModel       string
	CompletionAPIKey     string
	EmbeddingAPIKey      string
	BuildDigest          string
	AttestationSignature string
	BundlePublicKey      string
	TelemetryAddr        string
	WorkerIdentity       string
	Lease                time.Duration
	AdapterTimeout       time.Duration
	PollInterval         time.Duration
}

func LoadRuntimeConfig() (RuntimeConfig, error) {
	enabled, err := strconv.ParseBool(envGraph("AGENT_MEMORY_GRAPHRAG_ENABLED", "false"))
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("AGENT_MEMORY_GRAPHRAG_ENABLED must be true or false")
	}
	configuration := RuntimeConfig{
		Enabled:  enabled,
		QueueURL: strings.TrimSpace(os.Getenv("AGENT_MEMORY_QUEUE_URL")), ObjectEndpoint: strings.TrimSpace(os.Getenv("AGENT_MEMORY_OBJECT_ENDPOINT")),
		ObjectAccessKey: strings.TrimSpace(os.Getenv("AGENT_MEMORY_OBJECT_ACCESS_KEY")), ObjectSecretKey: strings.TrimSpace(os.Getenv("AGENT_MEMORY_OBJECT_SECRET_KEY")),
		AdapterExecutable: envGraph("AGENT_MEMORY_GRAPHRAG_EXECUTABLE", "/opt/adapter/.venv/bin/agent-memory-graphrag"), JobRoot: envGraph("AGENT_MEMORY_GRAPHRAG_JOB_ROOT", "/graph-job"),
		CompletionProvider: envGraph("AGENT_MEMORY_GRAPH_COMPLETION_PROVIDER", "openai"), CompletionModel: strings.TrimSpace(os.Getenv("AGENT_MEMORY_GRAPH_COMPLETION_MODEL")),
		EmbeddingProvider: envGraph("AGENT_MEMORY_GRAPH_EMBEDDING_PROVIDER", "openai"), EmbeddingModel: strings.TrimSpace(os.Getenv("AGENT_MEMORY_GRAPH_EMBEDDING_MODEL")),
		CompletionAPIKey: strings.TrimSpace(os.Getenv("INDEX_COMPLETION_API_KEY")), EmbeddingAPIKey: strings.TrimSpace(os.Getenv("INDEX_EMBEDDING_API_KEY")),
		BuildDigest: strings.TrimSpace(os.Getenv("AGENT_MEMORY_GRAPHRAG_BUILD_DIGEST")), AttestationSignature: strings.TrimSpace(os.Getenv("AGENT_MEMORY_GRAPHRAG_ATTESTATION_SIGNATURE")),
		BundlePublicKey: strings.TrimSpace(os.Getenv("AGENT_MEMORY_GRAPH_BUNDLE_PUBLIC_KEY")),
		TelemetryAddr:   envGraph("AGENT_MEMORY_TELEMETRY_LISTEN_ADDR", ":9090"), WorkerIdentity: envGraph("AGENT_MEMORY_GRAPH_WORKER_ID", "graph-worker"),
		Lease: 6 * time.Hour, AdapterTimeout: 4 * time.Hour, PollInterval: time.Second,
	}
	for name, destination := range map[string]*time.Duration{
		"AGENT_MEMORY_GRAPH_JOB_LEASE": &configuration.Lease, "AGENT_MEMORY_GRAPH_ADAPTER_TIMEOUT": &configuration.AdapterTimeout, "AGENT_MEMORY_GRAPH_POLL_INTERVAL": &configuration.PollInterval,
	} {
		if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil {
				return RuntimeConfig{}, fmt.Errorf("%s is invalid", name)
			}
			*destination = value
		}
	}
	if err := configuration.Validate(); err != nil {
		return RuntimeConfig{}, err
	}
	return configuration, nil
}

func (c RuntimeConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	for name, value := range map[string]string{
		"queue URL": c.QueueURL, "object endpoint": c.ObjectEndpoint, "object access key": c.ObjectAccessKey, "object secret key": c.ObjectSecretKey,
		"adapter executable": c.AdapterExecutable, "job root": c.JobRoot, "completion provider": c.CompletionProvider, "completion model": c.CompletionModel,
		"embedding provider": c.EmbeddingProvider, "embedding model": c.EmbeddingModel, "completion API key": c.CompletionAPIKey, "embedding API key": c.EmbeddingAPIKey,
		"build digest": c.BuildDigest, "attestation signature": c.AttestationSignature, "worker identity": c.WorkerIdentity,
		"bundle public key": c.BundlePublicKey,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("graph worker %s is required", name)
		}
	}
	if !filepath.IsAbs(c.AdapterExecutable) || !filepath.IsAbs(c.JobRoot) || !strings.HasPrefix(c.TelemetryAddr, ":") || c.Lease < time.Minute || c.Lease > 24*time.Hour || c.AdapterTimeout < time.Minute || c.AdapterTimeout > c.Lease || c.PollInterval < 100*time.Millisecond || c.PollInterval > time.Minute {
		return fmt.Errorf("graph worker paths, listener, or time bounds are invalid")
	}
	return nil
}

func envGraph(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
