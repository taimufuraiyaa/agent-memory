package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/engine"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

type benchmarkWorkerResponse struct {
	OK    bool           `json:"ok"`
	Data  map[string]any `json:"data,omitempty"`
	Error string         `json:"error,omitempty"`
}

func newBenchmarkWorkerCommand() *cobra.Command {
	var flags commonFlags
	cmd := &cobra.Command{
		Use:    "benchmark-worker",
		Short:  "Persistent benchmark worker for internal benchmark execution",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cfg, err := resolveRuntime(flags)
			if err != nil {
				return err
			}
			if cfg.apiURL != "" {
				return errors.New("benchmark-worker only supports in-process execution")
			}
			if err := validateOutputFormat(flags.format, false); err != nil {
				return err
			}

			memoryEnabled := engine.MemoryEnabled()
			var store *sqlite.Store
			var provider embeddings.Provider
			if memoryEnabled {
				store, provider, err = openDeps(ctx, cfg)
			} else {
				store, err = openStore(ctx, cfg)
			}
			if err != nil {
				return err
			}
			defer func() { _ = store.Close() }()

			scanner := bufio.NewScanner(cmd.InOrStdin())
			scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetEscapeHTML(false)

			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				var req recallRequest
				if err := json.Unmarshal([]byte(line), &req); err != nil {
					if encodeErr := encoder.Encode(benchmarkWorkerResponse{OK: false, Error: fmt.Sprintf("invalid request: %v", err)}); encodeErr != nil {
						return encodeErr
					}
					continue
				}
				payload, err := executeRecallWithRetry(ctx, cfg, store, provider, memoryEnabled, req)
				if err != nil {
					if encodeErr := encoder.Encode(benchmarkWorkerResponse{OK: false, Error: err.Error()}); encodeErr != nil {
						return encodeErr
					}
					continue
				}
				if err := encoder.Encode(benchmarkWorkerResponse{OK: true, Data: payload}); err != nil {
					return err
				}
			}
			return scanner.Err()
		},
	}
	addCommonFlags(cmd, &flags)
	return cmd
}

func executeRecallWithRetry(
	ctx context.Context,
	cfg runtimeConfig,
	store *sqlite.Store,
	provider embeddings.Provider,
	memoryEnabled bool,
	req recallRequest,
) (map[string]any, error) {
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		payload, _, err := executeRecall(ctx, cfg, store, provider, memoryEnabled, req)
		if err == nil {
			return payload, nil
		}
		lastErr = err
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "database is locked") || strings.Contains(lower, "sqlite_busy") {
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("recall failed after sqlite retry budget: %w", lastErr)
}
