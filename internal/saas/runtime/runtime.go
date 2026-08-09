// Package runtime provides the minimal lifecycle shared by hosted services.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	saasconfig "github.com/taimufuraiyaa/agent-memory/internal/saas/config"
)

func ProbeHandler(service string, ready func() bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeProbe(w, http.StatusOK, service, "ok")
	})
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, _ *http.Request) {
		if !ready() {
			writeProbe(w, http.StatusServiceUnavailable, service, "unavailable")
			return
		}
		writeProbe(w, http.StatusOK, service, "ok")
	})
	return mux
}

func Run(cfg saasconfig.Config) error {
	return RunHTTP(cfg, nil)
}

// RunHTTP runs a hosted service and permits the API binary to supply its
// composed domain handler. Non-API services ignore the handler.
func RunHTTP(cfg saasconfig.Config, handler http.Handler) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With(
		"service", cfg.Service,
		"environment", cfg.Environment,
	)
	logger.Info("service starting", "config", cfg.Summary())

	if cfg.Service == saasconfig.Migration {
		logger.Info("migration configuration validated")
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if cfg.Service == saasconfig.API {
		if handler == nil {
			handler = ProbeHandler(string(cfg.Service), func() bool { return true })
		}
		return serveHTTP(ctx, cfg, logger, handler)
	}

	<-ctx.Done()
	logger.Info("service stopped")
	return nil
}

func serveHTTP(ctx context.Context, cfg saasconfig.Config, logger *slog.Logger, handler http.Handler) error {
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ShutdownTimeout,
	}
	errorChannel := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "address", cfg.ListenAddr)
		errorChannel <- server.ListenAndServe()
	}()

	select {
	case err := <-errorChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve hosted API: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shutdown hosted API: %w", err)
		}
		logger.Info("service stopped")
		return nil
	}
}

func writeProbe(w http.ResponseWriter, status int, service, state string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"service": service, "status": state})
}
