package harnessapproval

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxAuditBytes = 4 << 20

// auditLine is one content-free fact. Lines are chained: each carries the hash of the
// line before it, so removing, reordering or editing a line is detectable.
type auditLine struct {
	Seq       uint64    `json:"seq"`
	At        time.Time `json:"at"`
	Event     string    `json:"event"`
	Approval  string    `json:"approval"`
	Run       string    `json:"run"`
	Workspace string    `json:"workspace"`
	Client    string    `json:"client"`
	Tool      string    `json:"tool"`
	Digest    string    `json:"digest"`
	Friction  string    `json:"friction"`
	Paths     []string  `json:"paths"`
	State     string    `json:"state"`
	Detail    string    `json:"detail,omitempty"`
	Prev      string    `json:"prev"`
	Hash      string    `json:"hash"`
}

func (l auditLine) seal() string {
	l.Hash = ""
	body, _ := json.Marshal(l)
	sum := sha256.Sum256(append([]byte(l.Prev+"\n"), body...))
	return hex.EncodeToString(sum[:])
}

func (s *Store) auditPath(dir string) string { return filepath.Join(dir, auditName) }

// lastLine returns the final audit line, reading only the tail of the file.
func lastLine(path string) (auditLine, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return auditLine{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return auditLine{}, false, errStorage("unsafe audit file")
	}
	file, err := os.Open(path)
	if err != nil {
		return auditLine{}, false, errStorage("cannot read audit file")
	}
	defer file.Close()
	const tail = 16 << 10
	start := max(0, info.Size()-tail)
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return auditLine{}, false, errStorage("cannot read audit file")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return auditLine{}, false, errStorage("cannot read audit file")
	}
	data = bytes.TrimRight(data, "\n")
	if len(data) == 0 {
		return auditLine{}, false, nil
	}
	if i := bytes.LastIndexByte(data, '\n'); i >= 0 {
		data = data[i+1:]
	}
	var line auditLine
	if json.Unmarshal(data, &line) != nil || line.Hash == "" || line.Hash != line.seal() {
		return auditLine{}, false, errStorage("malformed audit file")
	}
	return line, true, nil
}

// auditHook lets a test fail one event after the health check has passed, to prove an
// approval that could not be recorded is removed. It is nil outside tests.
var auditHook func(event string) error

// audit appends one line for a record. It must be called with the directory locked.
func (s *Store) audit(dir string, record Record, event, detail string) error {
	if !reasonRE.MatchString(event) || (detail != "" && !reasonRE.MatchString(detail)) {
		return fmt.Errorf("%w: audit event", ErrInvalid)
	}
	if auditHook != nil {
		if err := auditHook(event); err != nil {
			return err
		}
	}
	path := s.auditPath(dir)
	previous, found, err := lastLine(path)
	if err != nil {
		return err
	}
	if found {
		if info, err := os.Lstat(path); err == nil && info.Size() > s.maxAudit {
			rotated := filepath.Join(dir, "audit-"+s.now().UTC().Format("20060102T150405.000000000Z")+".jsonl")
			if err := os.Rename(path, rotated); err != nil {
				return errStorage("cannot rotate audit file")
			}
		}
	}
	line := auditLine{Seq: previous.Seq + 1, At: s.now().UTC(), Event: event, Approval: record.ID, Run: record.RunID, Workspace: record.Owner.Workspace,
		Client: record.Owner.ClientID, Tool: record.Tool, Digest: record.Digest, Friction: string(record.Friction), Paths: record.Paths,
		State: string(record.State), Detail: detail, Prev: previous.Hash}
	if line.Paths == nil {
		line.Paths = []string{}
	}
	line.Hash = line.seal()
	data, err := json.Marshal(line)
	if err != nil {
		return errStorage("cannot encode audit line")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return errStorage("cannot open audit file")
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return errStorage("cannot write audit file")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errStorage("cannot sync audit file")
	}
	return file.Close()
}

// ErrAuditBroken reports a chain that does not verify.
var ErrAuditBroken = errors.New("audit chain is broken")

// VerifyAudit checks every audit file in order and returns the number of lines. It fails
// at the first line whose hash, link to the previous line or sequence is wrong.
func (s *Store) VerifyAudit(ctx context.Context) (int, error) {
	var count int
	err := s.locked(ctx, func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return errStorage("cannot list approvals")
		}
		var files []string
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "audit-") && strings.HasSuffix(entry.Name(), ".jsonl") {
				files = append(files, entry.Name())
			}
		}
		sort.Strings(files)
		if _, err := os.Lstat(filepath.Join(dir, auditName)); err == nil {
			files = append(files, auditName)
		}
		prev, seq := "", uint64(0)
		for _, name := range files {
			info, err := os.Lstat(filepath.Join(dir, name))
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
				return errStorage("unsafe audit file")
			}
			file, err := os.Open(filepath.Join(dir, name))
			if err != nil {
				return errStorage("cannot read audit file")
			}
			reader := bufio.NewReaderSize(file, 64<<10)
			for {
				raw, err := reader.ReadBytes('\n')
				if len(bytes.TrimSpace(raw)) > 0 {
					var line auditLine
					decoder := json.NewDecoder(bytes.NewReader(raw))
					decoder.DisallowUnknownFields()
					if decoder.Decode(&line) != nil || line.Hash != line.seal() || line.Prev != prev || line.Seq != seq+1 {
						file.Close()
						return fmt.Errorf("%w at line %d", ErrAuditBroken, seq+1)
					}
					prev, seq = line.Hash, line.Seq
					count++
				}
				if err != nil {
					break
				}
			}
			file.Close()
		}
		return nil
	})
	return count, err
}
