// Package harnessauth mints and verifies the local credentials that authorize an
// MCP client to use the harness. A client ID, profile name, loopback address or
// caller-supplied principal is never authority: only a Go-verified bearer grant
// bound to a registered client, registered workspaces, an operation allowlist, an
// expiry and a revision is. The package lives outside the harness core because the
// credential store is a local-product concern.
package harnessauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
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

	"github.com/taimufuraiyaa/agent-memory/internal/clientprofile"
)

const (
	schemaVersion         = 1
	tokenPrefix           = "hcap1"
	secretBytes           = 32
	idBytes               = 16
	maxGrants             = 256
	maxActivePerClient    = 8
	maxWorkspacesPerGrant = 16

	DefaultTTL = 24 * time.Hour
	MinTTL     = time.Minute
	MaxTTL     = 30 * 24 * time.Hour
	retainDead = 30 * 24 * time.Hour
)

// Operation is one action a grant may authorize. Approval is deliberately absent:
// approving a mutation is a trusted local UI or CLI action and no credential, and
// therefore no model-callable MCP tool, can hold it.
type Operation string

const (
	OpCapabilities Operation = "capabilities"
	OpStart        Operation = "start"
	OpStatus       Operation = "status"
	OpContinue     Operation = "continue"
	OpArtifact     Operation = "artifact"
	OpCancel       Operation = "cancel"
	OpDecide       Operation = "decide"
)

var operations = []Operation{OpCapabilities, OpStart, OpStatus, OpContinue, OpArtifact, OpCancel, OpDecide}

// AllOperations lists every grantable operation.
func AllOperations() []Operation { return append([]Operation(nil), operations...) }

var (
	// ErrDenied is the single outcome of every failed verification, so a caller
	// cannot distinguish an unknown grant from a revoked, expired or mismatched one.
	ErrDenied = errors.New("harness access denied")
	// ErrDisabled reports that the opt-in capability pack is off.
	ErrDisabled = errors.New("harness capability pack is disabled")
	ErrInvalid  = errors.New("invalid harness grant")
	ErrNotFound = errors.New("harness grant not found")
	ErrStorage  = errors.New("harness authority storage unavailable")

	idRE        = regexp.MustCompile(`^[a-f0-9]{32}$`)
	hashRE      = regexp.MustCompile(`^[a-f0-9]{64}$`)
	workspaceRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	secretRE    = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

func errStorage(detail string) error { return fmt.Errorf("%w: %s", ErrStorage, detail) }

// ParseOperation validates one operation name.
func ParseOperation(name string) (Operation, error) {
	name = strings.TrimSpace(name)
	if name == "approve" {
		return "", fmt.Errorf("%w: approval is a trusted local action and cannot be granted", ErrInvalid)
	}
	for _, operation := range operations {
		if string(operation) == name {
			return operation, nil
		}
	}
	return "", fmt.Errorf("%w: unknown operation", ErrInvalid)
}

// ClientDirectory resolves registered client profiles; *clientprofile.Store satisfies it.
type ClientDirectory interface {
	Get(id string) (clientprofile.Profile, error)
}

// WorkspaceDirectory resolves a registered workspace to its project root. The root
// always comes from this registry, never from the caller.
type WorkspaceDirectory interface {
	Root(name string) (string, error)
}

type binding struct {
	Name string `json:"name"`
	Root string `json:"root"`
}

type grant struct {
	ID         string      `json:"id"`
	ClientID   string      `json:"client_id"`
	Workspaces []binding   `json:"workspaces"`
	Operations []Operation `json:"operations"`
	Revision   int64       `json:"revision"`
	CreatedAt  time.Time   `json:"created_at"`
	ExpiresAt  time.Time   `json:"expires_at"`
	RevokedAt  *time.Time  `json:"revoked_at,omitempty"`
	SecretHash string      `json:"secret_sha256"`
}

type state struct {
	SchemaVersion int     `json:"schema_version"`
	Enabled       bool    `json:"enabled"`
	Grants        []grant `json:"grants"`
}

func (s state) validate() error {
	if s.SchemaVersion != schemaVersion {
		return errStorage("unsupported authority schema version")
	}
	if len(s.Grants) > maxGrants {
		return errStorage("too many grants")
	}
	seen := make(map[string]struct{}, len(s.Grants))
	for _, g := range s.Grants {
		if !idRE.MatchString(g.ID) || !hashRE.MatchString(g.SecretHash) || g.Revision < 1 ||
			clientprofile.ValidateID(g.ClientID) != nil || g.CreatedAt.IsZero() || !g.ExpiresAt.After(g.CreatedAt) {
			return errStorage("invalid grant record")
		}
		if _, dup := seen[g.ID]; dup {
			return errStorage("duplicate grant record")
		}
		seen[g.ID] = struct{}{}
		if len(g.Operations) < 1 || len(g.Operations) > len(operations) || len(g.Workspaces) < 1 || len(g.Workspaces) > maxWorkspacesPerGrant {
			return errStorage("invalid grant bindings")
		}
		ops := make(map[Operation]struct{}, len(g.Operations))
		for _, operation := range g.Operations {
			if _, err := ParseOperation(string(operation)); err != nil {
				return errStorage("invalid grant operation")
			}
			if _, dup := ops[operation]; dup {
				return errStorage("duplicate grant operation")
			}
			ops[operation] = struct{}{}
		}
		names := make(map[string]struct{}, len(g.Workspaces))
		for _, b := range g.Workspaces {
			if !workspaceRE.MatchString(b.Name) || !filepath.IsAbs(b.Root) {
				return errStorage("invalid grant workspace")
			}
			if _, dup := names[b.Name]; dup {
				return errStorage("duplicate grant workspace")
			}
			names[b.Name] = struct{}{}
		}
	}
	return nil
}

func (g grant) status(now time.Time) string {
	switch {
	case g.RevokedAt != nil:
		return "revoked"
	case !now.Before(g.ExpiresAt):
		return "expired"
	default:
		return "active"
	}
}

// GrantInfo is the safe, listable view of a grant. It never carries a secret or hash.
type GrantInfo struct {
	ID         string      `json:"id"`
	ClientID   string      `json:"client_id"`
	Workspaces []string    `json:"workspaces"`
	Operations []Operation `json:"operations"`
	Revision   int64       `json:"revision"`
	CreatedAt  time.Time   `json:"created_at"`
	ExpiresAt  time.Time   `json:"expires_at"`
	Status     string      `json:"status"`
}

func (g grant) info(now time.Time) GrantInfo {
	names := make([]string, len(g.Workspaces))
	for i, b := range g.Workspaces {
		names[i] = b.Name
	}
	return GrantInfo{ID: g.ID, ClientID: g.ClientID, Workspaces: names, Operations: append([]Operation(nil), g.Operations...),
		Revision: g.Revision, CreatedAt: g.CreatedAt, ExpiresAt: g.ExpiresAt, Status: g.status(now)}
}

// Principal is the verified identity a run is started under.
type Principal struct {
	GrantID       string
	GrantRevision int64
	ClientID      string
	Workspace     string
	// Root is the canonical registered project root, taken from the registry.
	Root      string
	Operation Operation
	// Operations lists everything the grant allows, for capability discovery.
	Operations []Operation
}

// Request names what the caller wants to do. ClientID is a claim used only to
// detect a mismatch; the token alone decides who the caller is.
type Request struct {
	ClientID  string
	Workspace string
	Operation Operation
}

// MintRequest describes a new grant. Minting is a trusted local operation.
type MintRequest struct {
	ClientID   string
	Workspaces []string
	Operations []Operation
	TTL        time.Duration
}

// Options configures an Authority. Now and Random exist for tests.
type Options struct {
	Clients    ClientDirectory
	Workspaces WorkspaceDirectory
	Now        func() time.Time
	Random     io.Reader
}

type Authority struct {
	base       string
	clients    ClientDirectory
	workspaces WorkspaceDirectory
	now        func() time.Time
	random     io.Reader
	mu         sync.Mutex
}

func New(dataDir string, options Options) (*Authority, error) {
	if strings.TrimSpace(dataDir) == "" || options.Clients == nil || options.Workspaces == nil {
		return nil, fmt.Errorf("%w: data directory, client and workspace directories are required", ErrInvalid)
	}
	a := &Authority{base: dataDir, clients: options.Clients, workspaces: options.Workspaces, now: options.Now, random: options.Random}
	if a.now == nil {
		a.now = time.Now
	}
	if a.random == nil {
		a.random = rand.Reader
	}
	return a, nil
}

// Status reports whether the capability pack is enabled and whether storage is
// usable. It never returns a secret.
type Status struct {
	Enabled bool `json:"enabled"`
	Grants  int  `json:"grants"`
	Active  int  `json:"active"`
}

func (a *Authority) Status() (Status, error) {
	current, err := a.load()
	if err != nil {
		return Status{}, err
	}
	status := Status{Enabled: current.Enabled, Grants: len(current.Grants)}
	now := a.now()
	for _, g := range current.Grants {
		if g.status(now) == "active" {
			status.Active++
		}
	}
	return status, nil
}

// SetEnabled turns the opt-in capability pack on or off. Disabling keeps grants but
// denies every verification until re-enabled; it is the rollback switch.
func (a *Authority) SetEnabled(ctx context.Context, enabled bool) error {
	return a.mutate(ctx, func(s *state) error {
		s.Enabled = enabled
		return nil
	})
}

func (a *Authority) List() ([]GrantInfo, error) {
	current, err := a.load()
	if err != nil {
		return nil, err
	}
	now := a.now()
	items := make([]GrantInfo, 0, len(current.Grants))
	for _, g := range current.Grants {
		items = append(items, g.info(now))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreatedAt.Before(items[j].CreatedAt) || (items[i].CreatedAt.Equal(items[j].CreatedAt) && items[i].ID < items[j].ID)
	})
	return items, nil
}

// Mint creates a grant and returns its bearer token. The token is shown once; only
// its SHA-256 is stored, so a lost token can only be replaced by rotation.
func (a *Authority) Mint(ctx context.Context, request MintRequest) (string, GrantInfo, error) {
	if err := clientprofile.ValidateID(request.ClientID); err != nil {
		return "", GrantInfo{}, fmt.Errorf("%w: client ID", ErrInvalid)
	}
	if _, err := a.clients.Get(request.ClientID); err != nil {
		return "", GrantInfo{}, fmt.Errorf("%w: client is not registered", ErrInvalid)
	}
	ttl := request.TTL
	if ttl == 0 {
		ttl = DefaultTTL
	}
	if ttl < MinTTL || ttl > MaxTTL {
		return "", GrantInfo{}, fmt.Errorf("%w: ttl must be between %s and %s", ErrInvalid, MinTTL, MaxTTL)
	}
	ops, err := normalizeOperations(request.Operations)
	if err != nil {
		return "", GrantInfo{}, err
	}
	bindings, err := a.resolveWorkspaces(request.Workspaces)
	if err != nil {
		return "", GrantInfo{}, err
	}
	secret, err := a.randomString(secretBytes)
	if err != nil {
		return "", GrantInfo{}, errStorage("cannot generate credential")
	}
	id, err := a.randomHex(idBytes)
	if err != nil {
		return "", GrantInfo{}, errStorage("cannot generate credential")
	}
	var info GrantInfo
	err = a.mutate(ctx, func(s *state) error {
		if !s.Enabled {
			return ErrDisabled
		}
		now := a.now().UTC()
		s.Grants = prune(s.Grants, now)
		active := 0
		for _, g := range s.Grants {
			if g.ClientID == request.ClientID && g.status(now) == "active" {
				active++
			}
		}
		if active >= maxActivePerClient || len(s.Grants) >= maxGrants {
			return fmt.Errorf("%w: too many grants; revoke one first", ErrInvalid)
		}
		created := grant{ID: id, ClientID: request.ClientID, Workspaces: bindings, Operations: ops, Revision: 1,
			CreatedAt: now, ExpiresAt: now.Add(ttl), SecretHash: hashSecret(secret)}
		s.Grants = append(s.Grants, created)
		info = created.info(now)
		return nil
	})
	if err != nil {
		return "", GrantInfo{}, err
	}
	return tokenPrefix + "." + id + "." + secret, info, nil
}

// Rotate replaces a grant's secret and bumps its revision, so the previous token
// stops working at once. Bindings are kept as minted: a grant whose workspace root
// changed must be revoked and minted again, never silently re-blessed. A ttl of zero
// keeps the existing expiry; an expired or revoked grant cannot be rotated.
func (a *Authority) Rotate(ctx context.Context, id string, ttl time.Duration) (string, GrantInfo, error) {
	if !idRE.MatchString(id) {
		return "", GrantInfo{}, ErrNotFound
	}
	if ttl != 0 && (ttl < MinTTL || ttl > MaxTTL) {
		return "", GrantInfo{}, fmt.Errorf("%w: ttl must be between %s and %s", ErrInvalid, MinTTL, MaxTTL)
	}
	secret, err := a.randomString(secretBytes)
	if err != nil {
		return "", GrantInfo{}, errStorage("cannot generate credential")
	}
	var info GrantInfo
	err = a.mutate(ctx, func(s *state) error {
		if !s.Enabled {
			return ErrDisabled
		}
		now := a.now().UTC()
		for i := range s.Grants {
			if s.Grants[i].ID != id {
				continue
			}
			if s.Grants[i].status(now) != "active" {
				return fmt.Errorf("%w: grant is %s; mint a new one", ErrInvalid, s.Grants[i].status(now))
			}
			s.Grants[i].SecretHash = hashSecret(secret)
			s.Grants[i].Revision++
			if ttl != 0 {
				s.Grants[i].ExpiresAt = now.Add(ttl)
			}
			info = s.Grants[i].info(now)
			return nil
		}
		return ErrNotFound
	})
	if err != nil {
		return "", GrantInfo{}, err
	}
	return tokenPrefix + "." + id + "." + secret, info, nil
}

// Revoke permanently disables a grant. It is idempotent and works while the pack is
// disabled, so access can always be withdrawn.
func (a *Authority) Revoke(ctx context.Context, id string) (GrantInfo, error) {
	if !idRE.MatchString(id) {
		return GrantInfo{}, ErrNotFound
	}
	var info GrantInfo
	err := a.mutate(ctx, func(s *state) error {
		now := a.now().UTC()
		for i := range s.Grants {
			if s.Grants[i].ID != id {
				continue
			}
			if s.Grants[i].RevokedAt == nil {
				s.Grants[i].RevokedAt = &now
				s.Grants[i].Revision++
			}
			info = s.Grants[i].info(now)
			return nil
		}
		return ErrNotFound
	})
	return info, err
}

// Verify authenticates a bearer token for one request. Every failure, including
// malformed tokens, unknown, revoked and expired grants, a disabled pack, a client
// or workspace that is no longer registered, a changed root and any storage fault,
// returns ErrDenied so nothing about which grants exist is disclosed. The registry
// and grant file are read fresh on every call.
func (a *Authority) Verify(token string, request Request) (Principal, error) {
	id, secret, ok := parseToken(token)
	current, err := a.load()
	var found *grant
	if ok && err == nil && current.Enabled {
		for i := range current.Grants {
			if current.Grants[i].ID == id {
				found = &current.Grants[i]
				break
			}
		}
	}
	// Always hash and compare so an unknown ID costs the same as a wrong secret.
	want := strings.Repeat("0", 64)
	if found != nil {
		want = found.SecretHash
	}
	match := subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(want)) == 1
	if !ok || err != nil || found == nil || !match {
		return Principal{}, ErrDenied
	}
	now := a.now()
	if found.status(now) != "active" {
		return Principal{}, ErrDenied
	}
	if request.ClientID != "" && request.ClientID != found.ClientID {
		return Principal{}, ErrDenied
	}
	if !hasOperation(found.Operations, request.Operation) {
		return Principal{}, ErrDenied
	}
	var bound *binding
	for i := range found.Workspaces {
		if found.Workspaces[i].Name == request.Workspace {
			bound = &found.Workspaces[i]
			break
		}
	}
	if bound == nil {
		return Principal{}, ErrDenied
	}
	if _, err := a.clients.Get(found.ClientID); err != nil {
		return Principal{}, ErrDenied
	}
	root, err := a.workspaces.Root(bound.Name)
	if err != nil {
		return Principal{}, ErrDenied
	}
	canonical, err := canonicalDir(root)
	if err != nil || canonical != bound.Root {
		return Principal{}, ErrDenied
	}
	return Principal{GrantID: found.ID, GrantRevision: found.Revision, ClientID: found.ClientID,
		Workspace: bound.Name, Root: canonical, Operation: request.Operation,
		Operations: append([]Operation(nil), found.Operations...)}, nil
}

// Active reports whether a grant can still authorize work in a workspace: the pack
// is enabled and the grant exists, is neither revoked nor expired, and its client and
// workspace root still match. Running work uses it to notice revocation. Rotation does
// not end a run: the grant is the same and only its secret changed. It never says why
// it is false.
func (a *Authority) Active(grantID, workspace string) bool {
	current, err := a.load()
	if err != nil || !current.Enabled || !idRE.MatchString(grantID) {
		return false
	}
	for _, g := range current.Grants {
		if g.ID != grantID {
			continue
		}
		if g.status(a.now()) != "active" {
			return false
		}
		if _, err := a.clients.Get(g.ClientID); err != nil {
			return false
		}
		for _, b := range g.Workspaces {
			if b.Name != workspace {
				continue
			}
			root, err := a.workspaces.Root(b.Name)
			if err != nil {
				return false
			}
			canonical, err := canonicalDir(root)
			return err == nil && canonical == b.Root
		}
		return false
	}
	return false
}

func (a *Authority) resolveWorkspaces(names []string) ([]binding, error) {
	if len(names) < 1 || len(names) > maxWorkspacesPerGrant {
		return nil, fmt.Errorf("%w: between 1 and %d workspaces are required", ErrInvalid, maxWorkspacesPerGrant)
	}
	seen := make(map[string]struct{}, len(names))
	bindings := make([]binding, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !workspaceRE.MatchString(name) {
			return nil, fmt.Errorf("%w: workspace name", ErrInvalid)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("%w: duplicate workspace", ErrInvalid)
		}
		seen[name] = struct{}{}
		root, err := a.workspaces.Root(name)
		if err != nil || strings.TrimSpace(root) == "" {
			return nil, fmt.Errorf("%w: workspace is not registered with a project root", ErrInvalid)
		}
		canonical, err := canonicalDir(root)
		if err != nil {
			return nil, fmt.Errorf("%w: workspace root is not an existing directory", ErrInvalid)
		}
		bindings = append(bindings, binding{Name: name, Root: canonical})
	}
	return bindings, nil
}

func normalizeOperations(requested []Operation) ([]Operation, error) {
	if len(requested) < 1 {
		return nil, fmt.Errorf("%w: at least one operation is required", ErrInvalid)
	}
	seen := make(map[Operation]struct{}, len(requested))
	result := make([]Operation, 0, len(requested))
	for _, operation := range requested {
		parsed, err := ParseOperation(string(operation))
		if err != nil {
			return nil, err
		}
		if _, dup := seen[parsed]; dup {
			continue
		}
		seen[parsed] = struct{}{}
		result = append(result, parsed)
	}
	return result, nil
}

func hasOperation(granted []Operation, wanted Operation) bool {
	for _, operation := range granted {
		if operation == wanted {
			return true
		}
	}
	return false
}

// prune drops grants that ended more than retainDead ago, keeping the file small.
func prune(grants []grant, now time.Time) []grant {
	kept := grants[:0]
	for _, g := range grants {
		ended := g.ExpiresAt
		if g.RevokedAt != nil && g.RevokedAt.Before(ended) {
			ended = *g.RevokedAt
		}
		if g.status(now) != "active" && now.Sub(ended) > retainDead {
			continue
		}
		kept = append(kept, g)
	}
	return kept
}

func parseToken(token string) (id, secret string, ok bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix || !idRE.MatchString(parts[1]) || !secretRE.MatchString(parts[2]) {
		return "", strings.Repeat("A", 43), false
	}
	return parts[1], parts[2], true
}

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func (a *Authority) randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(a.random, buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (a *Authority) randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(a.random, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// canonicalDir resolves symlinks so a registered root swapped for a link to
// somewhere else no longer matches the root a grant was minted for.
func canonicalDir(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("not a directory")
	}
	return filepath.Clean(resolved), nil
}
