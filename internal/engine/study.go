package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/time/timebooks/agent-memory/internal/core"
)

type StudyResult struct {
	SourcesScanned int      `json:"sources_scanned"`
	ScannedFiles   int      `json:"scanned_files"`
	Extracted      int      `json:"extracted"`
	WrittenIDs     []string `json:"written_ids,omitempty"`
	DryRun         bool     `json:"dry_run"`
}

type StudyOptions struct {
	Workspace string
	Sources   []string
	Depth     string
	DryRun    bool
	MaxFiles  int
	Ignore    []string
}

type StudyEngine struct {
	pipeline *WritePipeline
}

func NewStudyEngine(pipeline *WritePipeline) *StudyEngine { return &StudyEngine{pipeline: pipeline} }

var errStudyMaxFiles = errors.New("study max files reached")

func (s *StudyEngine) Ingest(ctx context.Context, workspace, root string, dryRun bool) (*StudyResult, error) {
	return s.IngestWithOptions(ctx, StudyOptions{
		Workspace: workspace,
		Sources:   []string{root},
		Depth:     "medium",
		DryRun:    dryRun,
		MaxFiles:  0,
	})
}

func (s *StudyEngine) IngestWithOptions(ctx context.Context, opts StudyOptions) (*StudyResult, error) {
	res := &StudyResult{DryRun: opts.DryRun, WrittenIDs: []string{}}
	if len(opts.Sources) == 0 {
		opts.Sources = []string{"."}
	}
	for _, source := range opts.Sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		res.SourcesScanned++
		info, err := os.Stat(source)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if err := s.processFile(ctx, opts, source, res); err != nil && !errors.Is(err, errStudyMaxFiles) {
				return res, err
			}
			if opts.MaxFiles > 0 && res.ScannedFiles >= opts.MaxFiles {
				break
			}
			continue
		}
		err = filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if shouldIgnoreStudyPath(path, d, opts.Ignore) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			return s.processFile(ctx, opts, path, res)
		})
		if errors.Is(err, errStudyMaxFiles) {
			break
		}
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

func shouldIgnoreStudyPath(path string, d os.DirEntry, ignore []string) bool {
	name := d.Name()
	if d.IsDir() && (name == ".git" || name == "node_modules" || name == "bin" || strings.HasPrefix(name, ".")) {
		return true
	}
	if len(ignore) == 0 {
		return false
	}
	slashed := filepath.ToSlash(path)
	for _, pattern := range ignore {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
		if ok, _ := filepath.Match(pattern, slashed); ok {
			return true
		}
	}
	return false
}

func (s *StudyEngine) processFile(ctx context.Context, opts StudyOptions, path string, res *StudyResult) error {
	if opts.MaxFiles > 0 && res.ScannedFiles >= opts.MaxFiles {
		return errStudyMaxFiles
	}
	if !isStudyFile(path) {
		return nil
	}
	res.ScannedFiles++
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	text := summarizeForStudy(string(content), opts.Depth)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	res.Extracted++
	if opts.DryRun {
		return nil
	}
	out, err := s.pipeline.Write(ctx, WriteInput{
		Workspace: opts.Workspace,
		Type:      core.SemanticMemory,
		Content:   text,
		Source:    core.MemorySource{Type: core.SourceCodeAnalysis, FilePath: path},
		Mode:      ExtractFast,
	})
	if err == nil && !out.Rejected {
		res.WrittenIDs = append(res.WrittenIDs, out.ID)
	}
	return nil
}

func isStudyFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".txt", ".go", ".ts", ".tsx", ".js", ".json", ".yaml", ".yml":
		return true
	default:
		return false
	}
}

func summarizeForStudy(s, depth string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	max := 20
	switch strings.ToLower(strings.TrimSpace(depth)) {
	case "shallow":
		max = 8
	case "deep":
		max = 60
	}
	if len(lines) < max {
		max = len(lines)
	}
	return strings.TrimSpace(strings.Join(lines[:max], " "))
}
