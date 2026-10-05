// Package harnessapproval is the trusted local channel for approving harness actions.
// An approval is a record in a private directory, written by the local approval command
// after a person has reviewed the change at a terminal and typed a short code. Nothing
// reachable through a bearer grant, an HTTP route or an MCP tool can create a decision:
// the run manager only ever reads decisions from this store. A record binds the run, the
// run's generation, the client and grant, the exact normalized arguments and an action
// digest that covers the revision of every file the action touches, so what a person
// reviewed is what runs or nothing runs.
package harnessapproval

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	schemaVersion = 1
	dirName       = "approvals"
	lockName      = ".lock"
	auditName     = "audit.jsonl"
	maxRecord     = 256 << 10
	lockStaleAge  = 30 * time.Second
	lockWait      = 3 * time.Second

	// MaxLive bounds records that are pending or approved, so the directory cannot be flooded.
	MaxLive = 64
	// MaxRecords bounds the records kept, resolved ones included, before the oldest are removed.
	MaxRecords = 400
	// MaxPaths, MaxPreviewBytes and MaxArgumentBytes bound what one record holds.
	MaxPaths         = 32
	MaxPreviewBytes  = 16 << 10
	MaxArgumentBytes = 96 << 10
	maxSummaryBytes  = 256

	DefaultTTL       = 30 * time.Minute
	DefaultRetention = 7 * 24 * time.Hour
	// MaxCodeFailures wrong codes deny the approval, so a script cannot guess one.
	MaxCodeFailures = 5
)

type State string

const (
	StatePending    State = "pending"
	StateApproved   State = "approved"
	StateDenied     State = "denied"
	StateConsumed   State = "consumed"
	StateExpired    State = "expired"
	StateRevoked    State = "revoked"
	StateSuperseded State = "superseded"
)

func (s State) valid() bool {
	switch s {
	case StatePending, StateApproved, StateDenied, StateConsumed, StateExpired, StateRevoked, StateSuperseded:
		return true
	}
	return false
}

// Live reports whether the approval can still lead to an action.
func (s State) Live() bool { return s == StatePending || s == StateApproved }

type Friction string

const (
	FrictionStandard Friction = "standard"
	FrictionStrict   Friction = "strict"
)

var (
	ErrNotFound  = errors.New("approval not found")
	ErrNotLive   = errors.New("approval is not waiting")
	ErrExpired   = errors.New("approval expired")
	ErrCode      = errors.New("the code does not match")
	ErrBusy      = errors.New("too many approvals are waiting")
	ErrInvalid   = errors.New("invalid approval")
	ErrStorage   = errors.New("approval storage unavailable")
	ErrNotUsable = errors.New("approval cannot be used for this action")
)

func errStorage(detail string) error { return fmt.Errorf("%w: %s", ErrStorage, detail) }

var (
	idRE     = regexp.MustCompile(`^apr_[0-9a-f]{32}$`)
	runRE    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	toolRE   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	digestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	reasonRE = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)
	nameRE   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
)

// Owner is the identity a run belongs to, copied from the run so the store has no
// dependency on the run package.
type Owner struct {
	ClientID      string `json:"client_id"`
	Workspace     string `json:"workspace"`
	GrantID       string `json:"grant_id"`
	GrantRevision int64  `json:"grant_revision"`
}

func (o Owner) validate() error {
	if !nameRE.MatchString(o.ClientID) || !nameRE.MatchString(o.Workspace) || !nameRE.MatchString(o.GrantID) || o.GrantRevision < 1 {
		return fmt.Errorf("%w: owner", ErrInvalid)
	}
	return nil
}

// Request is what the run loop asks a person to approve.
type Request struct {
	RunID         string
	RunGeneration uint64
	Owner         Owner
	Tool          string
	Digest        string
	Summary       string
	Paths         []string
	Friction      Friction
	Reasons       []string
	Preview       string
	Arguments     []byte
}

// Record is one approval as stored. Preview and Arguments exist only while it is live.
type Record struct {
	SchemaVersion int             `json:"schema_version"`
	ID            string          `json:"id"`
	RunID         string          `json:"run_id"`
	RunGeneration uint64          `json:"run_generation"`
	Owner         Owner           `json:"owner"`
	Tool          string          `json:"tool"`
	Digest        string          `json:"digest"`
	Summary       string          `json:"summary"`
	Paths         []string        `json:"paths"`
	Friction      Friction        `json:"friction"`
	Reasons       []string        `json:"reasons,omitempty"`
	Preview       string          `json:"preview,omitempty"`
	Arguments     json.RawMessage `json:"arguments,omitempty"`
	State         State           `json:"state"`
	Stop          bool            `json:"stop,omitempty"`
	CodeFailures  int             `json:"code_failures,omitempty"`
	Reason        string          `json:"reason,omitempty"`
	Outcome       string          `json:"outcome,omitempty"`
	DecidedVia    string          `json:"decided_via,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	ExpiresAt     time.Time       `json:"expires_at"`
	DecidedAt     time.Time       `json:"decided_at,omitzero"`
	ConsumedAt    time.Time       `json:"consumed_at,omitzero"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func (r Record) validate() error {
	if r.SchemaVersion != schemaVersion || !idRE.MatchString(r.ID) || !runRE.MatchString(r.RunID) || r.RunGeneration < 1 ||
		r.Owner.validate() != nil || !toolRE.MatchString(r.Tool) || !digestRE.MatchString(r.Digest) || !r.State.valid() ||
		(r.Friction != FrictionStandard && r.Friction != FrictionStrict) || r.CreatedAt.IsZero() || r.ExpiresAt.IsZero() ||
		len(r.Paths) > MaxPaths || len(r.Reasons) > 8 || len(r.Preview) > MaxPreviewBytes || len(r.Arguments) > MaxArgumentBytes ||
		r.CodeFailures < 0 || r.CodeFailures > MaxCodeFailures {
		return errStorage("invalid approval record")
	}
	if !r.State.Live() && (r.Preview != "" || len(r.Arguments) != 0) {
		return errStorage("a resolved approval still holds content")
	}
	return nil
}

// public is the view without the replayable arguments, which only Consume returns.
func (r Record) public() Record {
	r.Arguments = nil
	r.Paths = append([]string(nil), r.Paths...)
	r.Reasons = append([]string(nil), r.Reasons...)
	return r
}

func (r *Record) clearContent() {
	r.Preview = ""
	r.Arguments = nil
}

// Option configures a Store.
type Option func(*Store)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// WithTTL sets how long an approval can wait, between one minute and one day.
func WithTTL(ttl time.Duration) Option {
	return func(s *Store) {
		if ttl >= time.Minute && ttl <= 24*time.Hour {
			s.ttl = ttl
		}
	}
}

// WithLimits lowers how many live and total records are kept, for tests.
func WithLimits(live, total int) Option {
	return func(s *Store) {
		if live >= 1 && live <= MaxLive {
			s.maxLive = live
		}
		if total >= 1 && total <= MaxRecords {
			s.maxRecords = total
		}
	}
}

// WithAuditLimit lowers the size at which the audit file rotates, for tests.
func WithAuditLimit(bytes int64) Option {
	return func(s *Store) {
		if bytes >= 1<<10 && bytes <= maxAuditBytes {
			s.maxAudit = bytes
		}
	}
}

// WithRandom replaces the identifier source, for tests.
func WithRandom(r io.Reader) Option { return func(s *Store) { s.random = r } }

// Store is the approval directory under a data directory.
type Store struct {
	base   string
	now    func() time.Time
	ttl    time.Duration
	random io.Reader
	mu     sync.Mutex

	maxLive, maxRecords int
	maxAudit            int64
}

// Open returns a store rooted at dataDir/approvals. Nothing is created until the first write.
func Open(dataDir string, opts ...Option) *Store {
	s := &Store{base: dataDir, now: time.Now, ttl: DefaultTTL, random: rand.Reader, maxLive: MaxLive, maxRecords: MaxRecords, maxAudit: maxAuditBytes}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// dir returns the private directory, refusing a symlinked base or a loose mode.
func (s *Store) dir(create bool) (string, error) {
	if create {
		if err := os.MkdirAll(s.base, 0o700); err != nil {
			return "", errStorage("cannot create data directory")
		}
	}
	info, err := os.Lstat(s.base)
	if errors.Is(err, os.ErrNotExist) && !create {
		return filepath.Join(s.base, dirName), nil
	}
	if err != nil || !info.IsDir() {
		return "", errStorage("unsafe data directory")
	}
	dir := filepath.Join(s.base, dirName)
	if create {
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", errStorage("cannot create approval directory")
		}
	}
	info, err = os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) && !create {
		return dir, nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return "", errStorage("unsafe approval directory")
	}
	return dir, nil
}

func validFile(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm() == 0o600 && info.Size() <= maxRecord
}

func (s *Store) read(dir, id string) (Record, error) {
	if !idRE.MatchString(id) {
		return Record{}, ErrNotFound
	}
	path := filepath.Join(dir, id+".json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, ErrNotFound
	}
	if err != nil || !validFile(info) {
		return Record{}, errStorage("unsafe approval file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Record{}, errStorage("cannot read approval file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !validFile(opened) || !os.SameFile(info, opened) {
		return Record{}, errStorage("unsafe approval file")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxRecord+1))
	if err != nil || len(content) > maxRecord {
		return Record{}, errStorage("cannot read approval file")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var record Record
	if decoder.Decode(&record) != nil || decoder.Decode(new(any)) != io.EOF || record.ID != id {
		return Record{}, errStorage("malformed approval file")
	}
	if err := record.validate(); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (s *Store) write(dir string, record Record) error {
	record.SchemaVersion = schemaVersion
	record.UpdatedAt = s.now().UTC()
	if err := record.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil || len(data) > maxRecord {
		return errStorage("cannot encode approval record")
	}
	path := filepath.Join(dir, record.ID+".json")
	if info, err := os.Lstat(path); err == nil {
		if !validFile(info) {
			return errStorage("unsafe approval file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errStorage("cannot inspect approval file")
	}
	temp, err := os.CreateTemp(dir, ".approval-*.tmp")
	if err != nil {
		return errStorage("cannot create approval file")
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return errStorage("cannot secure approval file")
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		return errStorage("cannot write approval file")
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return errStorage("cannot sync approval file")
	}
	if err := temp.Close(); err != nil {
		return errStorage("cannot close approval file")
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return errStorage("cannot replace approval file")
	}
	return nil
}

// locked runs fn under the in-process mutex and an exclusive cross-process lock, so a
// decision, a consumption and an expiry can never interleave.
func (s *Store) locked(ctx context.Context, fn func(dir string) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir, err := s.dir(true)
	if err != nil {
		return err
	}
	unlock, err := lockDir(ctx, dir)
	if err != nil {
		return err
	}
	defer unlock()
	return fn(dir)
}

// mutating is locked plus a check that the audit log is healthy, so nothing changes
// unless the change can be recorded: a tampered or unwritable audit stops approvals
// instead of letting them proceed unrecorded.
func (s *Store) mutating(ctx context.Context, fn func(dir string) error) error {
	return s.locked(ctx, func(dir string) error {
		if _, _, err := lastLine(s.auditPath(dir)); err != nil {
			return err
		}
		return fn(dir)
	})
}

func lockDir(ctx context.Context, dir string) (func(), error) {
	path := filepath.Join(dir, lockName)
	deadline := time.Now().Add(lockWait)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, errStorage("cannot lock approval directory")
		}
		if info, statErr := os.Lstat(path); statErr == nil && time.Since(info.ModTime()) > lockStaleAge {
			_ = os.Remove(path)
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, errStorage("approval directory is busy")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *Store) newID() (string, error) {
	var token [16]byte
	if _, err := io.ReadFull(s.random, token[:]); err != nil {
		return "", errStorage("cannot create an identifier")
	}
	return "apr_" + hex.EncodeToString(token[:]), nil
}

func clean(value string, max int) (string, bool) {
	if len(value) > max {
		return "", false
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", false
		}
	}
	return value, true
}

func (r Request) validate() error {
	if !runRE.MatchString(r.RunID) || r.RunGeneration < 1 || r.Owner.validate() != nil || !toolRE.MatchString(r.Tool) ||
		!digestRE.MatchString(r.Digest) || (r.Friction != FrictionStandard && r.Friction != FrictionStrict) {
		return fmt.Errorf("%w: request", ErrInvalid)
	}
	if _, ok := clean(r.Summary, maxSummaryBytes); !ok || strings.ContainsAny(r.Summary, "\n\t") || r.Summary == "" {
		return fmt.Errorf("%w: summary", ErrInvalid)
	}
	if len(r.Paths) == 0 || len(r.Paths) > MaxPaths {
		return fmt.Errorf("%w: paths", ErrInvalid)
	}
	for _, p := range r.Paths {
		if _, ok := clean(p, 512); !ok || p == "" || strings.ContainsAny(p, "\n\t") {
			return fmt.Errorf("%w: path", ErrInvalid)
		}
	}
	if len(r.Reasons) > 8 {
		return fmt.Errorf("%w: reasons", ErrInvalid)
	}
	for _, reason := range r.Reasons {
		if !reasonRE.MatchString(reason) {
			return fmt.Errorf("%w: reason", ErrInvalid)
		}
	}
	if _, ok := clean(r.Preview, MaxPreviewBytes); !ok {
		return fmt.Errorf("%w: preview", ErrInvalid)
	}
	if len(r.Arguments) == 0 || len(r.Arguments) > MaxArgumentBytes || !json.Valid(r.Arguments) {
		return fmt.Errorf("%w: arguments", ErrInvalid)
	}
	return nil
}

// Request records an approval that a person must decide. A run has at most one live
// approval, so an older one for the same run is superseded.
func (s *Store) Request(ctx context.Context, req Request) (Record, error) {
	if err := req.validate(); err != nil {
		return Record{}, err
	}
	var created Record
	err := s.mutating(ctx, func(dir string) error {
		records, err := s.scan(dir)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		live := 0
		for _, existing := range records {
			if !existing.State.Live() {
				continue
			}
			if existing.RunID == req.RunID {
				existing.State, existing.Reason = StateSuperseded, "superseded"
				existing.clearContent()
				if err := s.write(dir, existing); err != nil {
					return err
				}
				if err := s.audit(dir, existing, "superseded", ""); err != nil {
					return err
				}
				continue
			}
			if !existing.ExpiresAt.After(now) {
				continue
			}
			live++
		}
		if live >= s.maxLive {
			return ErrBusy
		}
		if err := s.prune(dir, records); err != nil {
			return err
		}
		id, err := s.newID()
		if err != nil {
			return err
		}
		record := Record{ID: id, RunID: req.RunID, RunGeneration: req.RunGeneration, Owner: req.Owner, Tool: req.Tool, Digest: req.Digest,
			Summary: req.Summary, Paths: append([]string(nil), req.Paths...), Friction: req.Friction, Reasons: append([]string(nil), req.Reasons...),
			Preview: req.Preview, Arguments: append(json.RawMessage(nil), req.Arguments...), State: StatePending, CreatedAt: now, ExpiresAt: now.Add(s.ttl)}
		if err := s.write(dir, record); err != nil {
			return err
		}
		if err := s.audit(dir, record, "requested", ""); err != nil {
			_ = os.Remove(filepath.Join(dir, record.ID+".json")) // never leave an approval that was not recorded
			return err
		}
		created = record
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return created.public(), nil
}

// scan reads every record, skipping any that is damaged: a damaged record can never
// authorize anything, and it must not stop the rest from being handled.
func (s *Store) scan(dir string) ([]Record, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, errStorage("cannot list approvals")
	}
	var records []Record
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") || !idRE.MatchString(strings.TrimSuffix(name, ".json")) {
			continue
		}
		record, err := s.read(dir, strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].CreatedAt.After(records[j].CreatedAt)
		}
		return records[i].ID < records[j].ID
	})
	return records, nil
}

// prune removes the oldest resolved records once there are too many.
func (s *Store) prune(dir string, records []Record) error {
	if len(records) < s.maxRecords {
		return nil
	}
	removed := 0
	for i := len(records) - 1; i >= 0 && len(records)-removed >= s.maxRecords; i-- {
		if records[i].State.Live() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, records[i].ID+".json")); err == nil {
			removed++
		}
	}
	return nil
}

// Get returns one approval without its replayable arguments.
func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	var found Record
	err := s.mutating(ctx, func(dir string) error {
		record, err := s.read(dir, id)
		if err != nil {
			return err
		}
		record, err = s.expireIfDue(dir, record)
		if err != nil {
			return err
		}
		found = record
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return found.public(), nil
}

// expireIfDue moves a live approval whose time has passed to expired and drops its content.
func (s *Store) expireIfDue(dir string, record Record) (Record, error) {
	if !record.State.Live() || s.now().Before(record.ExpiresAt) {
		return record, nil
	}
	record.State, record.Reason = StateExpired, "expired"
	record.clearContent()
	if err := s.write(dir, record); err != nil {
		return Record{}, err
	}
	if err := s.audit(dir, record, "expired", ""); err != nil {
		return Record{}, err
	}
	return record, nil
}

// ListOptions selects approvals to show.
type ListOptions struct {
	Workspace string
	LiveOnly  bool
	Limit     int
}

// List returns approvals newest first without their preview or arguments.
func (s *Store) List(ctx context.Context, opts ListOptions) ([]Record, error) {
	var out []Record
	err := s.mutating(ctx, func(dir string) error {
		records, err := s.scan(dir)
		if err != nil {
			return err
		}
		limit := opts.Limit
		if limit < 1 || limit > 200 {
			limit = 200
		}
		for _, record := range records {
			record, err = s.expireIfDue(dir, record)
			if err != nil {
				return err
			}
			if opts.Workspace != "" && record.Owner.Workspace != opts.Workspace {
				continue
			}
			if opts.LiveOnly && !record.State.Live() {
				continue
			}
			view := record.public()
			view.Preview = ""
			out = append(out, view)
			if len(out) >= limit {
				break
			}
		}
		return nil
	})
	return out, err
}
