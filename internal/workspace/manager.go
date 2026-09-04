package workspace

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/application"
	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/embeddings"
	"github.com/taimufuraiyaa/agent-memory/internal/engine"
	"github.com/taimufuraiyaa/agent-memory/internal/storage/sqlite"
)

type Project struct {
	Name          string    `json:"name"`
	DBPath        string    `json:"db_path"`
	WorkspaceRoot string    `json:"workspace_root,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	LastUsedAt    time.Time `json:"last_used_at"`
}

type Registry struct {
	Projects []Project `json:"projects"`
}

type Manager struct {
	BaseDir string
}

var ErrProjectNotRegistered = errors.New("workspace is not registered")

type InitOptions struct {
	CWD         string
	ProjectName string
	Study       bool
	Reuse       bool
	Force       bool
	NoRule      bool
	RulePath    string
	IDEs        []string
}

type InitResult struct {
	Project         string                `json:"project"`
	DBPath          string                `json:"db_path"`
	CursorRule      string                `json:"cursor_rule,omitempty"`
	RuleFiles       []string              `json:"rule_files,omitempty"`
	StudyRun        bool                  `json:"study_run"`
	MemoriesCreated int                   `json:"memories_created,omitempty"`
	TermIndex       *TermIndexSetupResult `json:"term_index,omitempty"`
}

type RenameOptions struct {
	CWD  string
	From string
	To   string
}

type RenameResult struct {
	From string `json:"from"`
	To   string `json:"to"`
	DB   string `json:"db_path"`
}

type ListItem struct {
	Name          string    `json:"name"`
	DBPath        string    `json:"db_path"`
	WorkspaceRoot string    `json:"workspace_root,omitempty"`
	SizeBytes     int64     `json:"size_bytes"`
	MemoryCount   int       `json:"memory_count"`
	LastActivity  time.Time `json:"last_activity"`
}

type DeleteOptions struct {
	ProjectName string
	KeepData    bool
	Yes         bool
}

type DeleteResult struct {
	Project      string `json:"project"`
	ArchivedPath string `json:"archived_path,omitempty"`
}

type ReinstallOptions struct {
	CWD         string
	ProjectName string
	Force       bool
	IDEs        []string
}

type ReinstallResult struct {
	Project    string                 `json:"project"`
	DBPath     string                 `json:"db_path"`
	AgentFiles *WriteAgentFilesResult `json:"agent_files"`
	TermIndex  *TermIndexSetupResult  `json:"term_index,omitempty"`
}

// TermIndexSetupResult is safe lifecycle metadata; it excludes raw terms and bitmap bytes.
type TermIndexSetupResult struct {
	Ready           bool   `json:"ready"`
	RebuildRequired bool   `json:"rebuild_required"`
	RebuildReason   string `json:"rebuild_reason,omitempty"`
	Scanned         int    `json:"scanned"`
	Indexed         int    `json:"indexed"`
	Skipped         int    `json:"skipped"`
	Failed          int    `json:"failed"`
	DistinctTerms   int64  `json:"distinct_terms"`
	Generation      int64  `json:"generation"`
}

func NewManager(baseDir string) (*Manager, error) {
	if strings.TrimSpace(baseDir) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		baseDir = filepath.Join(home, ".agent-memory")
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, err
	}
	return &Manager{BaseDir: baseDir}, nil
}

func ValidateProjectName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	re := regexp.MustCompile(`[^a-z0-9_-]`)
	name = re.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-_")
	if name == "" || len(name) > 64 {
		return "", errors.New("invalid project name")
	}
	if name == "default" || name == "archived" {
		return "", errors.New("project name is reserved")
	}
	return name, nil
}

func FindProjectRoot(start string) string {
	dir := start
	for i := 0; i < 12; i++ {
		if dirExists(filepath.Join(dir, ".git")) ||
			dirExists(filepath.Join(dir, ".cursor")) ||
			dirExists(filepath.Join(dir, ".kiro")) ||
			dirExists(filepath.Join(dir, ".agents")) ||
			dirExists(filepath.Join(dir, ".trae")) ||
			fileExists(filepath.Join(dir, ".cursorrules")) ||
			fileExists(filepath.Join(dir, ".aierules")) ||
			fileExists(filepath.Join(dir, ".windsurfrules")) ||
			fileExists(filepath.Join(dir, "CLAUDE.md")) ||
			fileExists(filepath.Join(dir, "AGENTS.md")) {
			return dir
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return start
}

func detectWorkspaceFromCWD(start string) (workspace string, root string, ok bool) {
	dir := start
	for i := 0; i < 12; i++ {
		candidates := []string{
			filepath.Join(dir, ".cursor", "rules", "agent-memory.mdc"),
			filepath.Join(dir, ".agents", "rules", "agent-memory.md"),
			filepath.Join(dir, ".trae", "rules", "project_rules.md"),
			filepath.Join(dir, ".cursorrules"),
			filepath.Join(dir, ".aierules"),
			filepath.Join(dir, ".windsurfrules"),
			filepath.Join(dir, "CLAUDE.md"),
			filepath.Join(dir, "AGENTS.md"),
		}
		for _, p := range candidates {
			if !fileExists(p) {
				continue
			}
			ws, err := readWorkspaceFromRule(p)
			if err == nil && strings.TrimSpace(ws) != "" {
				return strings.TrimSpace(ws), dir, true
			}
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
		dir = next
	}
	return "", "", false
}

func (m *Manager) Reinstall(ctx context.Context, opt ReinstallOptions) (*ReinstallResult, error) {
	root := FindProjectRoot(opt.CWD)
	name := strings.TrimSpace(opt.ProjectName)
	if name == "" {
		if detected, detectedRoot, ok := detectWorkspaceFromCWD(opt.CWD); ok {
			name = detected
			root = detectedRoot
		}
	}
	if name == "" {
		return nil, errors.New("project name not found (run from project root or pass --project-name)")
	}
	name, err := ValidateProjectName(name)
	if err != nil {
		return nil, err
	}

	v, err := m.withRegistryLock(func(reg *Registry) (any, error) {
		p := findProject(reg, name)
		if p == nil {
			return nil, fmt.Errorf("%w: project not found in registry (run init first)", core.ErrNotFound)
		}
		if !fileExists(p.DBPath) {
			return nil, fmt.Errorf("db file missing: %s", p.DBPath)
		}
		termIndex, err := prepareTermIndexPath(ctx, p.DBPath, name)
		if err != nil {
			return nil, fmt.Errorf("prepare term index: %w", err)
		}
		af, err := WriteAgentFiles(WriteAgentFilesOptions{
			CWD:       root,
			Workspace: name,
			DataDir:   m.BaseDir,
			Force:     opt.Force,
			IDEs:      opt.IDEs,
		})
		if err != nil {
			return nil, err
		}
		return &ReinstallResult{Project: name, DBPath: p.DBPath, AgentFiles: af, TermIndex: termIndex}, nil
	})
	if err != nil {
		return nil, err
	}
	out, _ := v.(*ReinstallResult)
	return out, nil
}

func (m *Manager) Init(ctx context.Context, opt InitOptions) (*InitResult, error) {
	v, err := m.withRegistryLock(func(reg *Registry) (any, error) {
		name := opt.ProjectName
		if strings.TrimSpace(name) == "" {
			base := filepath.Base(opt.CWD)
			name = base
		}
		name, err := ValidateProjectName(name)
		if err != nil {
			return nil, err
		}
		dbPath := filepath.Join(m.BaseDir, name+".db")
		existing := findProject(reg, name)
		if existing != nil && !opt.Reuse && !opt.Force {
			return nil, fmt.Errorf("%w: project already exists", core.ErrAlreadyExists)
		}
		if existing != nil && opt.Force {
			if _, err := archiveDBFile(m.BaseDir, existing.DBPath, name); err != nil {
				return nil, err
			}
		}
		store, err := sqlite.Open(ctx, dbPath)
		if err != nil {
			return nil, err
		}
		if err := store.Close(); err != nil {
			return nil, err
		}
		root := FindProjectRoot(opt.CWD)
		absRoot, err := filepath.Abs(root)
		if err != nil {
			absRoot = root
		}
		upsertProject(reg, Project{
			Name:          name,
			DBPath:        dbPath,
			WorkspaceRoot: absRoot,
			CreatedAt:     nowOr(existing, true),
			LastUsedAt:    time.Now().UTC(),
		})

		out := &InitResult{Project: name, DBPath: dbPath}
		if !opt.NoRule {
			targets, err := normalizeRuleTargets(opt.CWD, opt.IDEs)
			if err != nil {
				return nil, err
			}
			written := make([]string, 0, len(targets))
			cursorRuleSet := false
			rulePath := opt.RulePath
			if strings.TrimSpace(rulePath) == "" {
				rulePath = filepath.Join(opt.CWD, ".cursor", "rules", "agent-memory.mdc")
			}
			if err := validateRulePath(opt.CWD, rulePath); err != nil {
				return nil, err
			}
			for _, t := range targets {
				switch t {
				case "kiro":
					hooksDir := filepath.Join(opt.CWD, ".kiro", "hooks")
					if err := os.MkdirAll(hooksDir, 0o755); err != nil {
						return nil, err
					}
					for _, hook := range HippocampusHooks() {
						path := filepath.Join(hooksDir, hook.Name)
						if err := writeRuleFile(path, hook.Content); err != nil {
							return nil, err
						}
						written = append(written, path)
					}
				case "cursor":
					if err := writeCursorRule(rulePath, name); err != nil {
						return nil, err
					}
					written = append(written, rulePath)
					out.CursorRule = rulePath
					cursorRuleSet = true
				case "antigravity":
					p := filepath.Join(opt.CWD, ".agents", "rules", "agent-memory.md")
					if err := writeRuleFile(p, antigravityRuleContent(name)); err != nil {
						return nil, err
					}
					written = append(written, p)

					var ir IDEUpgradeResult
					if err := deployPredefinedSkills(opt.CWD, opt.Force, &ir); err != nil {
						return nil, err
					}
				case "aierules":
					p := filepath.Join(opt.CWD, ".aierules")
					if _, err := upsertRuleSection(p, "## agent-memory (MANDATORY)", genericRulesSection(name), opt.Force); err != nil {
						return nil, err
					}
					written = append(written, p)
				case "cursorrules":
					p := filepath.Join(opt.CWD, ".cursorrules")
					if _, err := upsertRuleSection(p, "## agent-memory (MANDATORY)", genericRulesSection(name), opt.Force); err != nil {
						return nil, err
					}
					written = append(written, p)
				case "windsurfrules":
					p := filepath.Join(opt.CWD, ".windsurfrules")
					if _, err := upsertRuleSection(p, "## agent-memory (MANDATORY)", genericRulesSection(name), opt.Force); err != nil {
						return nil, err
					}
					written = append(written, p)
				case "claude":
					p := filepath.Join(opt.CWD, "CLAUDE.md")
					if _, err := upsertRuleSection(p, "## agent-memory (MANDATORY)", genericRulesSection(name), opt.Force); err != nil {
						return nil, err
					}
					written = append(written, p)
				case "zcode":
					p := filepath.Join(opt.CWD, "AGENTS.md")
					if _, err := upsertRuleSection(p, "## agent-memory (MANDATORY)", genericRulesSection(name), opt.Force); err != nil {
						return nil, err
					}
					written = append(written, p)
				case "codex":
					paths, err := writeCodexFiles(opt.CWD, name, m.BaseDir, opt.Force)
					if err != nil {
						return nil, err
					}
					written = append(written, paths...)
				case "trae":
					p := filepath.Join(opt.CWD, ".trae", "rules", "project_rules.md")
					if _, err := upsertRuleSection(p, "## agent-memory (MANDATORY)", genericRulesSection(name), opt.Force); err != nil {
						return nil, err
					}
					written = append(written, p)
				}
			}
			if cursorRuleSet {
				out.CursorRule = rulePath
			}
			out.RuleFiles = written
		}
		if opt.Study {
			store, err := sqlite.Open(ctx, dbPath)
			if err != nil {
				return nil, err
			}
			defer func() { _ = store.Close() }()
			home, _ := os.UserHomeDir()
			provider, err := embeddings.NewProvider(embeddings.DefaultModelDir(home))
			if err != nil {
				return nil, err
			}
			sources := DefaultStudySources(opt.CWD)
			study := engine.NewStudyEngine(engine.NewWritePipelineWithEmbedder(store, provider))
			sr, err := study.IngestWithOptions(ctx, engine.StudyOptions{
				Workspace: name,
				Sources:   sources,
				Depth:     "medium",
				DryRun:    false,
			})
			if err != nil {
				return nil, err
			}
			out.StudyRun = true
			out.MemoriesCreated = len(sr.WrittenIDs)
		}
		termIndex, err := prepareTermIndexPath(ctx, dbPath, name)
		if err != nil {
			return nil, fmt.Errorf("prepare term index: %w", err)
		}
		out.TermIndex = termIndex
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	out, _ := v.(*InitResult)
	return out, nil
}

func validateRulePath(root, target string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve rule path: %w", err)
	}
	if info, statErr := os.Lstat(targetAbs); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("rule target must be a regular non-symlink file")
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect rule target: %w", statErr)
	}
	if rel, relErr := filepath.Rel(rootAbs, targetAbs); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("rule path must stay within the workspace")
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve workspace symlinks: %w", err)
	}
	ancestor := filepath.Dir(targetAbs)
	for {
		if _, statErr := os.Lstat(ancestor); statErr == nil {
			break
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("inspect rule path: %w", statErr)
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return errors.New("rule path has no existing parent")
		}
		ancestor = parent
	}
	resolvedAncestor, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return fmt.Errorf("resolve rule parent symlinks: %w", err)
	}
	if rel, relErr := filepath.Rel(resolvedRoot, resolvedAncestor); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("rule path must not escape through a symlink")
	}
	return nil
}

// PrepareTermIndex migrates, backfills, and rebuilds one registered project.
func (m *Manager) PrepareTermIndex(ctx context.Context, projectName string) (*TermIndexSetupResult, error) {
	name, err := ValidateProjectName(projectName)
	if err != nil {
		return nil, err
	}
	reg, err := m.readRegistry()
	if err != nil {
		return nil, err
	}
	project := findProject(reg, name)
	if project == nil {
		return nil, ErrProjectNotRegistered
	}
	return prepareTermIndexPath(ctx, project.DBPath, name)
}

// ProjectForPath returns the most specific registered workspace containing path.
func (m *Manager) ProjectForPath(path string) (*Project, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, ErrProjectNotRegistered
	}
	absPath, err := canonicalWorkspacePath(path)
	if err != nil {
		return nil, err
	}
	registry, err := m.readRegistry()
	if err != nil {
		return nil, err
	}
	var match *Project
	for index := range registry.Projects {
		project := &registry.Projects[index]
		root := strings.TrimSpace(project.WorkspaceRoot)
		if root == "" {
			continue
		}
		absRoot, err := canonicalWorkspacePath(root)
		if err != nil {
			continue
		}
		absRoot = filepath.Clean(absRoot)
		rel, err := filepath.Rel(absRoot, absPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if match == nil || len(absRoot) > len(filepath.Clean(match.WorkspaceRoot)) {
			copy := *project
			copy.WorkspaceRoot = absRoot
			match = &copy
		}
	}
	if match == nil {
		return nil, ErrProjectNotRegistered
	}
	return match, nil
}

func canonicalWorkspacePath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absPath = filepath.Clean(absPath)
	for existing := absPath; ; existing = filepath.Dir(existing) {
		resolved, resolveErr := filepath.EvalSymlinks(existing)
		if resolveErr == nil {
			remainder, relErr := filepath.Rel(existing, absPath)
			if relErr != nil {
				return "", relErr
			}
			return filepath.Clean(filepath.Join(resolved, remainder)), nil
		}
		if !errors.Is(resolveErr, os.ErrNotExist) {
			return "", resolveErr
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return absPath, nil
		}
	}
}

// PrepareAllTermIndexes prepares every registered project independently.
func (m *Manager) PrepareAllTermIndexes(ctx context.Context) (map[string]*TermIndexSetupResult, map[string]string, error) {
	reg, err := m.readRegistry()
	if err != nil {
		return nil, nil, err
	}
	prepared := make(map[string]*TermIndexSetupResult)
	failures := make(map[string]string)
	for _, project := range reg.Projects {
		result, err := prepareTermIndexPath(ctx, project.DBPath, project.Name)
		if err != nil {
			failures[project.Name] = err.Error()
			continue
		}
		prepared[project.Name] = result
	}
	return prepared, failures, nil
}

func prepareTermIndexPath(ctx context.Context, dbPath, workspace string) (*TermIndexSetupResult, error) {
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	service := application.NewMemoryService(store, nil, nil)
	report, err := service.RebuildTermIndex(ctx, application.RebuildTermIndexOptions{Workspace: workspace})
	if err != nil {
		return nil, err
	}
	status, err := service.TermIndexStatus(ctx, workspace)
	if err != nil {
		return nil, err
	}
	return &TermIndexSetupResult{
		Ready: status.GatingEligible, RebuildRequired: status.RebuildRequired, RebuildReason: status.RebuildReason,
		Scanned: report.Scanned, Indexed: report.Indexed, Skipped: report.Skipped, Failed: report.Failed,
		DistinctTerms: report.DistinctTerms, Generation: report.Generation,
	}, nil
}

func (m *Manager) Rename(_ context.Context, opt RenameOptions) (*RenameResult, error) {
	v, err := m.withRegistryLock(func(reg *Registry) (any, error) {
		from := strings.TrimSpace(opt.From)
		if from == "" {
			rulePath := filepath.Join(opt.CWD, ".cursor", "rules", "agent-memory.mdc")
			ws, _ := readWorkspaceFromRule(rulePath)
			from = ws
		}
		if strings.TrimSpace(from) == "" {
			return nil, errors.New("from project is required")
		}
		to, err := ValidateProjectName(opt.To)
		if err != nil {
			return nil, err
		}
		p := findProject(reg, from)
		if p == nil {
			return nil, fmt.Errorf("%w: project not found", core.ErrNotFound)
		}
		if findProject(reg, to) != nil {
			return nil, fmt.Errorf("%w: target project already exists", core.ErrAlreadyExists)
		}

		oldName := p.Name
		oldDB := p.DBPath
		newDB := filepath.Join(m.BaseDir, to+".db")

		// 1. Write the registry BEFORE moving the DB.
		p.Name = to
		p.DBPath = newDB
		p.LastUsedAt = time.Now().UTC()
		if err := m.writeRegistry(reg); err != nil {
			// Revert in-memory changes.
			p.Name = oldName
			p.DBPath = oldDB
			return nil, fmt.Errorf("write registry: %w", err)
		}

		// 2. Move the DB. If it fails, roll back the registry.
		if err := moveDBWithSidecars(oldDB, newDB); err != nil {
			p.Name = oldName
			p.DBPath = oldDB
			if wErr := m.writeRegistry(reg); wErr != nil {
				return nil, fmt.Errorf("move db failed (%w) and registry rollback also failed (%w)", err, wErr)
			}
			return nil, fmt.Errorf("move db: %w", err)
		}

		// 3. Update all rule files the installer writes.
		ruleFiles := []string{
			filepath.Join(opt.CWD, ".cursor", "rules", "agent-memory.mdc"),
			filepath.Join(opt.CWD, ".agents", "rules", "agent-memory.md"),
			filepath.Join(opt.CWD, ".aierules"),
			filepath.Join(opt.CWD, ".cursorrules"),
			filepath.Join(opt.CWD, ".windsurfrules"),
			filepath.Join(opt.CWD, ".trae", "rules", "project_rules.md"),
			filepath.Join(opt.CWD, "AGENTS.md"),
			filepath.Join(opt.CWD, "CLAUDE.md"),
		}
		var ruleErrors []string
		updatedCount := 0
		for _, rp := range ruleFiles {
			if err := rewriteRuleWorkspace(rp, oldName, to); err != nil {
				if os.IsNotExist(err) {
					continue // skip missing files (not an error)
				}
				ruleErrors = append(ruleErrors, fmt.Sprintf("%s: %v", filepath.Base(rp), err))
				continue
			}
			updatedCount++
		}

		result := &RenameResult{From: from, To: to, DB: newDB}
		if len(ruleErrors) > 0 {
			// Attach rule errors for reporting but don't fail the operation.
			// The caller can inspect RenameResult if we add a field — for now,
			// log via the returned error.
			return result, fmt.Errorf("rename succeeded but %d rule file(s) could not be updated: %s",
				len(ruleErrors), strings.Join(ruleErrors, "; "))
		}
		return result, nil
	})
	if err != nil {
		// If the result was still populated (partial success), return it alongside the error.
		// The RenameResult is embedded inside the any return — we check it below.
		// For now, return the error as-is; the caller receives nil result on error.
		return nil, err
	}
	out, _ := v.(*RenameResult)
	return out, nil
}

func (m *Manager) List(_ context.Context) ([]ListItem, error) {
	reg, err := m.readRegistry()
	if err != nil {
		return nil, err
	}
	out := make([]ListItem, 0, len(reg.Projects))
	for _, p := range reg.Projects {
		size := fileSize(p.DBPath)
		memCount := 0
		if store, err := sqlite.Open(context.Background(), p.DBPath); err == nil {
			memCount, _ = store.CountMemories(context.Background())
			_ = store.Close()
		}
		out = append(out, ListItem{
			Name:          p.Name,
			DBPath:        p.DBPath,
			WorkspaceRoot: p.WorkspaceRoot,
			SizeBytes:     size,
			MemoryCount:   memCount,
			LastActivity:  p.LastUsedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Project returns registry metadata without opening the workspace database.
// The registry DB path is authoritative for daemon request routing.
func (m *Manager) Project(name string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, errors.New("workspace is required")
	}
	reg, err := m.readRegistry()
	if err != nil {
		return Project{}, err
	}
	project := findProject(reg, name)
	if project == nil {
		return Project{}, fmt.Errorf("%w: %q", ErrProjectNotRegistered, name)
	}
	if strings.TrimSpace(project.DBPath) == "" {
		return Project{}, fmt.Errorf("workspace %q has no database path", name)
	}
	return *project, nil
}

// ProjectNames returns registered routing keys without opening databases.
func (m *Manager) ProjectNames() ([]string, error) {
	reg, err := m.readRegistry()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(reg.Projects))
	for _, project := range reg.Projects {
		if name := strings.TrimSpace(project.Name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (m *Manager) Delete(_ context.Context, opt DeleteOptions) (*DeleteResult, error) {
	if !opt.Yes {
		return nil, errors.New("delete requires --yes")
	}
	v, err := m.withRegistryLock(func(reg *Registry) (any, error) {
		p := findProject(reg, opt.ProjectName)
		if p == nil {
			return nil, fmt.Errorf("%w: project not found", core.ErrNotFound)
		}
		out := &DeleteResult{Project: p.Name}
		if opt.KeepData {
			ap, err := archiveDBFile(m.BaseDir, p.DBPath, p.Name)
			if err != nil {
				return nil, err
			}
			out.ArchivedPath = ap
		} else {
			_ = removeDBWithSidecars(p.DBPath)
		}
		deleteProject(reg, p.Name)
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	out, _ := v.(*DeleteResult)
	return out, nil
}

func (m *Manager) withRegistryLock(fn func(*Registry) (any, error)) (any, error) {
	unlock, err := m.lockRegistry()
	if err != nil {
		return nil, err
	}
	defer unlock()
	reg, err := m.readRegistry()
	if err != nil {
		return nil, err
	}
	out, err := fn(reg)
	if err != nil {
		return nil, err
	}
	if err := m.writeRegistry(reg); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *Manager) registryPath() string { return filepath.Join(m.BaseDir, "workspaces.json") }
func (m *Manager) lockPath() string     { return filepath.Join(m.BaseDir, "workspaces.lock") }

func (m *Manager) lockRegistry() (func(), error) {
	lockAgeDeadline := 5 * time.Minute
	for i := 0; i < 50; i++ {
		f, err := os.OpenFile(m.lockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			// Write our PID into the lock file so a future contender can check liveness.
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(m.lockPath()) }, nil
		}
		// Contention: check if the lock is stale before retrying.
		if m.isLockStale(lockAgeDeadline) {
			_ = os.Remove(m.lockPath())
			time.Sleep(10 * time.Millisecond)
			continue
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, errors.New("registry lock timeout")
}

// isLockStale returns true when the lock file is older than maxAge AND the owning
// PID (written as the first line of the file) no longer corresponds to a live process.
func (m *Manager) isLockStale(maxAge time.Duration) bool {
	info, statErr := os.Stat(m.lockPath())
	if statErr != nil {
		return false
	}
	if time.Since(info.ModTime()) <= maxAge {
		return false
	}
	// Read the PID from the lock file body.
	b, readErr := os.ReadFile(m.lockPath())
	if readErr != nil {
		return false
	}
	firstLine := strings.TrimSpace(string(b))
	if firstLine == "" {
		return false
	}
	pid, parseErr := strconv.Atoi(firstLine)
	if parseErr != nil || pid <= 0 {
		return false
	}
	// Check whether the owning process is still alive (Unix).
	proc, findErr := os.FindProcess(pid)
	if findErr != nil {
		return true
	}
	err := proc.Signal(syscall.Signal(0))
	return err != nil
}

func (m *Manager) readRegistry() (*Registry, error) {
	p := m.registryPath()
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Registry{Projects: []Project{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var reg Registry
	if err := json.Unmarshal(b, &reg); err != nil {
		return nil, err
	}
	if reg.Projects == nil {
		reg.Projects = []Project{}
	}
	return &reg, nil
}

func (m *Manager) writeRegistry(reg *Registry) error {
	b, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal registry: %w", err)
	}
	tmp := m.registryPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp registry %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, m.registryPath()); err != nil {
		return fmt.Errorf("rename registry temp to %s: %w", m.registryPath(), err)
	}
	return nil
}

func findProject(reg *Registry, name string) *Project {
	for i := range reg.Projects {
		if reg.Projects[i].Name == name {
			return &reg.Projects[i]
		}
	}
	return nil
}

func upsertProject(reg *Registry, p Project) {
	if cur := findProject(reg, p.Name); cur != nil {
		cur.DBPath = p.DBPath
		cur.LastUsedAt = p.LastUsedAt
		if cur.CreatedAt.IsZero() {
			cur.CreatedAt = p.CreatedAt
		}
		return
	}
	reg.Projects = append(reg.Projects, p)
}

func deleteProject(reg *Registry, name string) {
	next := make([]Project, 0, len(reg.Projects))
	for _, p := range reg.Projects {
		if p.Name != name {
			next = append(next, p)
		}
	}
	reg.Projects = next
}

func nowOr(existing *Project, created bool) time.Time {
	if existing != nil {
		if created {
			if !existing.CreatedAt.IsZero() {
				return existing.CreatedAt
			}
		}
		if !existing.LastUsedAt.IsZero() {
			return existing.LastUsedAt
		}
	}
	return time.Now().UTC()
}

func writeCursorRule(path, workspace string) error {
	return writeRuleFile(path, cursorRuleContent(workspace))
}

func normalizeRuleTargets(cwd string, in []string) ([]string, error) {
	if len(in) == 0 {
		return detectDefaultRuleTargets(cwd), nil
	}
	expanded := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		switch v {
		case "all":
			expanded = append(expanded, "kiro", "cursor", "antigravity", "aierules", "cursorrules", "windsurfrules", "claude", "zcode", "codex", "trae")
		case "generic":
			expanded = append(expanded, "aierules", "cursorrules", "windsurfrules", "trae")
		default:
			expanded = append(expanded, v)
		}
	}

	seen := map[string]bool{}
	out := make([]string, 0, len(expanded))
	for _, t := range expanded {
		if seen[t] {
			continue
		}
		switch t {
		case "kiro", "cursor", "antigravity", "aierules", "cursorrules", "windsurfrules", "claude", "zcode", "codex", "trae":
			seen[t] = true
			out = append(out, t)
		default:
			return nil, fmt.Errorf("invalid ide: %s (allowed: kiro|cursor|antigravity|claude|zcode|codex|aierules|cursorrules|trae|windsurfrules|generic|all)", t)
		}
	}
	if len(out) == 0 {
		return detectDefaultRuleTargets(cwd), nil
	}
	return out, nil
}

func detectDefaultRuleTargets(cwd string) []string {
	targets := make([]string, 0, 8)
	if dirExists(filepath.Join(cwd, ".kiro")) {
		targets = append(targets, "kiro")
	}
	if dirExists(filepath.Join(cwd, ".cursor")) {
		targets = append(targets, "cursor")
	}
	if dirExists(filepath.Join(cwd, ".agents")) || os.Getenv("ANTIGRAVITY_AGENT") == "1" {
		targets = append(targets, "antigravity")
	}
	if fileExists(filepath.Join(cwd, ".aierules")) {
		targets = append(targets, "aierules")
	}
	if fileExists(filepath.Join(cwd, ".cursorrules")) {
		targets = append(targets, "cursorrules")
	}
	if dirExists(filepath.Join(cwd, ".trae")) {
		targets = append(targets, "trae")
	}
	if fileExists(filepath.Join(cwd, ".windsurfrules")) {
		targets = append(targets, "windsurfrules")
	}
	if fileExists(filepath.Join(cwd, "CLAUDE.md")) {
		targets = append(targets, "claude")
	}
	if fileExists(filepath.Join(cwd, "AGENTS.md")) {
		targets = append(targets, "zcode")
	}
	if dirExists(filepath.Join(cwd, ".codex")) || os.Getenv("CODEX_HOME") != "" {
		targets = append(targets, "codex")
	}
	if len(targets) == 0 {
		return []string{"cursor"}
	}
	return targets
}

func writeRuleFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create rule directory for %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write rule file %s: %w", path, err)
	}
	return nil
}

const (
	codexConfigStart = "# BEGIN agent-memory managed Codex sandbox"
	codexConfigEnd   = "# END agent-memory managed Codex sandbox"
	codexHookMarker  = "agent-memory managed lifecycle"
)

func writeCodexFiles(cwd, workspace, dataDir string, force bool) ([]string, error) {
	agentsPath := filepath.Join(cwd, "AGENTS.md")
	if _, err := upsertRuleSection(agentsPath, "## agent-memory (MANDATORY)", genericRulesSection(workspace), force); err != nil {
		return nil, fmt.Errorf("write AGENTS.md: %w", err)
	}
	configPath := filepath.Join(cwd, ".codex", "config.toml")
	if err := writeCodexConfig(configPath, dataDir); err != nil {
		return nil, err
	}
	hooksPath := filepath.Join(cwd, ".codex", "hooks.json")
	if err := writeCodexHooks(hooksPath, workspace); err != nil {
		return nil, err
	}
	return []string{agentsPath, configPath, hooksPath}, nil
}

// WriteCodexProjectFiles applies the existing managed Codex project
// configuration for use by higher-level connection adapters.
func WriteCodexProjectFiles(cwd, workspace, dataDir string, force bool) ([]string, error) {
	return writeCodexFiles(cwd, workspace, dataDir, force)
}

// WriteManagedProjectRule installs the canonical operating contract in a
// shared Markdown rule file while preserving unrelated user content.
func WriteManagedProjectRule(path, workspace string, force bool) error {
	_, err := upsertRuleSection(path, "## agent-memory (MANDATORY)", genericRulesSection(workspace), force)
	return err
}

// WriteCursorProjectFile writes only Cursor's owned always-applied rule.
func WriteCursorProjectFile(cwd, workspace string) ([]string, error) {
	path := filepath.Join(cwd, ".cursor", "rules", "agent-memory.mdc")
	if err := writeRuleFile(path, cursorRuleContent(workspace)); err != nil {
		return nil, err
	}
	return []string{path}, nil
}

// WriteKiroProjectFiles writes only Kiro's two owned lifecycle hooks.
func WriteKiroProjectFiles(cwd string) ([]string, error) {
	paths := make([]string, 0, len(HippocampusHooks()))
	for _, hook := range HippocampusHooks() {
		path := filepath.Join(cwd, ".kiro", "hooks", hook.Name)
		if err := writeRuleFile(path, hook.Content); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// RemoveManagedProjectRule removes only the bounded Agent Memory section from
// a shared Markdown rule file.
func RemoveManagedProjectRule(path string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	existing := string(b)
	marker := "## agent-memory (MANDATORY)"
	markerAt := strings.Index(existing, marker)
	if markerAt < 0 {
		return nil
	}
	after := existing[markerAt:]
	endAt := strings.Index(after[len(marker):], "\n---\n")
	var updated string
	if endAt < 0 {
		updated = existing[:markerAt]
	} else {
		updated = existing[:markerAt] + after[len(marker)+endAt+1:]
	}
	updated = strings.TrimSpace(updated)
	if updated != "" {
		updated += "\n"
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

// RemoveCodexProjectFiles removes only agent-memory-owned Codex config and
// hook entries. User-owned settings and hooks are preserved.
func RemoveCodexProjectFiles(cwd string) ([]string, error) {
	paths := []string{filepath.Join(cwd, "AGENTS.md"), filepath.Join(cwd, ".codex", "config.toml"), filepath.Join(cwd, ".codex", "hooks.json")}
	if err := RemoveManagedProjectRule(paths[0]); err != nil {
		return nil, err
	}
	config, err := os.ReadFile(paths[1])
	if err == nil {
		updated, removeErr := removeManagedBlock(string(config), codexConfigStart, codexConfigEnd)
		if removeErr != nil {
			return nil, removeErr
		}
		if err := writeRuleFile(paths[1], strings.TrimSpace(updated)+"\n"); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	root := map[string]any{}
	if hooksData, readErr := os.ReadFile(paths[2]); readErr == nil {
		if err := json.Unmarshal(hooksData, &root); err != nil {
			return nil, fmt.Errorf("parse Codex hooks %s: %w", paths[2], err)
		}
		hooks, _ := root["hooks"].(map[string]any)
		for event, rawGroups := range hooks {
			groups, _ := rawGroups.([]any)
			kept := make([]any, 0, len(groups))
			for _, raw := range groups {
				if !strings.Contains(fmt.Sprint(raw), codexHookMarker) {
					kept = append(kept, raw)
				}
			}
			hooks[event] = kept
		}
		encoded, err := json.MarshalIndent(root, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := writeRuleFile(paths[2], string(encoded)+"\n"); err != nil {
			return nil, err
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, readErr
	}
	return paths, nil
}

// WriteCodexGlobalFiles installs the user-wide Codex sandbox root and lifecycle
// hooks while preserving unrelated Codex configuration.
func WriteCodexGlobalFiles(codexHome, dataDir string) ([]string, error) {
	if strings.TrimSpace(codexHome) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve Codex home: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	configPath := filepath.Join(codexHome, "config.toml")
	if err := writeCodexGlobalConfig(configPath, dataDir); err != nil {
		return nil, err
	}
	hooksPath := filepath.Join(codexHome, "hooks.json")
	if err := writeCodexHooks(hooksPath, "agent-memory"); err != nil {
		return nil, err
	}
	return []string{configPath, hooksPath}, nil
}

func writeCodexConfig(path, dataDir string) error {
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("resolve Codex writable root: %w", err)
	}
	permissionPath := absDataDir
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		if relative, relErr := filepath.Rel(home, absDataDir); relErr == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			permissionPath = filepath.Join("~", relative)
		}
	}
	quoted := strconv.Quote(filepath.ToSlash(permissionPath))
	managed := codexConfigStart + "\n" +
		"default_permissions = \"agent-memory-workspace\"\n" +
		"permissions.agent-memory-workspace.filesystem.\":root\" = \"read\"\n" +
		"permissions.agent-memory-workspace.filesystem.\":tmpdir\" = \"write\"\n" +
		"permissions.agent-memory-workspace.filesystem.\":slash_tmp\" = \"write\"\n" +
		"permissions.agent-memory-workspace.filesystem." + quoted + " = \"write\"\n" +
		"permissions.agent-memory-workspace.filesystem.\":workspace_roots\" = { \".\" = \"write\", \".git\" = \"read\", \".agents\" = \"read\", \".codex\" = \"read\" }\n" +
		codexConfigEnd

	existing := ""
	if b, err := os.ReadFile(path); err == nil {
		existing = string(b)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read Codex config: %w", err)
	}
	userConfig, err := removeManagedBlock(existing, codexConfigStart, codexConfigEnd)
	if err != nil {
		return fmt.Errorf("inspect Codex config: %w", err)
	}
	if regexp.MustCompile(`(?m)^\s*(?:default_permissions|sandbox_mode)\s*=`).MatchString(userConfig) {
		return errors.New("existing Codex permission selection conflicts with the agent-memory project profile")
	}
	updated, err := replaceManagedBlock(existing, codexConfigStart, codexConfigEnd, managed)
	if err != nil {
		return fmt.Errorf("update Codex config: %w", err)
	}
	return writeRuleFile(path, strings.TrimSpace(updated)+"\n")
}

func writeCodexGlobalConfig(path, dataDir string) error {
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("resolve Codex writable root: %w", err)
	}
	quoted := strconv.Quote(filepath.ToSlash(absDataDir))
	managed := codexConfigStart + "\n" +
		"sandbox_workspace_write.writable_roots = [" + quoted + "]\n" +
		codexConfigEnd

	existing := ""
	if b, err := os.ReadFile(path); err == nil {
		existing = string(b)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read Codex config: %w", err)
	}
	if !strings.Contains(existing, codexConfigStart) {
		writableRoots := regexp.MustCompile(`(?m)^(\s*(?:sandbox_workspace_write\.)?writable_roots\s*=\s*)\[([^\n]*)\](\s*)$`)
		if match := writableRoots.FindStringSubmatchIndex(existing); match != nil {
			line := existing[match[0]:match[1]]
			if !strings.Contains(line, quoted) {
				closeAt := strings.LastIndex(line, "]")
				before := strings.TrimSpace(line[:closeAt])
				separator := ""
				if !strings.HasSuffix(before, "[") {
					separator = ", "
				}
				line = line[:closeAt] + separator + quoted + line[closeAt:]
				existing = existing[:match[0]] + line + existing[match[1]:]
			}
			return writeRuleFile(path, strings.TrimSpace(existing)+"\n")
		}
	}
	updated, err := replaceManagedBlock(existing, codexConfigStart, codexConfigEnd, managed)
	if err != nil {
		return fmt.Errorf("update Codex config: %w", err)
	}
	return writeRuleFile(path, strings.TrimSpace(updated)+"\n")
}

func replaceManagedBlock(existing, start, end, managed string) (string, error) {
	startAt := strings.Index(existing, start)
	endAt := strings.Index(existing, end)
	if startAt >= 0 || endAt >= 0 {
		if startAt < 0 || endAt < startAt {
			return "", errors.New("incomplete managed block")
		}
		endAt += len(end)
		return existing[:startAt] + managed + existing[endAt:], nil
	}
	if strings.TrimSpace(existing) == "" {
		return managed + "\n", nil
	}
	// Keep dotted managed keys in the TOML root context. Appending after a table
	// header would make them relative to that table.
	return managed + "\n\n" + strings.TrimLeft(existing, "\n"), nil
}

func removeManagedBlock(existing, start, end string) (string, error) {
	startAt := strings.Index(existing, start)
	endAt := strings.Index(existing, end)
	if startAt < 0 && endAt < 0 {
		return existing, nil
	}
	if startAt < 0 || endAt < startAt {
		return "", errors.New("incomplete managed block")
	}
	endAt += len(end)
	return strings.TrimSpace(existing[:startAt] + existing[endAt:]), nil
}

func writeCodexHooks(path, workspaceName string) error {
	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &root); err != nil {
			return fmt.Errorf("parse Codex hooks %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read Codex hooks: %w", err)
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PreCompact", "Stop"} {
		groups, _ := hooks[event].([]any)
		kept := make([]any, 0, len(groups)+1)
		for _, raw := range groups {
			if !strings.Contains(fmt.Sprint(raw), codexHookMarker) {
				kept = append(kept, raw)
			}
		}
		kept = append(kept, map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": fmt.Sprintf("agent-memory hook --event %s --agent codex --workspace %s # %s | %s", event, workspaceName, codexHookMarker, MemoryContractMarker),
				"timeout": 2,
			}},
		})
		hooks[event] = kept
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Codex hooks: %w", err)
	}
	return writeRuleFile(path, string(b)+"\n")
}

func appendRuleSectionIfMissing(path, marker, section string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	b, err := os.ReadFile(path)
	if err == nil {
		if strings.Contains(string(b), marker) {
			return nil
		}
		existing := strings.TrimRight(string(b), "\n")
		add := strings.TrimLeft(section, "\n")
		out := existing + "\n\n---\n\n" + add + "\n"
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
			return fmt.Errorf("append to rule file %s: %w", path, err)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read rule file %s: %w", path, err)
	}
	base := "# AI Agent Rules\n\n" + strings.TrimLeft(section, "\n") + "\n"
	if err := os.WriteFile(path, []byte(base), 0o644); err != nil {
		return fmt.Errorf("write new rule file %s: %w", path, err)
	}
	return nil
}

func antigravityRuleContent(workspace string) string {
	return "---\ntrigger: always_on\n---\n# agent-memory\nworkspace: " + workspace + "\n\n" + genericRulesSection(workspace) + "\n"
}

// MemoryContractMarker identifies the current generated project policy. Doctor
// and connection adapters use it to reject stale, partially upgraded clients.
const MemoryContractMarker = "agent-memory operating contract: v4"

func genericRulesSection(workspace string) string {
	return fmt.Sprintf(`## agent-memory (MANDATORY)

workspace: %s
contract: agent-memory operating contract: v4

Memory dimensions:
- **What**: facts, outcomes, and artifacts learned during work.
- **Where**: source paths, commands, URLs, memory IDs, and other locators.
- **When**: session/episode ordering, checkpoints, timestamps, and terminal status.
- **How**: safe action, observation, decision, concise rationale-summary, result, and handoff steps in a bounded solution episode.
- **Feedback**: honest retrieval usefulness and corrective outcomes.

Do not store private chain-of-thought. Store only concise rationale summaries needed to understand or repeat the work.

Always use `+"`"+`agent-memory`+"`"+` as the memory system:
- Run a focused memory search for the key terms and entities you're about to research.
- Run a recall for the current task only when the task continues previous work or search is weak. Directly escalate for `+"`"+`continue`+"`"+`, `+"`"+`resume`+"`"+`, or `+"`"+`what were we doing`+"`"+`.
- Before doing any self-research: run memory `+"`"+`search`+"`"+` first. Run task `+"`"+`recall`+"`"+` only when the task is about continuing previous work, or when search returns no useful / weak / insufficient results.
- After running search or recall: you MUST immediately submit a feedback score from 0 (useless) to 5 (extremely helpful) using command `+"`"+`agent-memory feedback --request-id <request_id> --score <0-5> --reason "<explanation>" --useful-count <useful_memories_count> --total-count <total_memories_retrieved>`+"`"+`. You MUST score honestly and objectively. If the score is below 4, you MUST provide a detailed explanation.
- If a query receives a low score (0 to 3), proceed to investigate. After finding the correct information:
  - If information was **missing**: Use `+"`"+`agent-memory write`+"`"+` to write the correct fact.
  - If a retrieved memory was **incorrect or out-of-date**:
    1. Write the new corrected memory first using `+"`"+`agent-memory write`+"`"+` and copy the new memory ID.
    2. Link the old incorrect memory to the new one by running `+"`"+`agent-memory feedback --memory-id <old_id> --outcome rejected --reconsolidation-action superseded --successor-memory-id <new_id> --reason "<explanation>"`+"`"+`.
- After learning durable new knowledge: write it to memory immediately.
- For non-trivial work: start a solution episode before substantial investigation or implementation. Append a step after each meaningful action, decision, or result; checkpoint during long work; transition the episode to a terminal status; then run session-end.
- Before ending an episode, account for What, Where, and When. When an optional dimension has no applicable stored value, surface the literal `+"`"+`N/A`+"`"+` with a concise reason instead of silently omitting it.
- For a future how-oriented question: run solution-path recall before repeating research. Promote only verified, reusable solution-path knowledge.
- When writing a memory: Choose up to three explicit keywords: names, terms, or helpful locators a human would search later. Do not copy the full content into keywords.
- You MUST proactively package reusable scripts, grep queries, workflows, or complex setup/learnings into a custom agent skill under `+"`"+`.agents/skills/`+"`"+` (using `+"`"+`agent-memory distill`+"`"+` or manual packaging) if they are valuable and highly likely to be reused. Do NOT wait for the user to ask; proactively distill skills once a workflow or learning pattern is successfully validated.
  - Do NOT use generic, numbered, or index-based filenames (like `+"`"+`part1.md`+"`"+`, `+"`"+`workflows_part1.md`+"`"+`).
  - Always use clear, descriptive, and meaningful names for all custom skill reference files (e.g., `+"`"+`db_performance.md`+"`"+`, `+"`"+`ui_fixes.md`+"`"+`).
  - Limit every individual skill file's size strictly to a maximum of 12,000 characters. If a skill grows beyond this, partition it by domain/feature and place the detailed references into a `+"`"+`references/`+"`"+` subdirectory with descriptive, meaningful filenames.
- At the end of a session: store a short session summary via `+"`"+`session-end`+"`"+`.

Commands:
- `+"`"+`agent-memory init`+"`"+`
- `+"`"+`agent-memory search --query "<keywords/entities>" --top-k 8`+"`"+`
- `+"`"+`agent-memory recall --task "<one-line task>" --budget 800 --format raw --include-observations`+"`"+`
- `+"`"+`agent-memory feedback --request-id "<id>" --score <0-5> --reason "<explanation>" --useful-count <useful_memories_count> --total-count <total_memories_retrieved>`+"`"+`
- `+"`"+`agent-memory feedback --memory-id "<old_id>" --outcome rejected --reconsolidation-action superseded --successor-memory-id "<new_id>" --reason "<explanation>"`+"`"+`
- `+"`"+`agent-memory write --type semantic --content "<durable fact + source>"`+"`"+`
- `+"`"+`agent-memory write --type procedural --content "<repeatable steps/checklist>"`+"`"+`
- `+"`"+`agent-memory write --type outcome --content "<what you tried> (result: success|failure|partial, approach: <how>, reason: <why>)"`+"`"+`
- `+"`"+`agent-memory session-end --transcript "<session summary or transcript>" --format json`+"`"+`
- `+"`"+`agent-memory work start --goal "<goal>" --session "<session_id>" --principal "<principal_id>" --client "<client_id>"`+"`"+`
- `+"`"+`agent-memory work step --episode "<episode_id>" --principal "<principal_id>" --kind <action|observation|decision|result> --status <running|completed|failed> --summary "<safe summary>"`+"`"+`
- `+"`"+`agent-memory work checkpoint --episode "<episode_id>" --principal "<principal_id>" --goal "<goal>" --next-action "<next action>"`+"`"+`
- `+"`"+`agent-memory work end --episode "<episode_id>" --principal "<principal_id>" --status <completed|partial|abandoned|cancelled>`+"`"+`
- `+"`"+`agent-memory work recall --task "<how-oriented task>" --principal "<principal_id>"`+"`"+`
- `+"`"+`agent-memory work promote --episode "<episode_id>" --summary "<summary_id>" --principal "<principal_id>" --memory-type procedural`+"`"+`
`, workspace)
}

func readWorkspaceFromRule(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "workspace:") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "workspace:")), nil
		}
	}
	return "", errors.New("workspace not found in rule")
}

func rewriteRuleWorkspace(path, from, to string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	cur := strings.ReplaceAll(string(b), "workspace: "+from, "workspace: "+to)
	return os.WriteFile(path, []byte(cur), 0o644)
}

// DefaultStudySources returns the conventional project files and directories
// used by bootstrap study. Callers must supply an already trusted project root.
func DefaultStudySources(cwd string) []string {
	candidates := []string{
		filepath.Join(cwd, "README.md"),
		filepath.Join(cwd, "docs"),
		filepath.Join(cwd, "src"),
		filepath.Join(cwd, "internal"),
		filepath.Join(cwd, "cmd"),
		filepath.Join(cwd, "pkg"),
		filepath.Join(cwd, "AGENTS.md"),
		filepath.Join(cwd, ".cursor", "rules"),
	}
	out := make([]string, 0, len(candidates))
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func archiveDBFile(baseDir, dbPath, name string) (string, error) {
	archivedDir := filepath.Join(baseDir, "archived")
	if err := os.MkdirAll(archivedDir, 0o755); err != nil {
		return "", err
	}
	ts := time.Now().UTC().Format("20060102T150405Z")
	dst := filepath.Join(archivedDir, fmt.Sprintf("%s.%s.db", name, ts))
	if err := moveDBWithSidecars(dbPath, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func moveDBWithSidecars(from, to string) error {
	if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_ = os.Rename(from+"-wal", to+"-wal")
	_ = os.Rename(from+"-shm", to+"-shm")
	return nil
}

func removeDBWithSidecars(db string) error {
	_ = os.Remove(db + "-wal")
	_ = os.Remove(db + "-shm")
	if err := os.Remove(db); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// HookFile describes a hippocampus hook file to be written.
type HookFile struct {
	Name    string
	Content string
}

// HippocampusHooks returns the canonical hippocampus hook files (recall + consolidation).
// These are the V2 gates that enforce memory recall before every agent turn and
// consolidation after every agent turn.
func HippocampusHooks() []HookFile {
	return []HookFile{
		{
			Name: "memory-recall-gate.json",
			Content: `{
  "name": "Memory Recall Gate",
  "version": "2.2.0",
  "description": "agent-memory operating contract: v4. Hippocampus recall and solution capture before every agent turn.",
  "when": {
    "type": "promptSubmit"
  },
  "then": {
    "type": "askAgent",
    "prompt": "Before responding to the user, do the following first:\n1. Extract 3-5 key terms and run: agent-memory search --query \"<key terms>\" --top-k 8\n2. Inspect the result and immediately submit: agent-memory feedback --request-id \"<request_id>\" --score <0-5> --reason \"<explanation>\" --useful-count <n> --total-count <n>\n3. Run ordinary recall only if one of these is true: the user asks to continue, resume, or recall previous work; search is weak; or assembled task context is needed; avoid unnecessary recall when search is already enough.\n4. For a how-oriented request run: agent-memory work recall --task \"<user message>\" --principal \"<principal_id>\"\n5. For non-trivial new work run: agent-memory work start --goal \"<goal>\" --session \"<session_id>\" --principal \"<principal_id>\" --client kiro\n6. During work use agent-memory work step after meaningful actions/decisions/results and agent-memory work checkpoint during long work.\n7. Before ending, account for What, Where, and When; use literal N/A with a concise reason for an optional dimension with no applicable stored value.\nUse retrieved context before answering. Do not store private chain-of-thought; store only concise rationale summaries."
  }
}
`,
		},
		{
			Name: "memory-consolidation-gate.json",
			Content: `{
  "name": "Memory Consolidation Gate",
  "version": "2.1.0",
  "description": "agent-memory operating contract: v4. Consolidate durable knowledge and solution progress after every agent turn.",
  "when": {
    "type": "agentStop"
  },
  "then": {
    "type": "askAgent",
    "prompt": "Review what happened in this session. Store durable What and Where facts with agent-memory write. Account for What, Where, and When; use literal N/A with a concise reason for an optional dimension with no applicable stored value. If a solution episode is active, append a final agent-memory work step, checkpoint if continuation is needed, and transition it to the truthful terminal status. If the result is verified and reusable, run agent-memory work promote. Always run agent-memory session-end --transcript \"<one paragraph summary>\" --format json. Do not store private chain-of-thought; store only concise rationale summaries."
  }
}
`,
		},
	}
}

// cursorRuleContent returns the full content for a cursor rule file.
func cursorRuleContent(workspace string) string {
	return fmt.Sprintf("---\ndescription: Always use Agent Memory for What, Where, When, How, and Feedback\nglobs: *\nalwaysApply: true\n---\n# agent-memory\nworkspace: %s\n\n## Default memory policy (MANDATORY)\n\n%s\n", workspace, strings.TrimRight(genericRulesSection(workspace), "\n"))
}

// legacyCursorRuleContent is retained temporarily as migration reference for
// older generated files; new files use the canonical contract above.
func legacyCursorRuleContent(workspace string) string {
	return fmt.Sprintf(`---
description: Always use agent-memory CLI for memory search, recall, write, and session-end
globs: *
alwaysApply: true
---
# agent-memory
workspace: %s

## Default memory policy (MANDATORY)

You MUST use the `+"`"+`agent-memory`+"`"+` CLI for memory retrieval and persistence.

### Before doing any self-research

- Run a focused memory search for the key terms and entities you're about to research.
- Run a recall for the current task only when the task is about continuing previous work, or when search returns no useful / weak / insufficient results.
- Directly escalate to recall for prompts like `+"`"+`continue`+"`"+`, `+"`"+`resume`+"`"+`, or `+"`"+`what were we doing`+"`"+`.

Commands:
- `+"`"+`agent-memory search --query "<keywords/entities>" --top-k 8`+"`"+`
- `+"`"+`agent-memory recall --task "<one-line task>" --budget 800 --format raw`+"`"+`

### After running search or recall

- You MUST immediately submit a feedback score from 0 (useless) to 5 (extremely helpful), indicating how many retrieved memories were useful.
- You MUST score honestly and objectively based on the true usefulness of the retrieved memories. Do not default to high scores.
- You MUST always provide a reason regardless of the score; if the score is below 4, you MUST provide a detailed explanation (the command will fail if reason is omitted for scores below 4).

Command:
- `+"`"+`agent-memory feedback --request-id "<request_id>" --score <0-5> --reason "<explanation>" --useful-count <useful_memories_count> --total-count <total_memories_retrieved>`+"`"+`

### While working

- If you discover durable new knowledge (facts, commands, config, constraints, architecture decisions), write it immediately.
- When writing a memory: Choose up to three explicit keywords: names, terms, or helpful locators a human would search later. Do not copy the full content into keywords.
- Prefer short, atomic memories. Include the source (file path / command / URL) in the content when available.
- You MUST proactively package reusable scripts, grep queries, workflows, or complex setup/learnings into a custom agent skill under `+"`"+`.agents/skills/`+"`"+` (using `+"`"+`agent-memory distill`+"`"+` or manual packaging) if they are valuable and highly likely to be reused. Do NOT wait for the user to ask; proactively distill skills once a workflow or learning pattern is successfully validated.
  - Do NOT use generic, numbered, or index-based filenames (like `+"`"+`part1.md`+"`"+`, `+"`"+`workflows_part1.md`+"`"+`).
  - Always use clear, descriptive, and meaningful names for all custom skill reference files (e.g., `+"`"+`db_performance.md`+"`"+`, `+"`"+`ui_fixes.md`+"`"+`).
  - Limit every individual skill file's size strictly to a maximum of 12,000 characters. If a skill grows beyond this, partition it by domain/feature and place the detailed references into a `+"`"+`references/`+"`"+` subdirectory with descriptive, meaningful filenames.

Commands:
- `+"`"+`agent-memory write --type semantic --content "<durable fact + source>"`+"`"+`
- `+"`"+`agent-memory write --type procedural --content "<repeatable steps/checklist>"`+"`"+`

### Corrective action on low-scoring queries (Score 0-3)

If retrieval has a low score (0-3), proceed to investigate and solve the task. Once correct information is learned/verified:
- **Missing information**: Write a new memory with `+"`"+`agent-memory write`+"`"+`.
- **Out-of-date or incorrect memory**:
  1. Write the corrected memory entry using `+"`"+`agent-memory write`+"`"+` and copy the returned memory ID.
  2. Update the old memory by linking it to the new successor:
     `+"`"+`agent-memory feedback --memory-id <old_id> --outcome rejected --reconsolidation-action superseded --successor-memory-id <new_id> --reason "<explanation>"`+"`"+`

### After attempts (success/failure)

- Record outcomes that would prevent repeating mistakes or preserve a working approach.

Command:
- `+"`"+`agent-memory write --type outcome --content "<what you tried> (result: success|failure|partial, approach: <how>, reason: <why>)"`+"`"+`

### At the end of a session

- Extract learnings from the session summary/transcript into memory.

Command:
- `+"`"+`agent-memory session-end --transcript "<session summary or transcript>" --format json`+"`"+`
`, workspace)
}

// WriteAgentFilesOptions controls how agent IDE files are written during upgrade.
type WriteAgentFilesOptions struct {
	// CWD is the project root to write into.
	CWD string
	// Workspace is the project name used in rule file content.
	// If empty, it is read from the existing cursor rule file, or derived from the CWD basename.
	Workspace string
	// DataDir is the agent-memory data directory Codex must be able to write.
	// If empty, it defaults to ~/.agent-memory.
	DataDir string
	// Force overwrites existing files even if content is identical.
	Force bool
	// IDEs optionally forces writing specific IDE files even if the project
	// does not already contain that IDE's marker files/directories.
	IDEs []string
}

// IDEUpgradeResult reports what was written or skipped for a single IDE.
type IDEUpgradeResult struct {
	IDE     string   `json:"ide"`
	Written []string `json:"written,omitempty"`
	Skipped []string `json:"skipped,omitempty"`
}

// WriteAgentFilesResult reports the full upgrade result across all detected IDEs.
type WriteAgentFilesResult struct {
	IDEs      []IDEUpgradeResult `json:"ides"`
	Workspace string             `json:"workspace"`
}

// WriteAgentFiles detects which agent IDEs are present in the project and writes
// the hippocampus hook/rule files for each one. It is safe to run repeatedly —
// files with identical content are skipped unless Force is set.
//
// Supported IDEs and their detection signals:
//   - kiro        : .kiro/ directory exists
//   - cursor      : .cursor/ directory exists
//   - cursorrules : .cursorrules file exists
//   - antigravity : .agents/ directory exists
//   - trae        : .trae/ directory exists
//   - claude      : CLAUDE.md file exists
//   - zcode       : AGENTS.md file exists
//   - aierules    : .aierules file exists
//   - windsurfrules : .windsurfrules file exists
func WriteAgentFiles(opt WriteAgentFilesOptions) (*WriteAgentFilesResult, error) {
	if strings.TrimSpace(opt.CWD) == "" {
		return nil, errors.New("CWD is required")
	}
	forcedTargets, err := normalizeExplicitRuleTargets(opt.CWD, opt.IDEs)
	if err != nil {
		return nil, err
	}

	// Resolve workspace name: explicit > read from cursor rule > cwd basename.
	ws := strings.TrimSpace(opt.Workspace)
	if ws == "" {
		cursorRule := filepath.Join(opt.CWD, ".cursor", "rules", "agent-memory.mdc")
		if detected, err := readWorkspaceFromRule(cursorRule); err == nil && detected != "" {
			ws = detected
		}
	}
	if ws == "" {
		ws = filepath.Base(opt.CWD)
	}

	res := &WriteAgentFilesResult{Workspace: ws}

	// --- Codex: AGENTS.md + project sandbox config + lifecycle hooks ---
	if targetSelected(forcedTargets, "codex", dirExists(filepath.Join(opt.CWD, ".codex"))) {
		ir := IDEUpgradeResult{IDE: "codex"}
		dataDir := strings.TrimSpace(opt.DataDir)
		if dataDir == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("codex: resolve data dir: %w", err)
			}
			dataDir = filepath.Join(home, ".agent-memory")
		}
		paths, err := writeCodexFiles(opt.CWD, ws, dataDir, opt.Force)
		if err != nil {
			return nil, fmt.Errorf("codex: %w", err)
		}
		for _, path := range paths {
			ir.Written = append(ir.Written, strings.TrimPrefix(strings.TrimPrefix(path, opt.CWD), string(filepath.Separator)))
		}
		res.IDEs = append(res.IDEs, ir)
	}

	// --- Kiro: .kiro/hooks/*.json ---
	if targetSelected(forcedTargets, "kiro", dirExists(filepath.Join(opt.CWD, ".kiro"))) {
		ir := IDEUpgradeResult{IDE: "kiro"}
		hooksDir := filepath.Join(opt.CWD, ".kiro", "hooks")
		if err := os.MkdirAll(hooksDir, 0o755); err != nil {
			return nil, fmt.Errorf("kiro: create hooks dir: %w", err)
		}
		for _, hf := range HippocampusHooks() {
			dst := filepath.Join(hooksDir, hf.Name)
			if !opt.Force {
				if existing, err := os.ReadFile(dst); err == nil &&
					strings.TrimSpace(string(existing)) == strings.TrimSpace(hf.Content) {
					ir.Skipped = append(ir.Skipped, filepath.Join(".kiro", "hooks", hf.Name))
					continue
				}
			}
			if err := os.WriteFile(dst, []byte(hf.Content), 0o644); err != nil {
				return nil, fmt.Errorf("kiro: write %s: %w", hf.Name, err)
			}
			ir.Written = append(ir.Written, filepath.Join(".kiro", "hooks", hf.Name))
		}
		res.IDEs = append(res.IDEs, ir)
	}

	// --- Cursor: .cursor/rules/agent-memory.mdc (full overwrite — always canonical) ---
	if targetSelected(forcedTargets, "cursor", dirExists(filepath.Join(opt.CWD, ".cursor"))) {
		ir := IDEUpgradeResult{IDE: "cursor"}
		rulePath := filepath.Join(opt.CWD, ".cursor", "rules", "agent-memory.mdc")
		newContent := cursorRuleContent(ws)
		if !opt.Force {
			if existing, err := os.ReadFile(rulePath); err == nil &&
				strings.TrimSpace(string(existing)) == strings.TrimSpace(newContent) {
				ir.Skipped = append(ir.Skipped, ".cursor/rules/agent-memory.mdc")
			} else {
				if err := writeRuleFile(rulePath, newContent); err != nil {
					return nil, fmt.Errorf("cursor: %w", err)
				}
				ir.Written = append(ir.Written, ".cursor/rules/agent-memory.mdc")
			}
		} else {
			if err := writeRuleFile(rulePath, newContent); err != nil {
				return nil, fmt.Errorf("cursor: %w", err)
			}
			ir.Written = append(ir.Written, ".cursor/rules/agent-memory.mdc")
		}
		res.IDEs = append(res.IDEs, ir)
	}

	// --- Cursor (legacy): .cursorrules (upsert section) ---
	if targetSelected(forcedTargets, "cursorrules", fileExists(filepath.Join(opt.CWD, ".cursorrules"))) {
		ir := IDEUpgradeResult{IDE: "cursorrules"}
		rulePath := filepath.Join(opt.CWD, ".cursorrules")
		marker := "## agent-memory (MANDATORY)"
		section := genericRulesSection(ws)
		written, err := upsertRuleSection(rulePath, marker, section, opt.Force)
		if err != nil {
			return nil, fmt.Errorf("cursorrules: %w", err)
		}
		if written {
			ir.Written = append(ir.Written, ".cursorrules")
		} else {
			ir.Skipped = append(ir.Skipped, ".cursorrules")
		}
		res.IDEs = append(res.IDEs, ir)
	}

	// --- Antigravity: .agents/rules/agent-memory.md (full overwrite) ---
	if targetSelected(forcedTargets, "antigravity", dirExists(filepath.Join(opt.CWD, ".agents"))) {
		ir := IDEUpgradeResult{IDE: "antigravity"}
		rulePath := filepath.Join(opt.CWD, ".agents", "rules", "agent-memory.md")
		newContent := antigravityRuleContent(ws)
		if !opt.Force {
			if existing, err := os.ReadFile(rulePath); err == nil &&
				strings.TrimSpace(string(existing)) == strings.TrimSpace(newContent) {
				ir.Skipped = append(ir.Skipped, ".agents/rules/agent-memory.md")
			} else {
				if err := writeRuleFile(rulePath, newContent); err != nil {
					return nil, fmt.Errorf("antigravity: %w", err)
				}
				ir.Written = append(ir.Written, ".agents/rules/agent-memory.md")
			}
		} else {
			if err := writeRuleFile(rulePath, newContent); err != nil {
				return nil, fmt.Errorf("antigravity: %w", err)
			}
			ir.Written = append(ir.Written, ".agents/rules/agent-memory.md")
		}

		// Deploy predefined skills
		if err := deployPredefinedSkills(opt.CWD, opt.Force, &ir); err != nil {
			return nil, fmt.Errorf("predefined skills: %w", err)
		}

		res.IDEs = append(res.IDEs, ir)
	}

	// --- Trae: .trae/rules/project_rules.md (upsert section) ---
	if targetSelected(forcedTargets, "trae", dirExists(filepath.Join(opt.CWD, ".trae"))) {
		ir := IDEUpgradeResult{IDE: "trae"}
		rulePath := filepath.Join(opt.CWD, ".trae", "rules", "project_rules.md")
		marker := "## agent-memory (MANDATORY)"
		section := genericRulesSection(ws)
		written, err := upsertRuleSection(rulePath, marker, section, opt.Force)
		if err != nil {
			return nil, fmt.Errorf("trae: %w", err)
		}
		if written {
			ir.Written = append(ir.Written, ".trae/rules/project_rules.md")
		} else {
			ir.Skipped = append(ir.Skipped, ".trae/rules/project_rules.md")
		}
		res.IDEs = append(res.IDEs, ir)
	}

	// --- AI Rules: .aierules (upsert section) ---
	if targetSelected(forcedTargets, "aierules", fileExists(filepath.Join(opt.CWD, ".aierules"))) {
		ir := IDEUpgradeResult{IDE: "aierules"}
		rulePath := filepath.Join(opt.CWD, ".aierules")
		marker := "## agent-memory (MANDATORY)"
		section := genericRulesSection(ws)
		written, err := upsertRuleSection(rulePath, marker, section, opt.Force)
		if err != nil {
			return nil, fmt.Errorf("aierules: %w", err)
		}
		if written {
			ir.Written = append(ir.Written, ".aierules")
		} else {
			ir.Skipped = append(ir.Skipped, ".aierules")
		}
		res.IDEs = append(res.IDEs, ir)
	}

	// --- Windsurf Rules: .windsurfrules (upsert section) ---
	if targetSelected(forcedTargets, "windsurfrules", fileExists(filepath.Join(opt.CWD, ".windsurfrules"))) {
		ir := IDEUpgradeResult{IDE: "windsurfrules"}
		rulePath := filepath.Join(opt.CWD, ".windsurfrules")
		marker := "## agent-memory (MANDATORY)"
		section := genericRulesSection(ws)
		written, err := upsertRuleSection(rulePath, marker, section, opt.Force)
		if err != nil {
			return nil, fmt.Errorf("windsurfrules: %w", err)
		}
		if written {
			ir.Written = append(ir.Written, ".windsurfrules")
		} else {
			ir.Skipped = append(ir.Skipped, ".windsurfrules")
		}
		res.IDEs = append(res.IDEs, ir)
	}

	// --- Claude: CLAUDE.md (upsert section) ---
	if targetSelected(forcedTargets, "claude", fileExists(filepath.Join(opt.CWD, "CLAUDE.md"))) {
		ir := IDEUpgradeResult{IDE: "claude"}
		rulePath := filepath.Join(opt.CWD, "CLAUDE.md")
		marker := "## agent-memory (MANDATORY)"
		section := genericRulesSection(ws)
		written, err := upsertRuleSection(rulePath, marker, section, opt.Force)
		if err != nil {
			return nil, fmt.Errorf("claude: %w", err)
		}
		if written {
			ir.Written = append(ir.Written, "CLAUDE.md")
		} else {
			ir.Skipped = append(ir.Skipped, "CLAUDE.md")
		}
		res.IDEs = append(res.IDEs, ir)
	}

	// --- ZCode: AGENTS.md (upsert section) ---
	if targetSelected(forcedTargets, "zcode", fileExists(filepath.Join(opt.CWD, "AGENTS.md"))) {
		ir := IDEUpgradeResult{IDE: "zcode"}
		rulePath := filepath.Join(opt.CWD, "AGENTS.md")
		marker := "## agent-memory (MANDATORY)"
		section := genericRulesSection(ws)
		written, err := upsertRuleSection(rulePath, marker, section, opt.Force)
		if err != nil {
			return nil, fmt.Errorf("zcode: %w", err)
		}
		if written {
			ir.Written = append(ir.Written, "AGENTS.md")
		} else {
			ir.Skipped = append(ir.Skipped, "AGENTS.md")
		}
		res.IDEs = append(res.IDEs, ir)
	}

	return res, nil
}

func normalizeExplicitRuleTargets(cwd string, in []string) (map[string]bool, error) {
	if len(in) == 0 {
		return nil, nil
	}
	targets, err := normalizeRuleTargets(cwd, in)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(targets))
	for _, target := range targets {
		out[target] = true
	}
	return out, nil
}

func targetEnabled(targets map[string]bool, name string) bool {
	return targets != nil && targets[name]
}

func targetSelected(targets map[string]bool, name string, detected bool) bool {
	if targets != nil {
		return targetEnabled(targets, name)
	}
	return detected
}

// dirExists returns true if path exists and is a directory.
func dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// fileExists returns true if path exists and is a regular file.
func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// upsertRuleSection replaces an existing marked section or appends it if missing.
// Returns true if the file was written, false if it was already up-to-date.
func upsertRuleSection(path, marker, section string, force bool) (bool, error) {
	newSection := strings.TrimSpace(section)
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	if errors.Is(err, os.ErrNotExist) {
		// File doesn't exist — create it with just the section.
		content := "# AI Agent Rules\n\n" + newSection + "\n"
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, err
		}
		return true, os.WriteFile(path, []byte(content), 0o644)
	}

	existing := string(b)
	markerIdx := strings.Index(existing, marker)

	if markerIdx == -1 {
		// Section not present — append it.
		out := strings.TrimRight(existing, "\n") + "\n\n---\n\n" + newSection + "\n"
		return true, os.WriteFile(path, []byte(out), 0o644)
	}

	// Section present — replace from marker to next "---" separator or end of file.
	before := existing[:markerIdx]
	after := existing[markerIdx:]
	// Find the end of the section: next top-level "---" separator after the marker.
	endIdx := strings.Index(after[len(marker):], "\n---\n")
	var replaced string
	if endIdx == -1 {
		// Section runs to end of file.
		replaced = before + newSection + "\n"
	} else {
		tail := after[len(marker)+endIdx:]
		replaced = before + newSection + "\n" + tail
	}

	if !force && strings.TrimSpace(replaced) == strings.TrimSpace(existing) {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(replaced), 0o644)
}

//go:embed predefined_skills
var predefinedSkillsFS embed.FS

func deployPredefinedSkills(cwd string, force bool, ir *IDEUpgradeResult) error {
	err := fs.WalkDir(predefinedSkillsFS, "predefined_skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel("predefined_skills", path)
		if err != nil {
			return err
		}

		content, err := predefinedSkillsFS.ReadFile(path)
		if err != nil {
			return err
		}

		destPath := filepath.Join(cwd, ".agents", "skills", relPath)
		destDir := filepath.Dir(destPath)
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return err
		}

		displayPath := filepath.Join(".agents", "skills", relPath)

		if !force {
			if existing, err := os.ReadFile(destPath); err == nil &&
				strings.TrimSpace(string(existing)) == strings.TrimSpace(string(content)) {
				ir.Skipped = append(ir.Skipped, displayPath)
				return nil
			}
		}

		if err := os.WriteFile(destPath, content, 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", displayPath, err)
		}
		ir.Written = append(ir.Written, displayPath)
		return nil
	})
	return err
}
