// Package harnesstools offers the harness's project tools. This first slice is read-only:
// read a file, list a directory, and search. Every tool is confined to the registered
// project root through the shared confined-filesystem package, validates its arguments
// strictly while preparing, does no work until invoked, and returns bounded, redacted,
// deterministic output. Output is evidence for the model, never policy.
package harnesstools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessfs"
)

const (
	ProviderID harness.ProviderID = "project-read"

	ToolReadFile = "read_file"
	ToolListDir  = "list_dir"
	ToolSearch   = "search"

	DefaultReadLines     = 200
	MaxReadLines         = 400
	MaxReadFileBytes     = 256 << 10
	MaxLineBytes         = 300
	MaxQueryBytes        = 200
	MaxGlobBytes         = 100
	DefaultSearchResults = 50
	MaxSearchResults     = 100
	MaxContextLines      = 3
	MaxSearchFiles       = 4000
	MaxSearchTotalBytes  = 16 << 20
	DefaultSearchBudget  = 5 * time.Second
	maxPending           = 64
)

// searchSkipDirs are never entered by a search: dependency and build output.
var searchSkipDirs = map[string]bool{"node_modules": true, "vendor": true, "dist": true, "build": true, "target": true, "__pycache__": true}

// Config wires the provider to the harness's workspace registry.
type Config struct {
	// Root resolves a registered workspace name to its project root.
	Root func(workspace string) (string, error)
	// Redact removes secrets and personal data from everything the tools return.
	Redact func(string) string
	// SearchBudget bounds one search's wall time.
	SearchBudget time.Duration
}

type Provider struct{ cfg Config }

func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Root == nil {
		return nil, errors.New("a workspace root resolver is required")
	}
	if cfg.Redact == nil {
		cfg.Redact = func(s string) string { return s }
	}
	if cfg.SearchBudget <= 0 {
		cfg.SearchBudget = DefaultSearchBudget
	}
	return &Provider{cfg: cfg}, nil
}

// Manifest declares the tools. A declaration is not proof the workspace root exists.
func Manifest() harness.Manifest {
	return harness.Manifest{Version: harness.ContractVersion, ID: ProviderID, Kind: harness.KindTool,
		Capabilities: []harness.CapabilityID{ToolListDir, ToolReadFile, ToolSearch}}
}

// Register adds the provider; each session gets its own state.
func (p *Provider) Register(registry *harness.Registry) error {
	return registry.Register(Manifest(), func() (harness.Provider, error) {
		return &session{provider: p, pending: map[string]call{}}, nil
	})
}

type call struct {
	tool string
	args any
	root string
}

type session struct {
	provider *Provider
	scope    harness.Scope

	mu      sync.Mutex
	pending map[string]call
}

func (s *session) Close() error { return nil }

// Probe reports every tool available when the workspace's project root resolves and opens.
func (s *session) Probe(_ context.Context, scope harness.Scope) (harness.LiveAccess, error) {
	s.scope = scope
	state := harness.AccessUnavailable
	if root, err := s.provider.cfg.Root(scope.Workspace); err == nil {
		if handle, err := harnessfs.Open(root); err == nil {
			_ = handle.Close()
			state = harness.AccessAvailable
		}
	}
	access := harness.LiveAccess{Version: harness.ContractVersion, Provider: ProviderID, Scope: scope, Revision: 1,
		Capabilities: map[harness.CapabilityID]harness.AccessState{}}
	for _, c := range Manifest().Capabilities {
		access.Capabilities[c] = state
	}
	return access, nil
}

type readArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	MaxLines  int    `json:"max_lines"`
}

type listArgs struct {
	Path string `json:"path"`
}

type searchArgs struct {
	Query      string `json:"query"`
	Regex      bool   `json:"regex"`
	IgnoreCase bool   `json:"ignore_case"`
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	MaxResults int    `json:"max_results"`
	Context    int    `json:"context"`
}

func decodeStrict(raw []byte, into any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) == nil {
		return errors.New("trailing data")
	}
	return nil
}

// Prepare validates one call and has no side effects: it reads no file and touches no
// state beyond remembering the normalized call for Invoke. Invalid arguments are a typed
// failure and a protected path is a typed denial, neither carrying detail.
func (s *session) Prepare(_ context.Context, q harness.ToolRequest) (harness.PreparedAction, error) {
	action := harness.PreparedAction{Envelope: q.Envelope}
	if harness.CapabilityID(q.ToolID) != q.Capability {
		action.Outcome = harness.OutcomeFailed
		return action, nil
	}
	root, err := s.provider.cfg.Root(s.scope.Workspace)
	if err != nil {
		action.Outcome = harness.OutcomeUnavailable
		return action, nil
	}
	var normalized any
	var summary string
	switch q.ToolID {
	case ToolReadFile:
		var a readArgs
		if decodeStrict(q.Arguments, &a) != nil {
			action.Outcome = harness.OutcomeFailed
			return action, nil
		}
		path, outcome := cleanPath(a.Path, false)
		if outcome != "" {
			action.Outcome = outcome
			return action, nil
		}
		if a.StartLine == 0 {
			a.StartLine = 1
		}
		if a.MaxLines == 0 {
			a.MaxLines = DefaultReadLines
		}
		if a.StartLine < 1 || a.MaxLines < 1 || a.MaxLines > MaxReadLines {
			action.Outcome = harness.OutcomeFailed
			return action, nil
		}
		a.Path = path
		normalized, summary = a, "read "+path
	case ToolListDir:
		var a listArgs
		if decodeStrict(q.Arguments, &a) != nil {
			action.Outcome = harness.OutcomeFailed
			return action, nil
		}
		path, outcome := cleanPath(a.Path, true)
		if outcome != "" {
			action.Outcome = outcome
			return action, nil
		}
		a.Path = path
		normalized, summary = a, "list "+path
	case ToolSearch:
		var a searchArgs
		if decodeStrict(q.Arguments, &a) != nil || a.Query == "" || len(a.Query) > MaxQueryBytes || len(a.Glob) > MaxGlobBytes ||
			a.MaxResults < 0 || a.MaxResults > MaxSearchResults || a.Context < 0 || a.Context > MaxContextLines {
			action.Outcome = harness.OutcomeFailed
			return action, nil
		}
		if _, err := compileMatcher(a); err != nil {
			action.Outcome = harness.OutcomeFailed
			return action, nil
		}
		path, outcome := cleanPath(a.Path, true)
		if outcome != "" {
			action.Outcome = outcome
			return action, nil
		}
		if a.MaxResults == 0 {
			a.MaxResults = DefaultSearchResults
		}
		a.Path = path
		normalized, summary = a, "search "+path
	default:
		action.Outcome = harness.OutcomeUnsupported
		return action, nil
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		action.Outcome = harness.OutcomeFailed
		return action, nil
	}
	sum := sha256.Sum256([]byte(q.ToolID + "\x00" + string(canonical) + "\x00" + root))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) >= maxPending {
		action.Outcome = harness.OutcomeFailed
		return action, nil
	}
	s.pending[digest] = call{tool: q.ToolID, args: normalized, root: root}
	action.Outcome, action.Digest, action.Summary = harness.OutcomeOK, digest, summary
	return action, nil
}

// cleanPath validates a path argument. Only the directory tools accept the root itself.
func cleanPath(raw string, allowRoot bool) (string, harness.Outcome) {
	cleaned, err := harnessfs.Clean(raw)
	if err != nil {
		return "", harness.OutcomeDenied
	}
	if cleaned == "." && !allowRoot {
		return "", harness.OutcomeFailed
	}
	if harnessfs.Denied(cleaned) {
		return "", harness.OutcomeDenied
	}
	return cleaned, ""
}

// Invoke runs a call this session prepared. The root is resolved again, so a workspace
// whose root was re-pointed since Prepare is stale rather than read from.
func (s *session) Invoke(ctx context.Context, action harness.PreparedAction) (harness.ToolAnswer, error) {
	answer := harness.ToolAnswer{Envelope: action.Envelope}
	s.mu.Lock()
	prepared, ok := s.pending[action.Digest]
	delete(s.pending, action.Digest)
	s.mu.Unlock()
	if !ok {
		answer.Outcome = harness.OutcomeFailed
		return answer, nil
	}
	root, err := s.provider.cfg.Root(s.scope.Workspace)
	if err != nil {
		answer.Outcome = harness.OutcomeUnavailable
		return answer, nil
	}
	if root != prepared.root {
		answer.Outcome = harness.OutcomeStale
		return answer, nil
	}
	project, err := harnessfs.Open(root)
	if err != nil {
		answer.Outcome = harness.OutcomeUnavailable
		return answer, nil
	}
	defer project.Close()
	var body []byte
	var outcome harness.Outcome
	switch prepared.tool {
	case ToolReadFile:
		body, outcome = s.read(project, prepared.args.(readArgs), action.MaxBytes)
	case ToolListDir:
		body, outcome = s.list(project, prepared.args.(listArgs), action.MaxBytes)
	case ToolSearch:
		body, outcome = s.search(ctx, project, prepared.args.(searchArgs), action.MaxBytes)
	default:
		outcome = harness.OutcomeUnsupported
	}
	answer.Outcome = outcome
	if outcome == harness.OutcomeOK || outcome == harness.OutcomePartial {
		answer.Output = body
	}
	return answer, nil
}

func outcomeFor(err error) harness.Outcome {
	switch {
	case errors.Is(err, harnessfs.ErrDenied), errors.Is(err, harnessfs.ErrInvalidPath):
		return harness.OutcomeDenied
	case errors.Is(err, context.DeadlineExceeded):
		return harness.OutcomeTimeout
	case errors.Is(err, context.Canceled):
		return harness.OutcomeCancelled
	}
	return harness.OutcomeFailed
}
