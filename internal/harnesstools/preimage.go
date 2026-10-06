package harnesstools

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessfs"
)

const (
	preimageVersion   = 1
	preimageRetention = 7 * 24 * time.Hour
	maxPreimages      = 500
	maxPreimageBytes  = 2 << 20
)

var (
	// ErrNoPreimage: nothing was saved for that action.
	ErrNoPreimage = errors.New("no saved copy for that action")
	// ErrNotRestorable: the file is no longer in the state the action left it in, or the
	// action was applied to a different project, so restoring could destroy later work.
	ErrNotRestorable = errors.New("the file changed after that action")
	// ErrUndone: the action was already undone.
	ErrUndone = errors.New("that action was already undone")

	hexDigestRE = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// preimage is the saved state from before one applied action, enough to undo it.
type preimage struct {
	Version      int       `json:"version"`
	Digest       string    `json:"digest"`
	Workspace    string    `json:"workspace"`
	Root         string    `json:"root"`
	Tool         string    `json:"tool"`
	Path         string    `json:"path"`
	Existed      bool      `json:"existed"`
	PreRevision  string    `json:"pre_revision"`
	PostRevision string    `json:"post_revision"`
	Mode         uint32    `json:"mode"`
	Data         []byte    `json:"data,omitempty"`
	CreatedDirs  []string  `json:"created_dirs,omitempty"`
	AppliedAt    time.Time `json:"applied_at"`
	UndoneAt     time.Time `json:"undone_at,omitzero"`
}

func preimagePath(dir, digest string) (string, error) {
	hexPart := strings.TrimPrefix(digest, "sha256:")
	if !hexDigestRE.MatchString(hexPart) {
		return "", ErrNoPreimage
	}
	return filepath.Join(dir, hexPart+".json"), nil
}

// privateDir creates the directory with owner-only access and refuses a link or a loose mode.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("unsafe saved-copy directory")
	}
	return nil
}

// writePreimage saves a record under its final name.
func writePreimage(dir string, p preimage) error { return savePreimage(dir, p, ".json") }

// stagePreimage saves a record under a pending name. It becomes the action's saved copy
// only when the change really applied, so a stale or failed attempt can never replace the
// saved copy of an earlier, successful one.
func stagePreimage(dir string, p preimage) error { return savePreimage(dir, p, ".pending") }

func commitPreimage(dir, digest string) error {
	pending, err := preimagePath(dir, digest)
	if err != nil {
		return err
	}
	pending = strings.TrimSuffix(pending, ".json") + ".pending"
	final := strings.TrimSuffix(pending, ".pending") + ".json"
	return os.Rename(pending, final)
}

func discardPreimage(dir, digest string) {
	if pending, err := preimagePath(dir, digest); err == nil {
		_ = os.Remove(strings.TrimSuffix(pending, ".json") + ".pending")
	}
}

func savePreimage(dir string, p preimage, suffix string) error {
	if err := privateDir(dir); err != nil {
		return err
	}
	path, err := preimagePath(dir, p.Digest)
	if err != nil {
		return err
	}
	path = strings.TrimSuffix(path, ".json") + suffix
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode().Perm() != 0o600) {
		return errors.New("unsafe saved-copy file")
	}
	data, err := json.Marshal(p)
	if err != nil || len(data) > maxPreimageBytes {
		return errors.New("cannot encode saved copy")
	}
	temp, err := os.CreateTemp(dir, ".preimage-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func readPreimage(dir, digest string) (preimage, error) {
	path, err := preimagePath(dir, digest)
	if err != nil {
		return preimage{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return preimage{}, ErrNoPreimage
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > maxPreimageBytes {
		return preimage{}, errors.New("unsafe saved-copy file")
	}
	file, err := os.Open(path)
	if err != nil {
		return preimage{}, ErrNoPreimage
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, maxPreimageBytes+1))
	if err != nil || len(content) > maxPreimageBytes {
		return preimage{}, errors.New("cannot read saved copy")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var p preimage
	if decoder.Decode(&p) != nil || p.Version != preimageVersion || p.Digest != digest {
		return preimage{}, errors.New("malformed saved copy")
	}
	return p, nil
}

// prunePreimages removes saved copies that are old or beyond the bound, oldest first.
func prunePreimages(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type saved struct {
		path string
		mod  time.Time
	}
	var kept []saved
	for _, entry := range entries {
		name := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".json"), ".pending")
		if (!strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".pending")) || !hexDigestRE.MatchString(name) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if now.Sub(info.ModTime()) > preimageRetention || (strings.HasSuffix(entry.Name(), ".pending") && now.Sub(info.ModTime()) > time.Hour) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
			continue
		}
		kept = append(kept, saved{filepath.Join(dir, entry.Name()), info.ModTime()})
	}
	if len(kept) <= maxPreimages {
		return
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].mod.Before(kept[j].mod) })
	for _, old := range kept[:len(kept)-maxPreimages] {
		_ = os.Remove(old.path)
	}
}

// UndoResult says what an undo restored.
type UndoResult struct {
	Tool string
	Path string
}

// Undo restores the state from before an applied action, but only if the file is still
// exactly as that action left it and the action was applied to this project root: if
// anything changed since, restoring would overwrite later work, so it refuses. A created
// file is removed (directories made for it are left), an edited file gets its previous
// content back, and a deleted file is recreated with its previous content and mode.
func Undo(preimageDir, root, digest string, now time.Time) (UndoResult, error) {
	pre, err := readPreimage(preimageDir, digest)
	if err != nil {
		return UndoResult{}, err
	}
	if !pre.UndoneAt.IsZero() {
		return UndoResult{}, ErrUndone
	}
	if pre.Root != root {
		return UndoResult{}, ErrNotRestorable
	}
	project, err := harnessfs.Open(root)
	if err != nil {
		return UndoResult{}, ErrNotRestorable
	}
	defer project.Close()
	result := UndoResult{Tool: pre.Tool, Path: pre.Path}
	switch pre.Tool {
	case ToolEditFile:
		_, revision, _, err := project.ReadWhole(pre.Path)
		if err != nil || revision != pre.PostRevision {
			return UndoResult{}, ErrNotRestorable
		}
		if err := project.ReplaceFile(pre.Path, pre.Data, pre.PostRevision); err != nil {
			return UndoResult{}, ErrNotRestorable
		}
		if _, revision, _, err := project.ReadWhole(pre.Path); err != nil || revision != pre.PreRevision {
			return UndoResult{}, errors.New("the restored file did not verify")
		}
	case ToolCreateFile:
		if _, revision, _, err := project.ReadWhole(pre.Path); err != nil || revision != pre.PostRevision {
			return UndoResult{}, ErrNotRestorable
		}
		if err := project.RemoveFile(pre.Path, pre.PostRevision); err != nil {
			return UndoResult{}, ErrNotRestorable
		}
	case ToolDeleteFile:
		if info, err := project.Inspect(pre.Path); err != nil || info.Exists {
			return UndoResult{}, ErrNotRestorable
		}
		if _, err := project.CreateFile(pre.Path, pre.Data, fs.FileMode(pre.Mode)); err != nil {
			return UndoResult{}, ErrNotRestorable
		}
		if _, revision, _, err := project.ReadWhole(pre.Path); err != nil || revision != pre.PreRevision {
			return UndoResult{}, errors.New("the restored file did not verify")
		}
	default:
		return UndoResult{}, ErrNoPreimage
	}
	pre.UndoneAt = now.UTC()
	if err := writePreimage(preimageDir, pre); err != nil {
		return result, errors.New("restored, but could not record that")
	}
	return result, nil
}
