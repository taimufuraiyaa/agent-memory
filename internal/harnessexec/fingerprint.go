package harnessexec

import (
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

const (
	// MaxFingerprintFiles bounds how many ordinary files a snapshot records.
	MaxFingerprintFiles = 5000
	maxFingerprintDepth = 12
	maxProtectedEntries = 500
	// MaxReported bounds how many paths one category of a change report lists.
	MaxReported = 25
)

// skipDirs are never walked: dependency and build output changes constantly and is not the
// project's own work.
var skipDirs = map[string]bool{"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true, "__pycache__": true}

// protectedDirs and protectedFiles are the places a process would use to make itself run
// again later or to change how the project is built, checked or instructed. The read tools
// refuse to show them, so a change here is reported separately and loudly.
var (
	protectedDirs  = []string{".git/hooks", ".git/info", ".github", ".husky", ".claude", ".agents", ".kiro", ".vscode"}
	protectedFiles = []string{".git/config", ".git/HEAD", ".gitignore", ".gitattributes", ".gitmodules"}
)

type sig struct {
	size  int64
	mtime int64
	mode  fs.FileMode
}

// Fingerprint is a cheap record of a project's regular files, enough to tell what a command
// created, changed or removed. It records metadata only, never content.
type Fingerprint struct {
	files     map[string]sig
	protected map[string]sig
	truncated bool
}

// Snapshot records the project under root. A root that cannot be opened is an error; an
// unreadable directory below it is skipped.
func Snapshot(root string) (*Fingerprint, error) {
	project, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer project.Close()
	fp := &Fingerprint{files: map[string]sig{}, protected: map[string]sig{}}
	fp.walk(project, ".", 0)
	// Dotfiles in the project root are protected too: environment files, tool settings.
	if entries, err := fs.ReadDir(project.FS(), "."); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") && !e.IsDir() {
				fp.note(project, e.Name(), fp.protected)
			}
		}
	}
	for _, file := range protectedFiles {
		fp.note(project, file, fp.protected)
	}
	for _, dir := range protectedDirs {
		fp.walkProtected(project, dir, 0)
	}
	return fp, nil
}

func (fp *Fingerprint) note(project *os.Root, rel string, into map[string]sig) {
	info, err := project.Lstat(rel)
	if err != nil {
		return
	}
	into[rel] = sig{size: info.Size(), mtime: info.ModTime().UnixNano(), mode: info.Mode()}
}

func (fp *Fingerprint) walk(project *os.Root, dir string, depth int) {
	entries, err := fs.ReadDir(project.FS(), dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		rel := name
		if dir != "." {
			rel = path.Join(dir, name)
		}
		switch {
		case e.Type().IsDir():
			if skipDirs[name] || depth+1 >= maxFingerprintDepth {
				continue
			}
			fp.walk(project, rel, depth+1)
		case e.Type().IsRegular() || e.Type()&fs.ModeSymlink != 0:
			if len(fp.files) >= MaxFingerprintFiles {
				fp.truncated = true
				return
			}
			fp.note(project, rel, fp.files)
		}
	}
}

func (fp *Fingerprint) walkProtected(project *os.Root, dir string, depth int) {
	info, err := project.Lstat(dir)
	if err != nil {
		return
	}
	fp.protected[dir] = sig{size: 0, mtime: info.ModTime().UnixNano(), mode: info.Mode()}
	if !info.IsDir() || depth >= 4 {
		return
	}
	entries, err := fs.ReadDir(project.FS(), dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if len(fp.protected) >= maxProtectedEntries {
			fp.truncated = true
			return
		}
		fp.walkProtected(project, path.Join(dir, e.Name()), depth+1)
	}
}

// Change is what differs between two snapshots.
type Change struct {
	Created   []string
	Modified  []string
	Removed   []string
	Protected []string // paths in protected locations that were created, changed or removed
	// Omitted counts paths beyond MaxReported in any category.
	Omitted int
	// Incomplete reports that a snapshot hit its file bound, so the report may miss changes.
	Incomplete bool
}

// Empty reports whether nothing changed.
func (c Change) Empty() bool {
	return len(c.Created)+len(c.Modified)+len(c.Removed)+len(c.Protected) == 0 && c.Omitted == 0
}

// Diff compares two snapshots of the same project.
func Diff(before, after *Fingerprint) Change {
	var c Change
	c.Incomplete = before.truncated || after.truncated
	c.Created, c.Modified, c.Removed = compare(before.files, after.files)
	pCreated, pModified, pRemoved := compare(before.protected, after.protected)
	c.Protected = append(append(append([]string{}, pCreated...), pModified...), pRemoved...)
	sort.Strings(c.Protected)
	for _, list := range []*[]string{&c.Created, &c.Modified, &c.Removed, &c.Protected} {
		if len(*list) > MaxReported {
			c.Omitted += len(*list) - MaxReported
			*list = (*list)[:MaxReported]
		}
	}
	return c
}

func compare(before, after map[string]sig) (created, modified, removed []string) {
	for p, now := range after {
		was, ok := before[p]
		switch {
		case !ok:
			created = append(created, p)
		case was != now:
			modified = append(modified, p)
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			removed = append(removed, p)
		}
	}
	sort.Strings(created)
	sort.Strings(modified)
	sort.Strings(removed)
	return created, modified, removed
}
