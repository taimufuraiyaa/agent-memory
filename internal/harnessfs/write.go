package harnessfs

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// MaxWriteBytes bounds any file the harness creates, replaces or reads back for a write.
	MaxWriteBytes = 256 << 10
	// AbsentRevision is the revision of a file that does not exist.
	AbsentRevision = "absent"
)

var (
	// ErrExists: something is already at the path, so a create refuses.
	ErrExists = errors.New("path already exists")
	// ErrStale: the file no longer has the revision that was reviewed.
	ErrStale = errors.New("file changed since it was reviewed")
	// ErrTooLarge: the content exceeds MaxWriteBytes.
	ErrTooLarge = errors.New("content is too large")
)

// writeLock serializes every write in this process, so the revision recheck and the
// replacement that follows it cannot interleave with another harness write. A different
// process can still change a file in the gap; callers state that limit.
var writeLock sync.Mutex

// Revision identifies bytes exactly as Read does for a file that was read in full.
func Revision(data []byte) string { return revisionOf(data, int64(len(data))) }

func revisionOf(data []byte, size int64) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16] + fmt.Sprintf("-%d", size)
}

// Info describes what is at a path, without following a link.
type Info struct {
	Exists  bool
	Regular bool
	Dir     bool
	Size    int64
	Mode    fs.FileMode
}

// Inspect reports what is at a path. A hidden or credential-like name, a path outside
// the root and a path with a link in an existing component are refused.
func (r *Root) Inspect(rel string) (Info, error) {
	cleaned, err := r.check(rel)
	if err != nil {
		return Info{}, err
	}
	if cleaned == "." {
		return Info{}, ErrInvalidPath
	}
	info, err := r.root.Lstat(cleaned)
	if errors.Is(err, os.ErrNotExist) {
		return Info{}, nil
	}
	if err != nil {
		return Info{}, ErrUnreadable
	}
	return Info{Exists: true, Regular: info.Mode().IsRegular(), Dir: info.IsDir(), Size: info.Size(), Mode: info.Mode().Perm()}, nil
}

// readWhole reads a regular text file in full, refusing one that would not fit.
func (r *Root) readWhole(cleaned string) ([]byte, fs.FileMode, error) {
	info, err := r.root.Lstat(cleaned)
	if err != nil || !info.Mode().IsRegular() {
		return nil, 0, ErrUnreadable
	}
	if info.Size() > MaxWriteBytes {
		return nil, 0, ErrTooLarge
	}
	file, err := r.root.Open(cleaned)
	if err != nil {
		return nil, 0, ErrUnreadable
	}
	defer file.Close()
	buffer := make([]byte, 0, info.Size()+1)
	chunk := make([]byte, 32<<10)
	for {
		n, err := file.Read(chunk)
		buffer = append(buffer, chunk[:n]...)
		if len(buffer) > MaxWriteBytes {
			return nil, 0, ErrTooLarge
		}
		if err != nil {
			break
		}
	}
	return buffer, info.Mode().Perm(), nil
}

// ReadWhole reads one regular file completely, up to MaxWriteBytes, together with its
// revision and permission bits. It is what an edit is computed from.
func (r *Root) ReadWhole(rel string) (data []byte, revision string, mode fs.FileMode, err error) {
	cleaned, err := r.check(rel)
	if err != nil {
		return nil, "", 0, err
	}
	data, mode, err = r.readWhole(cleaned)
	if err != nil {
		return nil, "", 0, err
	}
	if bytesHaveNUL(data) {
		return nil, "", 0, ErrBinary
	}
	return data, Revision(data), mode, nil
}

func bytesHaveNUL(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), binarySniff)], 0) >= 0
}

// verify rereads a file and compares its revision with the one that was reviewed.
func (r *Root) verify(cleaned, expect string) error {
	data, _, err := r.readWhole(cleaned)
	if err != nil {
		if errors.Is(err, ErrUnreadable) || errors.Is(err, ErrTooLarge) {
			return ErrStale
		}
		return err
	}
	if Revision(data) != expect {
		return ErrStale
	}
	return nil
}

func tempName(dir string) (string, error) {
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	name := ".am-tmp-" + hex.EncodeToString(token[:])
	if dir == "." || dir == "" {
		return name, nil
	}
	return path.Join(dir, name), nil
}

// ReplaceFile atomically replaces an existing regular file. expect is the revision the
// caller reviewed: the file is reread immediately before the replacement and the call
// fails with ErrStale, changing nothing, if it differs. The new content is written to a
// temporary file in the same directory with the original permission bits, so the
// replacement is one rename and a reader never sees a partial file.
func (r *Root) ReplaceFile(rel string, data []byte, expect string) error {
	cleaned, err := r.check(rel)
	if err != nil {
		return err
	}
	if cleaned == "." {
		return ErrInvalidPath
	}
	if len(data) > MaxWriteBytes {
		return ErrTooLarge
	}
	writeLock.Lock()
	defer writeLock.Unlock()
	if err := r.verify(cleaned, expect); err != nil {
		return err
	}
	info, err := r.root.Lstat(cleaned)
	if err != nil {
		return ErrStale
	}
	tmp, err := tempName(path.Dir(filepath.ToSlash(cleaned)))
	if err != nil {
		return ErrUnreadable
	}
	file, err := r.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrUnreadable
	}
	cleanup := func() { _ = r.root.Remove(tmp) }
	if _, err := file.Write(data); err != nil {
		file.Close()
		cleanup()
		return ErrUnreadable
	}
	if err := file.Sync(); err != nil {
		file.Close()
		cleanup()
		return ErrUnreadable
	}
	if err := file.Close(); err != nil {
		cleanup()
		return ErrUnreadable
	}
	if err := r.root.Chmod(tmp, info.Mode().Perm()); err != nil {
		cleanup()
		return ErrUnreadable
	}
	// The recheck that matters is the last one before the replacement.
	if err := r.verify(cleaned, expect); err != nil {
		cleanup()
		return err
	}
	if err := r.root.Rename(tmp, cleaned); err != nil {
		cleanup()
		return ErrUnreadable
	}
	return nil
}

// createHook lets a test force a failure after directories exist, to prove they are
// removed again. It is nil outside tests.
var createHook func(stage string) error

// CreateFile creates a new regular file and any missing parent directories, and never
// replaces anything: if something is already at the path it fails with ErrExists. On a
// failure everything it made is removed. It returns the directories it created.
func (r *Root) CreateFile(rel string, data []byte, mode fs.FileMode) (made []string, err error) {
	cleaned, err := r.check(rel)
	if err != nil {
		return nil, err
	}
	if cleaned == "." {
		return nil, ErrInvalidPath
	}
	if len(data) > MaxWriteBytes {
		return nil, ErrTooLarge
	}
	writeLock.Lock()
	defer writeLock.Unlock()
	slash := filepath.ToSlash(cleaned)
	if _, err := r.root.Lstat(slash); err == nil {
		return nil, ErrExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, ErrUnreadable
	}
	var created []string
	rollback := func() {
		for i := len(created) - 1; i >= 0; i-- {
			_ = r.root.Remove(created[i])
		}
	}
	parts := strings.Split(slash, "/")
	prefix := ""
	for _, part := range parts[:len(parts)-1] {
		if prefix == "" {
			prefix = part
		} else {
			prefix += "/" + part
		}
		info, err := r.root.Lstat(prefix)
		switch {
		case err == nil && info.IsDir():
			continue
		case err == nil:
			rollback()
			return nil, ErrUnreadable // a file or link where a directory is needed
		case errors.Is(err, os.ErrNotExist):
			if err := r.root.Mkdir(prefix, 0o755); err != nil {
				rollback()
				return nil, ErrUnreadable
			}
			created = append(created, prefix)
		default:
			rollback()
			return nil, ErrUnreadable
		}
	}
	if createHook != nil {
		if err := createHook("dirs"); err != nil {
			rollback()
			return nil, err
		}
	}
	file, err := r.root.OpenFile(slash, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		rollback()
		if errors.Is(err, os.ErrExist) {
			return nil, ErrExists
		}
		return nil, ErrUnreadable
	}
	fail := func() {
		file.Close()
		_ = r.root.Remove(slash)
		rollback()
	}
	if _, err := file.Write(data); err != nil {
		fail()
		return nil, ErrUnreadable
	}
	if err := file.Sync(); err != nil {
		fail()
		return nil, ErrUnreadable
	}
	if err := file.Close(); err != nil {
		_ = r.root.Remove(slash)
		rollback()
		return nil, ErrUnreadable
	}
	return created, nil
}

// RemoveFile deletes one regular file whose revision still matches what was reviewed.
// It never removes a directory.
func (r *Root) RemoveFile(rel, expect string) error {
	cleaned, err := r.check(rel)
	if err != nil {
		return err
	}
	if cleaned == "." {
		return ErrInvalidPath
	}
	writeLock.Lock()
	defer writeLock.Unlock()
	if err := r.verify(cleaned, expect); err != nil {
		return err
	}
	if err := r.root.Remove(cleaned); err != nil {
		return ErrUnreadable
	}
	return nil
}
