package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"

	memorytui "github.com/taimufuraiyaa/agent-memory/internal/tui"
)

func tuiHistoryDirectory(config runtimeConfig) string {
	base := filepath.Join(filepath.Dir(config.dbPath), "tui-history")
	if config.apiURL != "" {
		digest := sha256.Sum256([]byte(config.apiURL))
		base = filepath.Join(defaultAgentMemoryDataDir(), "tui-history", fmt.Sprintf("api-%x", digest[:8]))
	}
	return base
}

func tuiModelWithHistory(ctx context.Context, config runtimeConfig, backend memorytui.Backend, credentials memorytui.JevCredentialStore, directory func(runtimeConfig) string) (memorytui.Model, error) {
	if directory != nil {
		var err error
		backend, err = memorytui.WithHistory(backend, config.workspace, directory(config))
		if err != nil {
			return memorytui.Model{}, err
		}
	}
	return memorytui.NewModelWithCredentials(ctx, config.workspace, backend, credentials), nil
}
