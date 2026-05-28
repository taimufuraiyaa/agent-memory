package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/time/timebooks/agent-memory/internal/core"
	"github.com/time/timebooks/agent-memory/internal/embeddings"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

type reembedWorkspaceResult struct {
	Workspace     string         `json:"workspace"`
	DBPath        string         `json:"db_path"`
	TotalMemories int            `json:"total_memories"`
	ReEmbedded    int            `json:"re_embedded"`
	Skipped       int            `json:"skipped"`
	SkipReasons   map[string]int `json:"skip_reasons,omitempty"`
	Provider      string         `json:"provider"`
}

type reembedResult struct {
	Provider       string                   `json:"provider"`
	WorkspaceCount int                      `json:"workspace_count"`
	TotalMemories  int                      `json:"total_memories"`
	ReEmbedded     int                      `json:"re_embedded"`
	Skipped        int                      `json:"skipped"`
	Workspaces     []reembedWorkspaceResult `json:"workspaces"`
}

func newReembedCommand() *cobra.Command {
	var flags commonFlags
	var all bool
	cmd := &cobra.Command{
		Use:   "re-embed",
		Short: "Rebuild stored memory vectors for one workspace or all workspaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if err := validateOutputFormat(flags.format, false); err != nil {
				return err
			}
			modelDir := strings.TrimSpace(flags.modelDir)
			if modelDir == "" {
				home, _ := os.UserHomeDir()
				modelDir = embeddings.DefaultModelDir(home)
			}
			provider, err := embeddings.NewProvider(modelDir)
			if err != nil {
				return err
			}

			targets, err := reembedTargets(flags, all)
			if err != nil {
				return err
			}

			result := reembedResult{
				Provider:   provider.Name(),
				Workspaces: make([]reembedWorkspaceResult, 0, len(targets)),
			}
			for _, target := range targets {
				item, err := runReembedWorkspace(ctx, target.workspace, target.dbPath, provider)
				if err != nil {
					return err
				}
				result.Workspaces = append(result.Workspaces, item)
				result.TotalMemories += item.TotalMemories
				result.ReEmbedded += item.ReEmbedded
				result.Skipped += item.Skipped
			}
			result.WorkspaceCount = len(result.Workspaces)
			return writeSuccessEnvelope(cmd.OutOrStdout(), "re-embed", result)
		},
	}

	cmd.Flags().StringVar(&flags.dbPath, "db", "", "Path to SQLite database file")
	cmd.Flags().StringVarP(&flags.workspace, "workspace", "w", "", "Workspace name")
	home, _ := os.UserHomeDir()
	cmd.Flags().StringVar(&flags.modelDir, "model-dir", embeddings.DefaultModelDir(home), "Path to local embedding model directory")
	cmd.Flags().StringVarP(&flags.format, "format", "f", formatJSON, "Output format: json")
	cmd.Flags().BoolVar(&all, "all", false, "Re-embed every workspace database under ~/.agent-memory")
	return cmd
}

type reembedTarget struct {
	workspace string
	dbPath    string
}

func reembedTargets(flags commonFlags, all bool) ([]reembedTarget, error) {
	if all {
		if strings.TrimSpace(flags.dbPath) != "" || strings.TrimSpace(flags.workspace) != "" {
			return nil, errors.New("--all cannot be combined with --db or --workspace")
		}
		baseDir, err := defaultDBBaseDir()
		if err != nil {
			return nil, err
		}
		matches, err := filepath.Glob(filepath.Join(baseDir, "*.db"))
		if err != nil {
			return nil, err
		}
		out := make([]reembedTarget, 0, len(matches))
		for _, match := range matches {
			name := strings.TrimSuffix(filepath.Base(match), filepath.Ext(match))
			if strings.HasPrefix(name, ".") || name == "" {
				continue
			}
			out = append(out, reembedTarget{workspace: name, dbPath: match})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].workspace < out[j].workspace })
		if len(out) == 0 {
			return nil, errors.New("no workspace databases found for --all")
		}
		return out, nil
	}

	cfg, err := resolveRuntime(flags)
	if err != nil {
		return nil, err
	}
	return []reembedTarget{{
		workspace: cfg.workspace,
		dbPath:    cfg.dbPath,
	}}, nil
}

func runReembedWorkspace(ctx context.Context, workspace, dbPath string, provider embeddings.Provider) (reembedWorkspaceResult, error) {
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return reembedWorkspaceResult{}, err
	}
	defer func() { _ = store.Close() }()

	memories, err := store.ListMemoriesByWorkspace(ctx, workspace)
	if err != nil {
		return reembedWorkspaceResult{}, err
	}

	result := reembedWorkspaceResult{
		Workspace:     workspace,
		DBPath:        dbPath,
		TotalMemories: len(memories),
		SkipReasons:   map[string]int{},
		Provider:      provider.Name(),
	}
	for _, memory := range memories {
		text := memoryVectorText(memory)
		if strings.TrimSpace(text) == "" {
			result.Skipped++
			result.SkipReasons["empty_content"]++
			continue
		}
		vec, err := provider.Embed(ctx, text)
		if err != nil {
			result.Skipped++
			result.SkipReasons["embed_error"]++
			continue
		}
		if err := store.UpsertMemoryVector(ctx, memory.ID, memory.Workspace, provider.Name(), vec); err != nil {
			return reembedWorkspaceResult{}, err
		}
		result.ReEmbedded++
	}
	if len(result.SkipReasons) == 0 {
		result.SkipReasons = nil
	}
	return result, nil
}

func memoryVectorText(memory core.MemoryEntry) string {
	text := strings.TrimSpace(memory.Content)
	if memory.Diagram == nil || strings.TrimSpace(memory.Diagram.Code) == "" {
		return text
	}
	if text == "" {
		return strings.TrimSpace(memory.Diagram.Code)
	}
	return text + "\n" + strings.TrimSpace(memory.Diagram.Code)
}
