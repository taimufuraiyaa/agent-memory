package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/graphworker"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/objectcustody"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := graphworker.LoadRuntimeConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "graph-worker")
	observer := telemetry.New("graph-worker", logger)
	var ready atomic.Bool
	server := healthServer(configuration.TelemetryAddr, &ready, observer.MetricsHandler())
	go func() {
		if serveErr := server.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("health server failed", "error", serveErr)
			stop()
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if !configuration.Enabled {
		logger.Info("graph worker disabled")
		<-ctx.Done()
		return nil
	}
	transport, err := graphworker.NewNATSTransport(configuration.QueueURL, configuration.WorkerIdentity)
	if err != nil {
		return fmt.Errorf("open graph queue: %w", err)
	}
	defer transport.Close()
	objects, err := objectcustody.NewMinIOGraphObjects(configuration.ObjectEndpoint, configuration.ObjectAccessKey, configuration.ObjectSecretKey)
	if err != nil {
		return fmt.Errorf("open graph object custody: %w", err)
	}
	adapter, err := graphworker.NewProcessAdapter(graphworker.ProcessAdapterConfig{
		Executable: configuration.AdapterExecutable, JobRoot: configuration.JobRoot,
		CompletionProvider: configuration.CompletionProvider, CompletionModel: configuration.CompletionModel,
		EmbeddingProvider: configuration.EmbeddingProvider, EmbeddingModel: configuration.EmbeddingModel,
		CompletionAPIKey: configuration.CompletionAPIKey, EmbeddingAPIKey: configuration.EmbeddingAPIKey,
		ProducerIdentity: configuration.WorkerIdentity, BuildDigest: configuration.BuildDigest,
		AttestationSignature: configuration.AttestationSignature, Timeout: configuration.AdapterTimeout, MaxOutputBytes: 1 << 20,
	})
	if err != nil {
		return err
	}
	publicKey, err := base64.StdEncoding.DecodeString(configuration.BundlePublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("graph bundle public key is invalid")
	}
	custody, err := objectcustody.NewVerifiedGraphArtifactCustody(objects, ed25519.PublicKey(publicKey), time.Now)
	if err != nil {
		return err
	}
	worker, err := graphworker.New(transport, custody, adapter, transport, configuration.WorkerIdentity, configuration.Lease, time.Now, observer.RecordGraph)
	if err != nil {
		return err
	}
	ready.Store(true)
	logger.Info("graph worker ready")
	ticker := time.NewTicker(configuration.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			ready.Store(false)
			return nil
		case <-ticker.C:
			if _, err := worker.RunOnce(ctx, 1); err != nil && ctx.Err() == nil {
				logger.Error("graph worker cycle failed", "error", err)
			}
		}
	}
}

func healthServer(address string, ready *atomic.Bool, metrics http.Handler) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/health/ready", func(response http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(response, "not ready", http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusOK)
	})
	if metrics != nil {
		mux.Handle("/metrics", metrics)
	}
	return &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}
