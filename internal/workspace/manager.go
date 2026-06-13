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

	"github.com/time/timebooks/agent-memory/internal/embeddings"
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
			fileExists(filepath.Join(dir, "CLAUDE.md")) {
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
	_ = ctx
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
	reg, err := m.readRegistry()
	if err != nil {
		return nil, err
	}
	p := findProject(reg, name)
	if p == nil {
		return nil, errors.New("project not found in registry (run init first)")
	}
	if !fileExists(p.DBPath) {
		return nil, fmt.Errorf("db file missing: %s", p.DBPath)
	}
	af, err := WriteAgentFiles(WriteAgentFilesOptions{
		CWD:       root,
		Workspace: name,
		Force:     opt.Force,
		IDEs:      opt.IDEs,
	})
	if err != nil {
		return nil, err
	}
	return &ReinstallResult{Project: name, DBPath: p.DBPath, AgentFiles: af}, nil
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
				case "trae":
					p := filepath.Join(opt.CWD, ".trae", "rules", "project_rules.md")
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
			home, _ := os.UserHomeDir()
			provider, err := embeddings.NewProvider(embeddings.DefaultModelDir(home))
			if err != nil {
				return nil, err
			}
			sources := defaultStudySources(opt.CWD)
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
			expanded = append(expanded, "cursor", "antigravity", "aierules", "cursorrules", "windsurfrules", "claude", "trae")
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
		case "cursor", "antigravity", "aierules", "cursorrules", "windsurfrules", "claude", "trae":
			seen[t] = true
			out = append(out, t)
		default:
			return nil, fmt.Errorf("invalid ide: %s (allowed: cursor|antigravity|claude|aierules|cursorrules|trae|windsurfrules|generic|all)", t)
		}
	}
	if len(out) == 0 {
		return detectDefaultRuleTargets(cwd), nil
	}
	return out, nil
}

func detectDefaultRuleTargets(cwd string) []string {
	targets := make([]string, 0, 7)
	if dirExists(filepath.Join(cwd, ".cursor")) {
		targets = append(targets, "cursor")
	}
	if dirExists(filepath.Join(cwd, ".agents")) {
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

func genericRulesSection(workspace string) string {
	return fmt.Sprintf(`## agent-memory (MANDATORY)

workspace: %s

Always use `+"`"+`agent-memory`+"`"+` as the memory system:
- Before doing any self-research: run memory `+"`"+`search`+"`"+` first. Run task `+"`"+`recall`+"`"+` only when the task is about continuing previous work, or when search returns no useful / weak / insufficient results.
- After learning durable new knowledge: write it to memory immediately.
- At the end of a session: store a short session summary via `+"`"+`session-end`+"`"+`.

Commands:
- `+"`"+`agent-memory init`+"`"+`
- `+"`"+`agent-memory search --query "<keywords/entities>" --top-k 8`+"`"+`
- `+"`"+`agent-memory recall --task "<one-line task>" --budget 800 --format raw --include-observations`+"`"+`
- `+"`"+`agent-memory write --type semantic --content "<durable fact + source>"`+"`"+`
- `+"`"+`agent-memory write --type procedural --content "<repeatable steps/checklist>"`+"`"+`
- `+"`"+`agent-memory write --type outcome --content "<what you tried>" --outcome-result success|failure|partial --outcome-approach "<how>" --outcome-reason "<why>"`+"`"+`
- `+"`"+`agent-memory session-end --transcript "<session summary or transcript>" --format json`+"`"+`
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
  "description": "Hippocampus recall: use staged retrieval before every agent turn. Search first, then escalate to recall only when continuity or weak search results justify the extra context load.",
  "when": {
    "type": "promptSubmit"
  },
  "then": {
    "type": "askAgent",
    "prompt": "Before responding to the user, do the following first:\n1. Extract 3-5 key terms from the user's message.\n2. Run: agent-memory search --query \"<key terms>\" --top-k 8\n3. Inspect the search result.\n4. Run: agent-memory recall --task \"<user message>\" --budget 800 --format raw --include-observations only if one of these is true:\n   - the user is asking to continue, resume, or recall previous work\n   - search returns no useful results\n   - search returns weak, ambiguous, or insufficiently actionable results\n   - the task needs an assembled task context, not just candidate memories\n5. Use the search hits or recall output as primary context before answering.\n6. If retrieval still finds nothing useful, proceed with general knowledge and note the gap.\nDo not skip memory lookup even for simple questions, but avoid unnecessary recall when search is already enough."
  }
}
`,
		},
		{
			Name: "memory-consolidation-gate.json",
			Content: `{
  "name": "Memory Consolidation Gate",
  "version": "2.1.0",
  "description": "Hippocampus consolidation: write durable knowledge and compact the session after every agent turn. The agent always saves what it learned — it does not decide whether to write.",
  "when": {
    "type": "agentStop"
  },
  "then": {
    "type": "askAgent",
    "prompt": "Review what happened in this session and do the following:\n1. Identify anything worth keeping using this filter:\n   - Structural delta: new service, integration, or data flow discovered\n   - Hidden why: non-obvious design decision or trade-off\n   - Interface contract: API schema, data model, or protocol change\n   - Systemic insight: complex logic spanning multiple files\n   - ANY failure: always write failures, no filter applies\n2. For each qualifying item, write it with the correct type:\n   - Fact about the system -> agent-memory write --type semantic --content \"<fact>\"\n   - Convention or repeatable process -> agent-memory write --type procedural --content \"<steps>\"\n   - Attempt result (success or failure) -> agent-memory write --type outcome --content \"<what was tried>\" --outcome-result success|failure|partial --outcome-approach \"<how>\" --outcome-reason \"<why>\"\n3. Run session-end compaction: agent-memory session-end --transcript \"<one paragraph summary of this session>\" --format json\nDo not skip step 3 even if nothing was written in step 2."
  }
}
`,
		},
	}
}

// cursorRuleContent returns the full content for a cursor rule file.
func cursorRuleContent(workspace string) string {
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

### While working

- If you discover durable new knowledge (facts, commands, config, constraints, architecture decisions), write it immediately.
- Prefer short, atomic memories. Include the source (file path / command / URL) in the content when available.

Commands:
- `+"`"+`agent-memory write --type semantic --content "<durable fact + source>"`+"`"+`
- `+"`"+`agent-memory write --type procedural --content "<repeatable steps/checklist>"`+"`"+`

### After attempts (success/failure)

- Record outcomes that would prevent repeating mistakes or preserve a working approach.

Command:
- `+"`"+`agent-memory write --type outcome --content "<what you tried>" --outcome-result success|failure|partial --outcome-approach "<how>" --outcome-reason "<why>"`+"`"+`

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

	// --- Kiro: .kiro/hooks/*.json ---
	if targetEnabled(forcedTargets, "kiro") || dirExists(filepath.Join(opt.CWD, ".kiro")) {
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
	if targetEnabled(forcedTargets, "cursor") || dirExists(filepath.Join(opt.CWD, ".cursor")) {
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
	if targetEnabled(forcedTargets, "cursorrules") || fileExists(filepath.Join(opt.CWD, ".cursorrules")) {
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
	if targetEnabled(forcedTargets, "antigravity") || dirExists(filepath.Join(opt.CWD, ".agents")) {
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
		res.IDEs = append(res.IDEs, ir)
	}

	// --- Trae: .trae/rules/project_rules.md (upsert section) ---
	if targetEnabled(forcedTargets, "trae") || dirExists(filepath.Join(opt.CWD, ".trae")) {
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
	if targetEnabled(forcedTargets, "aierules") || fileExists(filepath.Join(opt.CWD, ".aierules")) {
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
	if targetEnabled(forcedTargets, "windsurfrules") || fileExists(filepath.Join(opt.CWD, ".windsurfrules")) {
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
	if targetEnabled(forcedTargets, "claude") || fileExists(filepath.Join(opt.CWD, "CLAUDE.md")) {
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
