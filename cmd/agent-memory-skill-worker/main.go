package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/skillworker"
)

type hostedSkillRuntime interface {
	Run(context.Context) error
	Drain(context.Context) error
	Live() bool
	Ready() bool
}

var buildHostedSkillRuntime = func(context.Context, skillworker.RuntimeConfig) (hostedSkillRuntime, func(), error) {
	return nil, func() {}, errors.New("hosted skill stage providers are not linked into this process")
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	configuration, err := skillworker.LoadRuntimeConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "skill-worker")
	var live, ready atomic.Bool
	live.Store(true)
	server := skillHealthServer(configuration.TelemetryAddress, &live, &ready)
	go func() {
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			logger.Error("health server failed", "error", serveErr)
			stop()
		}
	}()
	defer func() {
		live.Store(false)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if !configuration.Enabled {
		logger.Info("skill worker disabled")
		<-ctx.Done()
		return nil
	}
	runtime, cleanup, err := buildHostedSkillRuntime(ctx, configuration)
	if err != nil {
		return err
	}
	defer cleanup()
	finished := make(chan error, 1)
	go func() { finished <- runtime.Run(ctx) }()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			ready.Store(false)
			drainCtx, cancel := context.WithTimeout(context.Background(), configuration.DrainTimeout)
			defer cancel()
			if err := runtime.Drain(drainCtx); err != nil {
				return err
			}
			return <-finished
		case err := <-finished:
			ready.Store(false)
			return err
		case <-ticker.C:
			live.Store(runtime.Live())
			ready.Store(runtime.Ready())
		}
	}
}

func skillHealthServer(address string, live, ready *atomic.Bool) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health/live", func(response http.ResponseWriter, _ *http.Request) {
		if !live.Load() {
			http.Error(response, "not live", http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/health/ready", func(response http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(response, "not ready", http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusOK)
	})
	return &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
}
