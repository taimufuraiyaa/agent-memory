package harnesstools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessfs"
)

// sanitizeLine makes one line safe to show: valid UTF-8, no control characters, bounded.
func sanitizeLine(line string) string {
	line = strings.ToValidUTF8(line, "�")
	var b strings.Builder
	n := 0
	for _, r := range line {
		if r == '\t' {
			r = ' '
		}
		if unicode.IsControl(r) {
			continue
		}
		if n >= MaxLineBytes {
			b.WriteString("…")
			break
		}
		b.WriteRune(r)
		n += len(string(r))
	}
	return b.String()
}

func splitLines(data []byte) []string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// fit cuts text at a line boundary so it is at most max bytes, reporting whether it cut.
func fit(text string, max int) (string, bool) {
	if len(text) <= max {
		return text, false
	}
	cut := strings.LastIndexByte(text[:max], '\n')
	if cut < 0 {
		cut = 0
	}
	return text[:cut], true
}

func (s *session) read(project *harnessfs.Root, a readArgs, maxBytes int) ([]byte, harness.Outcome) {
	file, err := project.Read(a.Path, MaxReadFileBytes)
	if err != nil {
		return nil, outcomeFor(err)
	}
	lines := splitLines(file.Data)
	from := a.StartLine - 1
	if from > len(lines) {
		from = len(lines)
	}
	to := from + a.MaxLines
	if to > len(lines) {
		to = len(lines)
	}
	var b strings.Builder
	total := fmt.Sprint(len(lines))
	if file.Truncated {
		total += "+"
	}
	fmt.Fprintf(&b, "%s (revision %s, lines %d-%d of %s)\n", a.Path, file.Revision, from+1, to, total)
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, sanitizeLine(lines[i]))
	}
	text, cut := fit(s.provider.cfg.Redact(b.String()), maxBytes)
	if cut || to < len(lines) || file.Truncated {
		text += "\n[more content not shown]"
		text, _ = fit(text, maxBytes)
		return []byte(text), harness.OutcomePartial
	}
	return []byte(text), harness.OutcomeOK
}

type listEntry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Size int64  `json:"size,omitempty"`
}

func (s *session) list(project *harnessfs.Root, a listArgs, maxBytes int) ([]byte, harness.Outcome) {
	entries, truncated, err := project.ReadDir(a.Path, harnessfs.MaxListEntries)
	if err != nil {
		return nil, outcomeFor(err)
	}
	out := make([]listEntry, len(entries))
	for i, e := range entries {
		out[i] = listEntry{Name: sanitizeLine(e.Name), Kind: e.Kind, Size: e.Size}
	}
	return s.bounded(map[string]any{"path": a.Path, "entries": out, "truncated": truncated}, "entries", len(out), maxBytes, truncated)
}

// bounded encodes a result, dropping trailing items of one list until it fits the reply
// envelope, and reports Partial whenever anything was left out.
func (s *session) bounded(result map[string]any, key string, count, maxBytes int, alreadyCut bool) ([]byte, harness.Outcome) {
	encode := func(n int) []byte {
		trimmed := map[string]any{}
		for k, v := range result {
			trimmed[k] = v
		}
		switch list := result[key].(type) {
		case []listEntry:
			trimmed[key] = list[:n]
		case []match:
			trimmed[key] = list[:n]
		}
		if n < count {
			trimmed["truncated"] = true
		}
		raw, _ := json.Marshal(trimmed)
		return []byte(s.provider.cfg.Redact(string(raw)))
	}
	n := count
	body := encode(n)
	for len(body) > maxBytes && n > 0 {
		n--
		body = encode(n)
	}
	if len(body) > maxBytes {
		return nil, harness.OutcomeFailed
	}
	if n < count || alreadyCut {
		return body, harness.OutcomePartial
	}
	return body, harness.OutcomeOK
}

type match struct {
	Path   string   `json:"path"`
	Line   int      `json:"line"`
	Text   string   `json:"text"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

type matcher func(string) bool

func compileMatcher(a searchArgs) (matcher, error) {
	if a.Regex {
		pattern := a.Query
		if a.IgnoreCase {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern) // RE2: linear time, no catastrophic backtracking
		if err != nil {
			return nil, err
		}
		return re.MatchString, nil
	}
	if a.IgnoreCase {
		needle := strings.ToLower(a.Query)
		return func(line string) bool { return strings.Contains(strings.ToLower(line), needle) }, nil
	}
	return func(line string) bool { return strings.Contains(line, a.Query) }, nil
}

func (s *session) search(ctx context.Context, project *harnessfs.Root, a searchArgs, maxBytes int) ([]byte, harness.Outcome) {
	matches, err := compileMatcher(a)
	if err != nil {
		return nil, harness.OutcomeFailed
	}
	ctx, cancel := context.WithTimeout(ctx, s.provider.cfg.SearchBudget)
	defer cancel()
	var found []match
	scanned, skipped := 0, 0
	var totalBytes int64
	truncated := false
	deadline := false
	visit := func(file string, size int64) error {
		if ctx.Err() != nil {
			deadline = true
			return harnessfs.ErrStop
		}
		if a.Glob != "" {
			if ok, _ := path.Match(a.Glob, path.Base(file)); !ok {
				return nil
			}
		}
		if size > MaxReadFileBytes || totalBytes+size > MaxSearchTotalBytes {
			skipped++
			return nil
		}
		f, err := project.Read(file, MaxReadFileBytes)
		if err != nil {
			skipped++
			return nil
		}
		scanned++
		totalBytes += int64(len(f.Data))
		lines := splitLines(f.Data)
		for i, line := range lines {
			if !matches(line) {
				continue
			}
			m := match{Path: file, Line: i + 1, Text: sanitizeLine(line)}
			for b := max(0, i-a.Context); b < i; b++ {
				m.Before = append(m.Before, sanitizeLine(lines[b]))
			}
			for af := i + 1; af <= i+a.Context && af < len(lines); af++ {
				m.After = append(m.After, sanitizeLine(lines[af]))
			}
			found = append(found, m)
			if len(found) >= a.MaxResults {
				truncated = true
				return harnessfs.ErrStop
			}
		}
		return nil
	}
	// A directory is walked; anything else is treated as a single file.
	if _, _, dirErr := project.ReadDir(a.Path, 1); dirErr != nil {
		f, readErr := project.Read(a.Path, MaxReadFileBytes)
		if readErr != nil {
			return nil, outcomeFor(readErr)
		}
		_ = visit(a.Path, f.Size)
	} else {
		_, cutWalk, walkErr := project.Walk(a.Path, harnessfs.WalkOptions{MaxFiles: MaxSearchFiles, SkipDirs: searchSkipDirs}, visit)
		if walkErr != nil {
			return nil, outcomeFor(walkErr)
		}
		truncated = truncated || cutWalk
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Path != found[j].Path {
			return found[i].Path < found[j].Path
		}
		return found[i].Line < found[j].Line
	})
	result := map[string]any{"query": a.Query, "path": a.Path, "matches": found, "files_scanned": scanned, "files_skipped": skipped, "truncated": truncated}
	if deadline {
		result["stopped"] = "time budget"
	}
	return s.bounded(result, "matches", len(found), maxBytes, truncated || deadline)
}
