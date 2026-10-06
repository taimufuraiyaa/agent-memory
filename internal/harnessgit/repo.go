package harnessgit

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Repo is a repository that passed the acceptance check.
type Repo struct {
	// Root is the project's real path; GitDir is Root's .git directory.
	Root   string
	GitDir string
}

// redirecting files make Git read objects, configuration or a work tree from somewhere
// other than the directory that was checked.
var redirecting = []string{".git/commondir", ".git/objects/info/alternates", ".git/config.worktree", ".git/gitdir"}

// mustBeRealDirs and mustBeRealFiles are looked at without following links: a link here would
// let Git read a different repository from the one that was checked.
var (
	mustBeRealDirs  = []string{".git", ".git/objects", ".git/refs"}
	mustBeRealFiles = []string{".git/HEAD", ".git/config"}
	mayBeRealFiles  = []string{".git/index", ".git/packed-refs", ".git/shallow"}
)

// Open checks that root contains a repository Git can be run on safely and returns it. It
// refuses a repository that is not a real directory directly under root, that redirects Git
// elsewhere, or whose configuration names anything that can start a program or reach beyond
// the project (see ScanConfig). It never runs Git.
func Open(root string) (*Repo, error) {
	real, err := filepath.EvalSymlinks(root)
	if err != nil || !filepath.IsAbs(real) {
		return nil, refuse("root_unreadable", "")
	}
	project, err := os.OpenRoot(real)
	if err != nil {
		return nil, refuse("root_unreadable", "")
	}
	defer project.Close()
	if _, err := project.Lstat(".git"); errors.Is(err, fs.ErrNotExist) {
		return nil, refuse("not_a_repository", "")
	}
	for _, dir := range mustBeRealDirs {
		info, err := project.Lstat(dir)
		if err != nil || !info.IsDir() {
			return nil, refuse("git_indirect", dir)
		}
	}
	for _, file := range mustBeRealFiles {
		info, err := project.Lstat(file)
		if err != nil || !info.Mode().IsRegular() {
			return nil, refuse("git_indirect", file)
		}
	}
	for _, file := range mayBeRealFiles {
		if info, err := project.Lstat(file); err == nil && !info.Mode().IsRegular() {
			return nil, refuse("git_indirect", file)
		}
	}
	for _, file := range redirecting {
		if _, err := project.Lstat(file); err == nil {
			return nil, refuse("git_indirect", file)
		}
	}
	file, err := project.Open(".git/config")
	if err != nil {
		return nil, refuse("config_unreadable", "")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, refuse("config_unreadable", "")
	}
	if _, err := ScanConfig(data); err != nil {
		return nil, err
	}
	return &Repo{Root: real, GitDir: filepath.Join(real, ".git")}, nil
}
