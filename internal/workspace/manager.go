package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/time/timebooks/agent-memory/internal/engine"
	"github.com/time/timebooks/agent-memory/internal/storage/sqlite"
)

type Project struct {
	Name       string    `json:"name"`
	DBPath     string    `json:"db_path"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at"`
}

type Registry struct {
	Projects []Project `json:"projects"`
}

type Manager struct {
	BaseDir string
}

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
	Project         string   `json:"project"`
	DBPath          string   `json:"db_path"`
	CursorRule      string   `json:"cursor_rule,omitempty"`
	RuleFiles       []string `json:"rule_files,omitempty"`
	StudyRun        bool     `json:"study_run"`
	MemoriesCreated int      `json:"memories_created,omitempty"`
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
	Name         string    `json:"name"`
	DBPath       string    `json:"db_path"`
	SizeBytes    int64     `json:"size_bytes"`
	MemoryCount  int       `json:"memory_count"`
	LastActivity time.Time `json:"last_activity"`
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
			return nil, errors.New("project already exists")
		}
		if existing != nil && opt.Force {
			if _, err := archiveDBFile(m.BaseDir, existing.DBPath, name); err != nil {
				return nil, err
			}
		}
		if _, err := sqlite.Open(ctx, dbPath); err != nil {
			return nil, err
		}
		upsertProject(reg, Project{
			Name:       name,
			DBPath:     dbPath,
			CreatedAt:  nowOr(existing, true),
			LastUsedAt: time.Now().UTC(),
		})

		out := &InitResult{Project: name, DBPath: dbPath}
		if !opt.NoRule {
			targets, err := normalizeRuleTargets(opt.IDEs)
			if err != nil {
				return nil, err
			}
			written := make([]string, 0, len(targets))
			cursorRuleSet := false
			rulePath := opt.RulePath
			if strings.TrimSpace(rulePath) == "" {
				rulePath = filepath.Join(opt.CWD, ".cursor", "rules", "agent-memory.mdc")
			}
			for _, t := range targets {
				switch t {
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
				case "aierules":
					p := filepath.Join(opt.CWD, ".aierules")
					if err := appendRuleSectionIfMissing(p, "## agent-memory (MANDATORY)", genericRulesSection(name)); err != nil {
						return nil, err
					}
					written = append(written, p)
				case "cursorrules":
					p := filepath.Join(opt.CWD, ".cursorrules")
					if err := appendRuleSectionIfMissing(p, "## agent-memory (MANDATORY)", genericRulesSection(name)); err != nil {
						return nil, err
					}
					written = append(written, p)
				case "windsurfrules":
					p := filepath.Join(opt.CWD, ".windsurfrules")
					if err := appendRuleSectionIfMissing(p, "## agent-memory (MANDATORY)", genericRulesSection(name)); err != nil {
						return nil, err
					}
					written = append(written, p)
				case "claude":
					p := filepath.Join(opt.CWD, "CLAUDE.md")
					if err := appendRuleSectionIfMissing(p, "## agent-memory (MANDATORY)", genericRulesSection(name)); err != nil {
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
			sources := defaultStudySources(opt.CWD)
			study := engine.NewStudyEngine(engine.NewWritePipeline(store))
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
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	out, _ := v.(*InitResult)
	return out, nil
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
			return nil, errors.New("project not found")
		}
		if findProject(reg, to) != nil {
			return nil, errors.New("target project already exists")
		}
		newDB := filepath.Join(m.BaseDir, to+".db")
		if err := moveDBWithSidecars(p.DBPath, newDB); err != nil {
			return nil, err
		}
		p.Name = to
		p.DBPath = newDB
		p.LastUsedAt = time.Now().UTC()
		rulePath := filepath.Join(opt.CWD, ".cursor", "rules", "agent-memory.mdc")
		_ = rewriteRuleWorkspace(rulePath, from, to)
		return &RenameResult{From: from, To: to, DB: newDB}, nil
	})
	if err != nil {
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
			Name:         p.Name,
			DBPath:       p.DBPath,
			SizeBytes:    size,
			MemoryCount:  memCount,
			LastActivity: p.LastUsedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Manager) Delete(_ context.Context, opt DeleteOptions) (*DeleteResult, error) {
	if !opt.Yes {
		return nil, errors.New("delete requires --yes")
	}
	v, err := m.withRegistryLock(func(reg *Registry) (any, error) {
		p := findProject(reg, opt.ProjectName)
		if p == nil {
			return nil, errors.New("project not found")
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
	for i := 0; i < 50; i++ {
		f, err := os.OpenFile(m.lockPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(m.lockPath()) }, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, errors.New("registry lock timeout")
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
		return err
	}
	tmp := m.registryPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.registryPath())
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(`# agent-memory
workspace: %s

## Default memory policy (MANDATORY)

You MUST use the `+"`"+`agent-memory`+"`"+` CLI for memory retrieval and persistence.

### Before doing any self-research

- Run a focused memory search for the key terms and entities you’re about to research.
- Then run a recall for the current task and use it as primary context before searching the web/docs/code.

Commands:
- `+"`"+`rtk agent-memory search --query "<keywords/entities>" --top-k 8`+"`"+`
- `+"`"+`rtk agent-memory recall --task "<one-line task>" --budget 800 --format raw`+"`"+`

### While working

- If you discover durable new knowledge (facts, commands, config, constraints, architecture decisions), write it immediately.
- Prefer short, atomic memories. Include the source (file path / command / URL) in the content when available.

Commands:
- `+"`"+`rtk agent-memory write --type semantic --content "<durable fact + source>"`+"`"+`
- `+"`"+`rtk agent-memory write --type procedural --content "<repeatable steps/checklist>"`+"`"+`

### After attempts (success/failure)

- Record outcomes that would prevent repeating mistakes or preserve a working approach.

Command:
- `+"`"+`rtk agent-memory write --type outcome --content "<what you tried>" --outcome-result success|failure|partial --outcome-approach "<how>" --outcome-reason "<why>"`+"`"+`

### At the end of a session

- Extract learnings from the session summary/transcript into memory.

Command:
- `+"`"+`rtk agent-memory session-end --transcript "<session summary or transcript>" --format json`+"`"+`
`, workspace)
	return os.WriteFile(path, []byte(content), 0o644)
}

func normalizeRuleTargets(in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{"cursor"}, nil
	}
	expanded := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		switch v {
		case "all":
			expanded = append(expanded, "cursor", "antigravity", "aierules", "cursorrules", "windsurfrules", "claude")
		case "generic":
			expanded = append(expanded, "aierules", "cursorrules", "windsurfrules")
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
		case "cursor", "antigravity", "aierules", "cursorrules", "windsurfrules", "claude":
			seen[t] = true
			out = append(out, t)
		default:
			return nil, fmt.Errorf("invalid ide: %s (allowed: cursor|antigravity|claude|aierules|cursorrules|windsurfrules|generic|all)", t)
		}
	}
	if len(out) == 0 {
		return []string{"cursor"}, nil
	}
	return out, nil
}

func writeRuleFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func appendRuleSectionIfMissing(path, marker, section string) error {
	b, err := os.ReadFile(path)
	if err == nil {
		if strings.Contains(string(b), marker) {
			return nil
		}
		existing := strings.TrimRight(string(b), "\n")
		add := strings.TrimLeft(section, "\n")
		out := existing + "\n\n---\n\n" + add + "\n"
		return os.WriteFile(path, []byte(out), 0o644)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	base := "# AI Agent Rules\n\n" + strings.TrimLeft(section, "\n") + "\n"
	return os.WriteFile(path, []byte(base), 0o644)
}

func antigravityRuleContent(workspace string) string {
	return "# agent-memory\nworkspace: " + workspace + "\n\n" + genericRulesSection(workspace) + "\n"
}

func genericRulesSection(workspace string) string {
	return fmt.Sprintf(`## agent-memory (MANDATORY)

workspace: %s

Always use `+"`"+`agent-memory`+"`"+` as the memory system:
- Before doing any self-research: run memory `+"`"+`search`+"`"+`, then task `+"`"+`recall`+"`"+`.
- After learning durable new knowledge: write it to memory immediately.
- At the end of a session: store a short session summary via `+"`"+`session-end`+"`"+`.

Commands:
- `+"`"+`rtk agent-memory init`+"`"+`
- `+"`"+`rtk agent-memory search --query "<keywords/entities>" --top-k 8`+"`"+`
- `+"`"+`rtk agent-memory recall --task "<one-line task>" --budget 800 --format raw`+"`"+`
- `+"`"+`rtk agent-memory write --type semantic --content "<durable fact + source>"`+"`"+`
- `+"`"+`rtk agent-memory write --type procedural --content "<repeatable steps/checklist>"`+"`"+`
- `+"`"+`rtk agent-memory write --type outcome --content "<what you tried>" --outcome-result success|failure|partial --outcome-approach "<how>" --outcome-reason "<why>"`+"`"+`
- `+"`"+`rtk agent-memory session-end --transcript "<session summary or transcript>" --format json`+"`"+`
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

func defaultStudySources(cwd string) []string {
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
