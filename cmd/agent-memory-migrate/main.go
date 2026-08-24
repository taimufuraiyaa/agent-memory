package main

import (
	"context"
	"fmt"
	"os"

	"github.com/taimufuraiyaa/agent-memory/internal/saas/config"
	"github.com/taimufuraiyaa/agent-memory/internal/saas/postgres"
)

func main() {
	cfg, err := config.LoadFor(config.Migration)
	if err == nil {
		ctx := context.Background()
		pool, openErr := postgres.Open(ctx, cfg.PostgresURL)
		if openErr != nil {
			err = openErr
		} else {
			defer pool.Close()
			err = postgres.Apply(ctx, pool)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
