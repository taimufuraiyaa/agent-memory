package harnessrun

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	harnessDir = "harness"
	runsDir    = "runs"
	idemDir    = "idem"
	keyFile    = "cursor.key"
	corruptExt = ".corrupt"
)

var idemFileRE = regexp.MustCompile(`^[a-f0-9]{64}\.json$`)

// FileStore keeps transient runs in a private directory tree under the data
// directory: one JSON file per run and one per idempotency key. It is separate from
// the durable memory databases, owned by a single Manager at a time.
type FileStore struct {
	root string
	runs string
	idem string
	key  []byte
}

type idemEntry struct {
	RunID      string    `json:"run_id"`
	RequestSHA string    `json:"request_sha256"`
	CreatedAt  time.Time `json:"created_at"`
}

// OpenStore creates the directory tree with owner-only permissions and loads, or
// creates, the secret that signs event cursors.
func OpenStore(dataDir string) (*FileStore, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errStorage("data directory is required")
	}
	s := &FileStore{root: filepath.Join(dataDir, harnessDir)}
	s.runs, s.idem = filepath.Join(s.root, runsDir), filepath.Join(s.root, idemDir)
	for _, dir := range []string{dataDir, s.root, s.runs, s.idem} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, errStorage("cannot create run directory")
		}
	}
	for _, dir := range []string{s.root, s.runs, s.idem} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			return nil, errStorage("unsafe run directory")
		}
	}
	key, err := s.loadKey()
	if err != nil {
		return nil, err
	}
	s.key = key
	return s, nil
}

func (s *FileStore) loadKey() ([]byte, error) {
	path := filepath.Join(s.root, keyFile)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		key := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, errStorage("cannot create cursor key")
		}
		if err := writeAtomic(s.root, path, key); err != nil {
			return nil, err
		}
		return key, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() != 32 {
		return nil, errStorage("unsafe cursor key")
	}
	key, err := os.ReadFile(path)
	if err != nil || len(key) != 32 {
		return nil, errStorage("cannot read cursor key")
	}
	return key, nil
}

func (s *FileStore) runPath(id string) (string, error) {
	if !runIDRE.MatchString(id) {
		return "", ErrNotFound
	}
	return filepath.Join(s.runs, id+".json"), nil
}

// Create writes a new run and fails if the ID already exists.
func (s *FileStore) Create(r Run) error {
	path, err := s.runPath(r.ID)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return errStorage("run already exists")
	}
	return s.write(path, r)
}

// Save atomically replaces an existing run.
func (s *FileStore) Save(r Run) error {
	path, err := s.runPath(r.ID)
	if err != nil {
		return err
	}
	return s.write(path, r)
}

func (s *FileStore) write(path string, r Run) error {
	r.SchemaVersion = schemaVersion
	data, err := json.Marshal(r)
	if err != nil || len(data) > maxRunFileBytes {
		return errStorage("run record too large")
	}
	return writeAtomic(s.runs, path, data)
}

// Load reads and validates one run. A damaged file is reported as ErrStorage so the
// caller can quarantine it; it is never partially trusted.
func (s *FileStore) Load(id string) (Run, error) {
	path, err := s.runPath(id)
	if err != nil {
		return Run{}, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Run{}, ErrNotFound
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > maxRunFileBytes {
		return Run{}, errStorage("unsafe run file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Run{}, errStorage("cannot read run file")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var run Run
	if decoder.Decode(&run) != nil || decoder.Decode(new(any)) != io.EOF {
		return Run{}, errStorage("malformed run file")
	}
	if err := run.validate(); err != nil || run.ID != id {
		return Run{}, errStorage("invalid run file")
	}
	return run, nil
}

// IDs lists stored run IDs; unrelated and quarantined files are ignored.
func (s *FileStore) IDs() ([]string, error) {
	entries, err := os.ReadDir(s.runs)
	if err != nil {
		return nil, errStorage("cannot list runs")
	}
	var ids []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".json") && runIDRE.MatchString(strings.TrimSuffix(name, ".json")) {
			ids = append(ids, strings.TrimSuffix(name, ".json"))
		}
	}
	return ids, nil
}

func (s *FileStore) Delete(id string) error {
	path, err := s.runPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errStorage("cannot delete run")
	}
	return nil
}

// Quarantine moves a damaged run aside so recovery never resumes or re-reads it.
func (s *FileStore) Quarantine(id string) error {
	path, err := s.runPath(id)
	if err != nil {
		return err
	}
	if err := os.Rename(path, path+corruptExt); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errStorage("cannot quarantine run")
	}
	return nil
}

func idemName(owner Owner, key string) string {
	sum := sha256.Sum256([]byte(owner.ClientID + "\x00" + owner.Workspace + "\x00" + key))
	return hex.EncodeToString(sum[:])
}

// PutIdem creates an idempotency record only if none exists. The record is written
// in full to a temporary file and published with a hard link, which fails if the name
// is taken, so a reader never sees a half-written record and two concurrent starts
// with one key resolve to a single winner. When one exists it is returned unchanged
// and created is false.
func (s *FileStore) PutIdem(name string, entry idemEntry) (existing idemEntry, created bool, err error) {
	path := filepath.Join(s.idem, name+".json")
	data, err := json.Marshal(entry)
	if err != nil {
		return idemEntry{}, false, errStorage("cannot encode idempotency record")
	}
	temp, err := os.CreateTemp(s.idem, ".idem-*.tmp")
	if err != nil {
		return idemEntry{}, false, errStorage("cannot create idempotency record")
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return idemEntry{}, false, errStorage("cannot secure idempotency record")
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return idemEntry{}, false, errStorage("cannot write idempotency record")
	}
	if err := temp.Close(); err != nil {
		return idemEntry{}, false, errStorage("cannot close idempotency record")
	}
	switch err := os.Link(temp.Name(), path); {
	case err == nil:
		return entry, true, nil
	case errors.Is(err, os.ErrExist):
		existing, err = s.GetIdem(name)
		return existing, false, err
	default:
		return idemEntry{}, false, errStorage("cannot publish idempotency record")
	}
}

// errIdemGone reports that an idempotency record disappeared while it was awaited.
var errIdemGone = errors.New("idempotency record is gone")

func (s *FileStore) GetIdem(name string) (idemEntry, error) {
	content, err := os.ReadFile(filepath.Join(s.idem, name+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return idemEntry{}, errIdemGone
	}
	if err != nil {
		return idemEntry{}, errStorage("cannot read idempotency record")
	}
	var entry idemEntry
	if json.Unmarshal(content, &entry) != nil || !runIDRE.MatchString(entry.RunID) || len(entry.RequestSHA) != 64 {
		return idemEntry{}, errStorage("invalid idempotency record")
	}
	return entry, nil
}

func (s *FileStore) DeleteIdem(name string) {
	_ = os.Remove(filepath.Join(s.idem, name+".json"))
}

// SweepIdem removes idempotency records older than cutoff and those whose run is gone.
func (s *FileStore) SweepIdem(cutoff time.Time, exists func(id string) bool) int {
	entries, err := os.ReadDir(s.idem)
	if err != nil {
		return 0
	}
	removed := 0
	for _, entry := range entries {
		if !idemFileRE.MatchString(entry.Name()) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		record, err := s.GetIdem(name)
		if err != nil || record.CreatedAt.Before(cutoff) || !exists(record.RunID) {
			s.DeleteIdem(name)
			removed++
		}
	}
	return removed
}

func writeAtomic(dir, path string, data []byte) error {
	temp, err := os.CreateTemp(dir, ".harness-*.tmp")
	if err != nil {
		return errStorage("cannot create temporary file")
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return errStorage("cannot secure temporary file")
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return errStorage("cannot write temporary file")
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return errStorage("cannot sync temporary file")
	}
	if err := temp.Close(); err != nil {
		return errStorage("cannot close temporary file")
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return errStorage("cannot replace file")
	}
	return nil
}

// cursor is opaque and bound to one run and client: an 8-byte sequence followed by
// a truncated HMAC over run, client and sequence.
func (s *FileStore) encodeCursor(runID, clientID string, seq uint64) string {
	raw := make([]byte, 8, 24)
	binary.BigEndian.PutUint64(raw, seq)
	raw = append(raw, s.mac(runID, clientID, raw[:8])...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (s *FileStore) decodeCursor(runID, clientID, cursor string) (uint64, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) != 24 || !hmac.Equal(raw[8:], s.mac(runID, clientID, raw[:8])) {
		return 0, ErrInvalidCursor
	}
	return binary.BigEndian.Uint64(raw[:8]), nil
}

func (s *FileStore) mac(runID, clientID string, seq []byte) []byte {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte(runID))
	h.Write([]byte{0})
	h.Write([]byte(clientID))
	h.Write([]byte{0})
	h.Write(seq)
	return h.Sum(nil)[:16]
}
