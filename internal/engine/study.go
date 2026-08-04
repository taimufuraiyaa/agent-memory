package engine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
)

// DefaultMaxFiles caps the number of files study will scan per run.
// A value of 0 in the user-supplied options means "use this default."
var DefaultMaxFiles = 200

// DefaultMaxFileSize is the maximum file size (in bytes) study will read.
// Larger files are skipped and reported as errors.
var DefaultMaxFileSize int64 = 256 * 1024 // 256 KB

// StudyError records a per-file error encountered during study ingestion.
type StudyError struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type StudyResult struct {
	SourcesScanned int          `json:"sources_scanned"`
	ScannedFiles   int          `json:"scanned_files"`
	Skipped        int          `json:"skipped"`
	Extracted      int          `json:"extracted"`
	WrittenIDs     []string     `json:"written_ids,omitempty"`
	Errors         []StudyError `json:"errors,omitempty"`
	DryRun         bool         `json:"dry_run"`
}

type StudyOptions struct {
	Workspace   string
	Sources     []string
	Depth       string
	DryRun      bool
	MaxFiles    int
	MaxFileSize int64
	Ignore      []string
}

type StudyEngine struct {
	pipeline *WritePipeline
}

func NewStudyEngine(pipeline *WritePipeline) *StudyEngine { return &StudyEngine{pipeline: pipeline} }

var errStudyMaxFiles = errors.New("study max files reached")

// gitignoreCache holds parsed ignore patterns keyed by directory path.
type gitignoreCache struct {
	mu    sync.RWMutex
	rules map[string][]string // dir -> list of glob patterns
}

func (c *gitignoreCache) get(dir string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Walk up the directory tree to find the nearest cached gitignore patterns.
	cur := cleanDirKey(dir)
	for {
		if patterns, ok := c.rules[cur]; ok {
			return patterns
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return nil
}

func (c *gitignoreCache) set(dir string, patterns []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rules == nil {
		c.rules = make(map[string][]string)
	}
	c.rules[cleanDirKey(dir)] = patterns
}

func cleanDirKey(dir string) string {
	return filepath.Clean(dir)
}

func (c *gitignoreCache) loadFile(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var patterns []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Strip leading / for top-level patterns.
		line = strings.TrimPrefix(line, "/")
		// For directory patterns (trailing /), store as "dir/" prefix
		// so that matchAnyGitignore can do prefix matching.
		// For non-directory patterns, store as-is.
		if line == "" {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// matchAnyGitignore checks whether a relative path matches any of the
// gitignore-style patterns from the owning directory.
func matchAnyGitignore(relPath string, patterns []string) bool {
	for _, p := range patterns {
		// Directory patterns (trailing /) match everything under that directory.
		if strings.HasSuffix(p, "/") {
			prefix := strings.TrimRight(p, "/")
			if strings.HasPrefix(relPath, prefix+"/") || relPath == prefix {
				return true
			}
			continue
		}
		if ok, _ := filepath.Match(p, relPath); ok {
			return true
		}
		// Also try matching just the base name (standard gitignore behavior).
		if ok, _ := filepath.Match(p, filepath.Base(relPath)); ok {
			return true
		}
	}
	return false
}

// isBinarySniff reads up to 512 bytes from a file and checks for null bytes.
func isBinarySniff(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var buf [512]byte
	n, err := f.Read(buf[:])
	if err != nil {
		return false, err
	}
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true, nil
		}
	}
	return false, nil
}

// safeTruncate truncates text to at most budget bytes while preserving
// structural boundaries (frontmatter fences, fenced code blocks, JSON/YAML
// bracket/brace pairs). Returns the truncated text.
func safeTruncate(text string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(text) <= budget {
		return text
	}
	// Work within the budget window.
	window := text[:budget]

	// Find the last safe boundary before budget.
	// Priority: end of a fenced code block (closing ```), end of frontmatter
	// (closing ---), a complete JSON object (balanced braces), a complete
	// YAML/JSON array (balanced brackets).
	lastSafe := findLastSafeBoundary(window)
	if lastSafe > 0 {
		return text[:lastSafe]
	}

	// Fallback: truncate at the last complete line.
	if idx := strings.LastIndex(window, "\n"); idx > 0 {
		return text[:idx]
	}
	return window
}

// findLastSafeBoundary looks for the last structural closure point in s.
// Returns the index just after that closure, or 0 if none found.
func findLastSafeBoundary(s string) int {
	// Scan byte-by-byte tracking fenced code blocks and bracket/brace depth.
	inFence := false
	fenceSeq := "" // the opening sequence we're inside (``` or ~~~)
	braceDepth := 0
	bracketDepth := 0
	lastSafe := 0
	i := 0

	for i < len(s) {
		// Check for fence open/close at current position.
		if !inFence && (hasPrefixAt(s, i, "```") || hasPrefixAt(s, i, "~~~")) {
			seq := s[i : i+3]
			inFence = true
			fenceSeq = seq
			i += 3
			continue
		}
		if inFence && hasPrefixAt(s, i, fenceSeq) {
			// Only close if it's a fence on its own line (preceded by newline
			// or at start, and followed by newline or end).
			beforeOK := i == 0 || s[i-1] == '\n'
			afterIdx := i + len(fenceSeq)
			afterOK := afterIdx >= len(s) || s[afterIdx] == '\n' || s[afterIdx] == '\r'
			if beforeOK && afterOK {
				inFence = false
				fenceSeq = ""
				lastSafe = afterIdx
				i = afterIdx
				continue
			}
		}

		if inFence {
			i++
			continue
		}

		ch := s[i]
		switch ch {
		case '{':
			braceDepth++
		case '}':
			braceDepth--
			if braceDepth == 0 && bracketDepth == 0 {
				lastSafe = i + 1
			}
		case '[':
			bracketDepth++
		case ']':
			bracketDepth--
			if braceDepth == 0 && bracketDepth == 0 {
				lastSafe = i + 1
			}
		case '\n':
			if braceDepth == 0 && bracketDepth == 0 {
				lastSafe = i
			}
		}
		i++
	}

	// If everything is balanced at the end, the whole string is safe.
	if braceDepth == 0 && bracketDepth == 0 && !inFence {
		lastSafe = len(s)
	}

	return lastSafe
}

// hasPrefixAt checks whether s[pos:] starts with prefix.
func hasPrefixAt(s string, pos int, prefix string) bool {
	return pos+len(prefix) <= len(s) && s[pos:pos+len(prefix)] == prefix
}

func (s *StudyEngine) Ingest(ctx context.Context, workspace, root string, dryRun bool) (*StudyResult, error) {
	return s.IngestWithOptions(ctx, StudyOptions{
		Workspace: workspace,
		Sources:   []string{root},
		Depth:     "medium",
		DryRun:    dryRun,
	})
}

func (s *StudyEngine) IngestWithOptions(ctx context.Context, opts StudyOptions) (*StudyResult, error) {
	res := &StudyResult{DryRun: opts.DryRun, WrittenIDs: []string{}}

	// Apply defaults.
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = DefaultMaxFiles
	}
	if opts.MaxFileSize <= 0 {
		opts.MaxFileSize = DefaultMaxFileSize
	}

	if len(opts.Sources) == 0 {
		opts.Sources = []string{"."}
	}

	cache := &gitignoreCache{}

	for _, source := range opts.Sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		res.SourcesScanned++

		info, err := os.Stat(source)
		if err != nil {
			res.Errors = append(res.Errors, StudyError{Path: source, Reason: fmt.Sprintf("stat: %v", err)})
			continue
		}
		if !info.IsDir() {
			if err := s.processFile(ctx, opts, cache, source, res); err != nil {
				if errors.Is(err, errStudyMaxFiles) {
					break
				}
				return res, err
			}
			if opts.MaxFiles > 0 && res.ScannedFiles >= opts.MaxFiles {
				break
			}
			continue
		}

		// Walk the directory tree.
		walkErr := filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				res.Errors = append(res.Errors, StudyError{Path: path, Reason: fmt.Sprintf("walk: %v", err)})
				return nil
			}

			// Parse .gitignore / .ignore files when we encounter them.
			if !d.IsDir() && (d.Name() == ".gitignore" || d.Name() == ".ignore") {
				patterns := cache.loadFile(path)
				if len(patterns) > 0 {
					cache.set(filepath.Dir(path), patterns)
				}
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

			// Check gitignore patterns for the file's parent directory.
			if patterns := cache.get(filepath.Dir(path)); len(patterns) > 0 {
				rel, relErr := filepath.Rel(source, path)
				if relErr == nil && matchAnyGitignore(rel, patterns) {
					res.Skipped++
					return nil
				}
			}

			return s.processFile(ctx, opts, cache, path, res)
		})
		if errors.Is(walkErr, errStudyMaxFiles) {
			break
		}
		if walkErr != nil {
			return res, walkErr
		}
	}

	// If no files succeeded and we have errors, propagate.
	if res.ScannedFiles == 0 && len(res.Errors) > 0 {
		return res, fmt.Errorf("study: no files indexed (%d error(s))", len(res.Errors))
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

func (s *StudyEngine) processFile(ctx context.Context, opts StudyOptions, cache *gitignoreCache, path string, res *StudyResult) error {
	if opts.MaxFiles > 0 && res.ScannedFiles >= opts.MaxFiles {
		return errStudyMaxFiles
	}
	if !isStudyFile(path) {
		return nil
	}

	// Check file size before reading.
	fi, err := os.Stat(path)
	if err != nil {
		res.Errors = append(res.Errors, StudyError{Path: path, Reason: fmt.Sprintf("stat: %v", err)})
		return nil
	}
	if fi.Size() > opts.MaxFileSize {
		res.Skipped++
		res.Errors = append(res.Errors, StudyError{Path: path, Reason: fmt.Sprintf("file too large (%d bytes > %d max)", fi.Size(), opts.MaxFileSize)})
		return nil
	}

	res.ScannedFiles++

	// Binary check.
	binary, err := isBinarySniff(path)
	if err != nil {
		res.Errors = append(res.Errors, StudyError{Path: path, Reason: fmt.Sprintf("binary check: %v", err)})
		return nil
	}
	if binary {
		res.ScannedFiles-- // undo increment; this file failed to scan
		res.Skipped++
		res.Errors = append(res.Errors, StudyError{Path: path, Reason: "binary file skipped"})
		return nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		res.Errors = append(res.Errors, StudyError{Path: path, Reason: fmt.Sprintf("read: %v", err)})
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
	if err != nil {
		res.Errors = append(res.Errors, StudyError{Path: path, Reason: fmt.Sprintf("write: %v", err)})
		return nil
	}
	if !out.Rejected {
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

	const (
		shallowBudget = 600
		mediumBudget  = 1500
		deepBudget    = 4000
	)

	var budget int
	switch strings.ToLower(strings.TrimSpace(depth)) {
	case "shallow":
		budget = shallowBudget
	case "deep":
		budget = deepBudget
	default:
		budget = mediumBudget
	}

	joined := strings.Join(lines, "\n")
	truncated := safeTruncate(joined, budget)
	// Replace newlines with spaces for a compact summary.
	return strings.TrimSpace(strings.ReplaceAll(truncated, "\n", " "))
}
