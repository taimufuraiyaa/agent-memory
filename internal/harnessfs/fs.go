// Package harnessfs is the one place the harness touches project files. Every read goes
// through an operating-system root-confined handle, so a symlink or `..` cannot reach
// outside the registered project root, and every path is refused if it names a hidden
// entry or something that usually holds credentials. Only regular text files are opened,
// so a FIFO or device cannot hang a reader, and every read is size-bounded.
package harnessfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// MaxPathBytes bounds one relative path.
	MaxPathBytes = 512
	// MaxListEntries bounds one directory listing.
	MaxListEntries = 500
	binarySniff    = 8192
)

var (
	// ErrInvalidPath: not a local relative path (absolute, parent traversal, NUL, too long).
	ErrInvalidPath = errors.New("invalid path")
	// ErrDenied: the path names a hidden entry or a credential-like file.
	ErrDenied = errors.New("path is not readable")
	// ErrBinary: the file looks binary.
	ErrBinary = errors.New("file is binary")
	// ErrUnreadable: missing, not a regular file, or not readable inside the root.
	ErrUnreadable = errors.New("file is not readable")
)

// Root is a project root opened for confined access.
type Root struct {
	root *os.Root
	path string
}

// Open opens the project root. The returned handle confines every later access to it.
func Open(path string) (*Root, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot open project root", ErrUnreadable)
	}
	return &Root{root: root, path: path}, nil
}

func (r *Root) Close() error { return r.root.Close() }

// Clean validates and normalizes a relative path. The empty path and "." name the root.
func Clean(rel string) (string, error) {
	if len(rel) > MaxPathBytes || strings.ContainsRune(rel, 0) {
		return "", ErrInvalidPath
	}
	if rel == "" {
		rel = "."
	}
	cleaned := filepath.Clean(rel)
	if !filepath.IsLocal(cleaned) && cleaned != "." {
		return "", ErrInvalidPath
	}
	return cleaned, nil
}

// Denied reports whether any component is hidden or credential-like. The root itself is not.
func Denied(rel string) bool {
	if rel == "." || rel == "" {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if deniedName(part) {
			return true
		}
	}
	return false
}

func deniedName(name string) bool {
	lower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(lower, "."):
		return true
	case strings.HasPrefix(lower, "id_rsa"), strings.HasPrefix(lower, "id_ed25519"), strings.HasPrefix(lower, "id_ecdsa"),
		strings.HasPrefix(lower, "credentials"), strings.HasPrefix(lower, "secrets"), strings.HasPrefix(lower, "secret."):
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".kdbx"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

func (r *Root) check(rel string) (string, error) {
	cleaned, err := Clean(rel)
	if err != nil {
		return "", err
	}
	if Denied(cleaned) {
		return "", ErrDenied
	}
	if err := r.noLinks(cleaned); err != nil {
		return "", err
	}
	return cleaned, nil
}

// noLinks refuses a path with a symlink in any component that exists. The operating
// system already stops a link from leaving the root, but a link inside the root can
// still name a protected target (`notes.txt` -> `.env`, `src` -> `.git`) that the name
// checks never see, so links are never followed. A component that does not exist yet is
// not an error here: creating a new file is checked against its existing parents.
func (r *Root) noLinks(rel string) error {
	if rel == "." {
		return nil
	}
	prefix := ""
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if prefix == "" {
			prefix = part
		} else {
			prefix += "/" + part
		}
		info, err := r.root.Lstat(prefix)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return ErrUnreadable
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrDenied
		}
	}
	return nil
}

// File is the result of one bounded read.
type File struct {
	Data []byte
	// Revision identifies the exact bytes read and the file size, so a change is detectable.
	Revision  string
	Size      int64
	Truncated bool
}

// Exists reports whether an entry is present, without following it, so a symlink the root
// refuses to follow is still seen as present. It performs no path policy.
func (r *Root) Exists(rel string) bool {
	cleaned, err := Clean(rel)
	if err != nil {
		return false
	}
	_, err = r.root.Lstat(cleaned)
	return err == nil
}

// Read reads at most limit bytes of one regular text file.
func (r *Root) Read(rel string, limit int) (File, error) {
	cleaned, err := r.check(rel)
	if err != nil {
		return File{}, err
	}
	info, err := r.root.Stat(cleaned)
	if err != nil || !info.Mode().IsRegular() {
		return File{}, ErrUnreadable
	}
	file, err := r.root.Open(cleaned)
	if err != nil {
		return File{}, ErrUnreadable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)))
	if err != nil {
		return File{}, ErrUnreadable
	}
	if bytes.IndexByte(data[:min(len(data), binarySniff)], 0) >= 0 {
		return File{}, ErrBinary
	}
	return File{Data: data, Revision: revisionOf(data, info.Size()), Size: info.Size(), Truncated: info.Size() > int64(len(data))}, nil
}

// Entry is one directory entry. Symlinks, devices and other non-regular files are not
// listed as files: they are reported as "other" so a caller can see they exist without
// being able to read through them.
type Entry struct {
	Name string
	Kind string // "file", "dir" or "other"
	Size int64
}

// ReadDir lists one directory, sorted by name, omitting hidden and credential-like entries.
// More than max entries are cut and reported as truncated.
func (r *Root) ReadDir(rel string, max int) ([]Entry, bool, error) {
	cleaned, err := r.check(rel)
	if err != nil {
		return nil, false, err
	}
	if max < 1 || max > MaxListEntries {
		max = MaxListEntries
	}
	dir, err := r.root.Open(cleaned)
	if err != nil {
		return nil, false, ErrUnreadable
	}
	defer dir.Close()
	if info, err := dir.Stat(); err != nil || !info.IsDir() {
		return nil, false, ErrUnreadable
	}
	raw, err := dir.ReadDir(-1)
	if err != nil {
		return nil, false, ErrUnreadable
	}
	sort.Slice(raw, func(i, j int) bool { return raw[i].Name() < raw[j].Name() })
	var entries []Entry
	for _, e := range raw {
		if deniedName(e.Name()) {
			continue
		}
		entry := Entry{Name: e.Name(), Kind: "other"}
		switch {
		case e.Type().IsDir():
			entry.Kind = "dir"
		case e.Type().IsRegular():
			entry.Kind = "file"
			if info, err := e.Info(); err == nil {
				entry.Size = info.Size()
			}
		}
		entries = append(entries, entry)
	}
	if len(entries) > max {
		return entries[:max], true, nil
	}
	return entries, false, nil
}

// WalkOptions bound a directory walk.
type WalkOptions struct {
	MaxDepth int
	MaxFiles int
	// SkipDirs are directory names never entered, such as build output.
	SkipDirs map[string]bool
}

// ErrStop ends a walk early without it being an error.
var ErrStop = errors.New("stop walk")

// Walk visits regular, non-hidden files under rel in a fixed order. It never follows
// symlinks and never enters hidden or skipped directories. visit receives the path
// relative to the root. A visit error other than ErrStop aborts the walk.
func (r *Root) Walk(rel string, opts WalkOptions, visit func(path string, size int64) error) (visited int, truncated bool, err error) {
	cleaned, err := r.check(rel)
	if err != nil {
		return 0, false, err
	}
	if opts.MaxDepth < 1 {
		opts.MaxDepth = 20
	}
	if opts.MaxFiles < 1 {
		opts.MaxFiles = 5000
	}
	stopped := false
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		handle, err := r.root.Open(dir)
		if err != nil {
			return nil // an unreadable directory is skipped, not fatal
		}
		raw, err := handle.ReadDir(-1)
		handle.Close()
		if err != nil {
			return nil
		}
		sort.Slice(raw, func(i, j int) bool { return raw[i].Name() < raw[j].Name() })
		for _, e := range raw {
			name := e.Name()
			if deniedName(name) {
				continue
			}
			path := name
			if dir != "." {
				path = dir + "/" + name
			}
			switch {
			case e.Type().IsDir():
				if opts.SkipDirs[name] || depth >= opts.MaxDepth {
					continue
				}
				if err := walk(path, depth+1); err != nil || stopped {
					return err
				}
			case e.Type().IsRegular():
				if visited >= opts.MaxFiles {
					truncated, stopped = true, true
					return nil
				}
				var size int64
				if info, err := e.Info(); err == nil {
					size = info.Size()
				}
				visited++
				if err := visit(path, size); err != nil {
					if errors.Is(err, ErrStop) {
						stopped = true
						return nil
					}
					return err
				}
			}
		}
		return nil
	}
	if err := walk(cleaned, 1); err != nil {
		return visited, truncated, err
	}
	return visited, truncated, nil
}
