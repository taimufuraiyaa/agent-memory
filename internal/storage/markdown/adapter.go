package markdown

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	sectionStartFmt = "<!-- AGENT_MEMORY:START id=%s -->"
	sectionEndFmt   = "<!-- AGENT_MEMORY:END id=%s -->"
)

type Adapter struct {
	filePath  string
	maxTokens int
}

func NewAdapter(filePath string, maxTokens int) *Adapter {
	if maxTokens <= 0 {
		maxTokens = 4000
	}
	return &Adapter{filePath: filePath, maxTokens: maxTokens}
}

func (a *Adapter) Upsert(id, content string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("id is required")
	}
	if err := a.ensureFile(); err != nil {
		return err
	}
	raw, err := os.ReadFile(a.filePath)
	if err != nil {
		return err
	}
	cur := string(raw)
	block := a.render(id, content)

	if a.exists(cur, id) {
		cur = a.replace(cur, id, block)
	} else {
		if !strings.HasSuffix(cur, "\n") && cur != "" {
			cur += "\n"
		}
		cur += block + "\n"
	}
	if countTokens(cur) > a.maxTokens {
		return fmt.Errorf("markdown budget exceeded")
	}
	return atomicWrite(a.filePath, []byte(cur))
}

func (a *Adapter) Remove(id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if err := a.ensureFile(); err != nil {
		return err
	}
	raw, err := os.ReadFile(a.filePath)
	if err != nil {
		return err
	}
	cur := string(raw)
	next := a.replace(cur, id, "")
	return atomicWrite(a.filePath, []byte(next))
}

func (a *Adapter) ensureFile() error {
	if err := os.MkdirAll(filepath.Dir(a.filePath), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(a.filePath); err == nil {
		return nil
	}
	return os.WriteFile(a.filePath, []byte(""), 0o644)
}

func (a *Adapter) render(id, content string) string {
	return fmt.Sprintf("%s\n%s\n%s", fmt.Sprintf(sectionStartFmt, id), strings.TrimSpace(content), fmt.Sprintf(sectionEndFmt, id))
}

func (a *Adapter) exists(cur, id string) bool {
	return strings.Contains(cur, fmt.Sprintf(sectionStartFmt, id))
}

func (a *Adapter) replace(cur, id, block string) string {
	start := fmt.Sprintf(sectionStartFmt, id)
	end := fmt.Sprintf(sectionEndFmt, id)
	si := strings.Index(cur, start)
	if si < 0 {
		return cur
	}
	ei := strings.Index(cur[si:], end)
	if ei < 0 {
		return cur
	}
	ei = si + ei + len(end)
	// Consume one trailing newline after managed block if present.
	if ei < len(cur) && cur[ei] == '\n' {
		ei++
	}
	return cur[:si] + block + cur[ei:]
}

func countTokens(s string) int {
	return len(strings.Fields(s))
}

func atomicWrite(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
