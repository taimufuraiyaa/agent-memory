package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/edge"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	upstream, err := url.Parse(strings.TrimSpace(os.Getenv("AGENT_MEMORY_EDGE_UPSTREAM")))
	if err != nil {
		return errors.New("AGENT_MEMORY_EDGE_UPSTREAM is invalid")
	}
	handler, err := edge.New(edge.Config{
		Upstream:       upstream,
		CountrySecret:  os.Getenv("AGENT_MEMORY_EDGE_COUNTRY_SECRET"),
		DefaultCountry: envOr("AGENT_MEMORY_EDGE_DEFAULT_COUNTRY", "VN"),
	})
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              envOr("AGENT_MEMORY_EDGE_LISTEN_ADDR", ":8081"),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
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
