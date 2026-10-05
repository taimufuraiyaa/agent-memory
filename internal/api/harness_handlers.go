package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/taimufuraiyaa/agent-memory/internal/harness"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessauth"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessrun"
)

const (
	harnessBasePath       = "/api/v1/harness/"
	maxHarnessRequestSize = 64 << 10
	maxHarnessTokenLength = 256
	harnessClientHeader   = "X-Agent-Memory-Client"
)

var (
	harnessRunPathRE   = regexp.MustCompile(`^run_[a-f0-9]{32}$`)
	harnessWorkspaceRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// HarnessGateway is the opt-in local harness surface. When a Service has none, the
// routes are not registered at all, so an unconfigured install exposes nothing.
// Authority is the only source of caller identity: a bearer grant verified by Go.
type HarnessGateway struct {
	Authority *harnessauth.Authority
	Runs      *harnessrun.Manager
	// Providers are the static manifests shown by capability discovery; listing
	// them performs no provider I/O.
	Providers []harness.Manifest
	// Fake marks providers that are test doubles, so discovery never overstates them.
	Fake bool
}

type harnessBudgetView struct {
	MaxTurns         int   `json:"max_turns"`
	MaxActiveSeconds int64 `json:"max_active_seconds"`
	MaxToolCalls     int   `json:"max_tool_calls"`
	MaxOutputBytes   int   `json:"max_output_bytes"`
	MaxSpendMicros   int64 `json:"max_spend_micros"`
	MaxDepth         int   `json:"max_depth"`
}

func budgetView(b harnessrun.Budget) harnessBudgetView {
	return harnessBudgetView{MaxTurns: b.MaxTurns, MaxActiveSeconds: int64(b.MaxActive / time.Second), MaxToolCalls: b.MaxToolCalls,
		MaxOutputBytes: b.MaxOutputBytes, MaxSpendMicros: b.MaxSpendMicros, MaxDepth: b.MaxDepth}
}

type harnessAttentionView struct {
	Kind   string `json:"kind"`
	Prompt string `json:"prompt,omitempty"`
	// ApprovalID lets the person find the approval at their terminal. It is an opaque
	// identifier and grants nothing; the action digest is withheld.
	ApprovalID string `json:"approval_id,omitempty"`
	Turn       int    `json:"turn"`
}

type harnessRunView struct {
	ID           string                    `json:"id"`
	State        harnessrun.State          `json:"state"`
	Generation   uint64                    `json:"generation"`
	Turn         int                       `json:"turn"`
	Code         string                    `json:"code,omitempty"`
	Depth        int                       `json:"depth"`
	Usage        harnessrun.Usage          `json:"usage"`
	Budget       harnessBudgetView         `json:"budget"`
	Attention    *harnessAttentionView     `json:"attention,omitempty"`
	Artifacts    []harnessrun.ArtifactMeta `json:"artifacts"`
	CreatedAt    time.Time                 `json:"created_at"`
	UpdatedAt    time.Time                 `json:"updated_at"`
	ExpiresAt    time.Time                 `json:"expires_at"`
	Deduplicated bool                      `json:"deduplicated,omitempty"`
}

// runView is the client view of a run. The approval action digest is withheld: only
// the trusted local approval channel needs it.
func runView(s harnessrun.Status) harnessRunView {
	view := harnessRunView{ID: s.ID, State: s.State, Generation: s.Generation, Turn: s.Turn, Code: s.Code, Depth: s.Depth, Usage: s.Usage,
		Budget: budgetView(s.Budget), Artifacts: s.Artifacts, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, ExpiresAt: s.ExpiresAt, Deduplicated: s.Deduplicated}
	if s.Attention != nil {
		view.Attention = &harnessAttentionView{Kind: s.Attention.Kind, Prompt: s.Attention.Prompt, ApprovalID: s.Attention.ApprovalID, Turn: s.Attention.Turn}
	}
	return view
}

type harnessBudgetRequest struct {
	MaxTurns         int   `json:"max_turns"`
	MaxActiveSeconds int64 `json:"max_active_seconds"`
	MaxToolCalls     int   `json:"max_tool_calls"`
	MaxOutputBytes   int   `json:"max_output_bytes"`
	MaxSpendMicros   int64 `json:"max_spend_micros"`
	MaxDepth         int   `json:"max_depth"`
}

func (b harnessBudgetRequest) budget() (harnessrun.Budget, bool) {
	if b.MaxActiveSeconds < 0 || b.MaxActiveSeconds > int64(harnessrun.CeilingBudget.MaxActive/time.Second) {
		return harnessrun.Budget{}, false
	}
	return harnessrun.Budget{MaxTurns: b.MaxTurns, MaxActive: time.Duration(b.MaxActiveSeconds) * time.Second, MaxToolCalls: b.MaxToolCalls,
		MaxOutputBytes: b.MaxOutputBytes, MaxSpendMicros: b.MaxSpendMicros, MaxDepth: b.MaxDepth}, true
}

type harnessStartRequest struct {
	Workspace      string               `json:"workspace"`
	IdempotencyKey string               `json:"idempotency_key"`
	Goal           string               `json:"goal"`
	Budget         harnessBudgetRequest `json:"budget"`
}

type harnessCancelRequest struct {
	Workspace          string `json:"workspace"`
	IdempotencyKey     string `json:"idempotency_key"`
	ExpectedGeneration uint64 `json:"expected_generation"`
}

func harnessRouter(g *HarnessGateway) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		rest := strings.TrimPrefix(r.URL.Path, harnessBasePath)
		parts := strings.Split(rest, "/")
		switch {
		case rest == "capabilities":
			if !harnessMethod(w, r, http.MethodGet) {
				return
			}
			g.capabilities(w, r)
		case rest == "runs":
			if !harnessMethod(w, r, http.MethodPost) {
				return
			}
			g.start(w, r)
		case len(parts) == 2 && parts[0] == "runs" && harnessRunPathRE.MatchString(parts[1]):
			if !harnessMethod(w, r, http.MethodGet) {
				return
			}
			g.status(w, r, parts[1])
		case len(parts) == 3 && parts[0] == "runs" && parts[2] == "cancel" && harnessRunPathRE.MatchString(parts[1]):
			if !harnessMethod(w, r, http.MethodPost) {
				return
			}
			g.cancel(w, r, parts[1])
		default:
			writeErr(w, http.StatusNotFound, "not_found", "not found")
		}
	}
}

func harnessMethod(w http.ResponseWriter, r *http.Request, allowed string) bool {
	if r.Method == allowed {
		return true
	}
	w.Header().Set("Allow", allowed)
	writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	return false
}

// authenticate verifies the bearer grant for one operation in one workspace. Every
// failure, whatever its cause, is the same 401. The token is read only from the
// Authorization header, never from a URL, so it cannot end up in logs or history.
func (g *HarnessGateway) authenticate(w http.ResponseWriter, r *http.Request, workspace string, operation harnessauth.Operation) (harnessauth.Principal, bool) {
	deny := func() (harnessauth.Principal, bool) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeErr(w, http.StatusUnauthorized, "unauthorized", "access denied")
		return harnessauth.Principal{}, false
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return deny()
	}
	scheme, token, found := strings.Cut(values[0], " ")
	token = strings.TrimSpace(token)
	if !found || !strings.EqualFold(scheme, "Bearer") || token == "" || len(token) > maxHarnessTokenLength {
		return deny()
	}
	principal, err := g.Authority.Verify(token, harnessauth.Request{
		ClientID: strings.TrimSpace(r.Header.Get(harnessClientHeader)), Workspace: workspace, Operation: operation})
	if err != nil {
		return deny()
	}
	return principal, true
}

func harnessOwner(p harnessauth.Principal) harnessrun.Owner {
	return harnessrun.Owner{ClientID: p.ClientID, Workspace: p.Workspace, GrantID: p.GrantID, GrantRevision: p.GrantRevision}
}

func queryWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	workspace := r.URL.Query().Get("workspace")
	if !harnessWorkspaceRE.MatchString(workspace) {
		writeErr(w, http.StatusBadRequest, "invalid_request", "workspace is required")
		return "", false
	}
	return workspace, true
}

func decodeHarnessBody(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxHarnessRequestSize))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request_too_large", "request is too large")
			return false
		}
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid request")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "invalid_request", "request must contain one JSON object")
		return false
	}
	return true
}

// capabilities reports the effective, grant-scoped surface without provider I/O.
func (g *HarnessGateway) capabilities(w http.ResponseWriter, r *http.Request) {
	workspace, ok := queryWorkspace(w, r)
	if !ok {
		return
	}
	principal, ok := g.authenticate(w, r, workspace, harnessauth.OpCapabilities)
	if !ok {
		return
	}
	providers := make([]map[string]any, 0, len(g.Providers))
	for _, manifest := range g.Providers {
		capabilities := make([]string, len(manifest.Capabilities))
		for i, capability := range manifest.Capabilities {
			capabilities[i] = string(capability)
		}
		providers = append(providers, map[string]any{"id": string(manifest.ID), "kind": string(manifest.Kind), "capabilities": capabilities, "fake": g.Fake})
	}
	operations := make([]string, len(principal.Operations))
	for i, operation := range principal.Operations {
		operations[i] = string(operation)
	}
	writeOK(w, http.StatusOK, map[string]any{
		"contract_version": harness.ContractVersion,
		"workspace":        principal.Workspace,
		"operations":       operations,
		"budget":           map[string]any{"defaults": budgetView(harnessrun.DefaultBudget), "ceiling": budgetView(harnessrun.CeilingBudget)},
		"limits": map[string]int{
			"max_request_bytes": maxHarnessRequestSize, "max_goal_bytes": harnessrun.MaxGoalBytes,
			"max_artifact_bytes": harnessrun.MaxArtifactBytes, "max_event_page": harnessrun.MaxEventPageLimit,
		},
		"providers": providers,
		"features":  map[string]bool{"idempotent_start": true, "expected_generation": true, "approval_over_mcp": false, "fake_providers": g.Fake},
	})
}

func (g *HarnessGateway) start(w http.ResponseWriter, r *http.Request) {
	var request harnessStartRequest
	if !decodeHarnessBody(w, r, &request) {
		return
	}
	principal, ok := g.authenticate(w, r, request.Workspace, harnessauth.OpStart)
	if !ok {
		return
	}
	budget, valid := request.Budget.budget()
	if !valid {
		writeHarnessRunError(w, harnessrun.ErrBudget)
		return
	}
	status, err := g.Runs.Start(r.Context(), harnessOwner(principal), harnessrun.StartRequest{IdempotencyKey: request.IdempotencyKey, Goal: request.Goal, Budget: budget})
	if err != nil {
		writeHarnessRunError(w, err)
		return
	}
	code := http.StatusAccepted
	if status.Deduplicated {
		code = http.StatusOK
	}
	writeOK(w, code, map[string]any{"run": runView(status)})
}

func (g *HarnessGateway) status(w http.ResponseWriter, r *http.Request, id string) {
	workspace, ok := queryWorkspace(w, r)
	if !ok {
		return
	}
	principal, ok := g.authenticate(w, r, workspace, harnessauth.OpStatus)
	if !ok {
		return
	}
	status, err := g.Runs.Status(r.Context(), harnessOwner(principal), id)
	if err != nil {
		writeHarnessRunError(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"run": runView(status)})
}

func (g *HarnessGateway) cancel(w http.ResponseWriter, r *http.Request, id string) {
	var request harnessCancelRequest
	if !decodeHarnessBody(w, r, &request) {
		return
	}
	principal, ok := g.authenticate(w, r, request.Workspace, harnessauth.OpCancel)
	if !ok {
		return
	}
	status, err := g.Runs.Cancel(r.Context(), harnessOwner(principal), id, harnessrun.Mutation{IdempotencyKey: request.IdempotencyKey, ExpectedGeneration: request.ExpectedGeneration})
	if err != nil {
		writeHarnessRunError(w, err)
		return
	}
	writeOK(w, http.StatusOK, map[string]any{"run": runView(status)})
}

// writeHarnessRunError maps runtime errors to fixed, content-free responses. Error
// text from the runtime is never forwarded.
func writeHarnessRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, harnessrun.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not_found", "run not found")
	case errors.Is(err, harnessrun.ErrStaleGeneration):
		writeErr(w, http.StatusConflict, "stale_generation", "the run changed since you last read it")
	case errors.Is(err, harnessrun.ErrIdempotencyConflict):
		writeErr(w, http.StatusConflict, "idempotency_conflict", "idempotency key was used for a different request")
	case errors.Is(err, harnessrun.ErrApprovalRequired):
		writeErr(w, http.StatusConflict, "approval_required", "this run waits for trusted local approval")
	case errors.Is(err, harnessrun.ErrInvalidState):
		writeErr(w, http.StatusConflict, "invalid_state", "the run state does not allow this operation")
	case errors.Is(err, harnessrun.ErrBudget):
		writeErr(w, http.StatusUnprocessableEntity, "budget_out_of_range", "budget is outside the allowed range")
	case errors.Is(err, harnessrun.ErrInvalid):
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid request")
	case errors.Is(err, harnessrun.ErrBusy):
		w.Header().Set("Retry-After", "1")
		writeErr(w, http.StatusServiceUnavailable, "busy", "the harness is busy; retry shortly")
	case errors.Is(err, harnessrun.ErrClosed):
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "the harness is unavailable")
	default:
		writeErr(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}
