package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/localoidc"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	provider, err := localoidc.New(localoidc.Config{
		Issuer:      os.Getenv("AGENT_MEMORY_LOCAL_OIDC_ISSUER"),
		Audience:    os.Getenv("AGENT_MEMORY_LOCAL_OIDC_AUDIENCE"),
		Subject:     os.Getenv("AGENT_MEMORY_LOCAL_OIDC_SUBJECT"),
		Email:       os.Getenv("AGENT_MEMORY_LOCAL_OIDC_EMAIL"),
		DisplayName: os.Getenv("AGENT_MEMORY_LOCAL_OIDC_DISPLAY_NAME"),
	})
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              envOr("AGENT_MEMORY_LOCAL_OIDC_LISTEN_ADDR", ":8082"),
		Handler:           provider.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
