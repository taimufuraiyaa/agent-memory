package api

import (
	"context"
	"encoding/json"
	"github.com/taimufuraiyaa/agent-memory/internal/readingroom"
	"net/http"
	"strings"
	"time"
)

type seminarStartRequest struct {
	RunID       string                     `json:"run_id"`
	PrincipalID string                     `json:"principal_id"`
	Packet      readingroom.EvidencePacket `json:"packet"`
	MaxTokens   int                        `json:"max_tokens"`
}
type SeminarRunState struct {
	ID                string                     `json:"id"`
	OwnerID           string                     `json:"-"`
	Status            readingroom.SeminarStatus  `json:"status"`
	Roles             map[string]string          `json:"roles"`
	ContributionCount int                        `json:"contribution_count"`
	HasSynthesis      bool                       `json:"has_synthesis"`
	Error             string                     `json:"error,omitempty"`
	UpdatedAt         time.Time                  `json:"updated_at"`
	cancel            context.CancelFunc         `json:"-"`
	result            *readingroom.SeminarResult `json:"-"`
}

func seminarStartHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireLibrary(w) {
			return
		}
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		var req seminarStartRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, 400, "bad_request", err.Error())
			return
		}
		if svc.LibraryRoleRunner == nil || strings.TrimSpace(req.RunID) == "" || strings.TrimSpace(req.PrincipalID) == "" {
			writeErr(w, 400, "validation", "run_id, principal_id, and configured role runner are required")
			return
		}
		scope := libraryScope(req.PrincipalID, nil)
		if req.Packet.AuthorizationFingerprint != readingroom.AuthorizationFingerprint(scope) {
			writeErr(w, 403, "forbidden", "evidence packet authorization does not match caller")
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		state := &SeminarRunState{ID: req.RunID, OwnerID: req.PrincipalID, Status: "running", Roles: map[string]string{}, UpdatedAt: time.Now().UTC(), cancel: cancel}
		for _, profile := range readingroom.SeminarProfiles() {
			state.Roles[string(profile.Role)] = "pending"
		}
		svc.mu.Lock()
		if svc.seminarRuns == nil {
			svc.seminarRuns = map[string]*SeminarRunState{}
		}
		if _, exists := svc.seminarRuns[req.RunID]; exists {
			svc.mu.Unlock()
			cancel()
			writeErr(w, 409, "conflict", "seminar run already exists")
			return
		}
		svc.seminarRuns[req.RunID] = state
		response := snapshotSeminarRun(state)
		svc.mu.Unlock()
		go func() {
			result, err := readingroom.NewSeminar(svc.LibraryRoleRunner, readingroom.NewVerifierGate("default:verifier", "v1", nil), nil).Run(ctx, req.RunID, req.Packet, req.MaxTokens)
			svc.mu.Lock()
			defer svc.mu.Unlock()
			state.UpdatedAt = time.Now().UTC()
			state.result = &result
			state.Status = result.Status
			state.ContributionCount = len(result.Contributions)
			state.HasSynthesis = result.Synthesis != nil
			if err != nil {
				state.Error = err.Error()
				if ctx.Err() != nil {
					state.Status = readingroom.SeminarCancelled
				}
			}
			for role := range state.Roles {
				if message, failed := result.RoleErrors[role]; failed {
					state.Roles[role] = "failed: " + message
				} else if state.Status == readingroom.SeminarCancelled {
					state.Roles[role] = "cancelled"
				} else {
					state.Roles[role] = "completed"
				}
			}
		}()
		writeOK(w, http.StatusAccepted, response)
	}
}
func seminarStatusHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireLibrary(w) {
			return
		}
		if r.Method != http.MethodGet {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		state, ok := seminarRunSnapshot(svc, r.URL.Query().Get("id"), r.URL.Query().Get("principal_id"))
		if !ok {
			writeErr(w, 404, "not_found", "seminar run not found")
			return
		}
		writeOK(w, 200, state)
	}
}
func seminarCancelHandler(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireLibrary(w) {
			return
		}
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "method not allowed")
			return
		}
		var req struct {
			ID          string `json:"id"`
			PrincipalID string `json:"principal_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, 400, "bad_request", err.Error())
			return
		}
		svc.mu.Lock()
		state, ok := svc.seminarRuns[req.ID]
		if !ok || state.OwnerID != req.PrincipalID {
			svc.mu.Unlock()
			writeErr(w, 404, "not_found", "seminar run not found")
			return
		}
		if state.cancel != nil {
			state.cancel()
		}
		if state.Status != "completed" && state.Status != readingroom.SeminarPartial {
			state.Status = readingroom.SeminarCancelled
		}
		state.UpdatedAt = time.Now().UTC()
		response := snapshotSeminarRun(state)
		svc.mu.Unlock()
		writeOK(w, 200, response)
	}
}
func seminarRunSnapshot(svc *Service, id, principalID string) (SeminarRunState, bool) {
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	state, ok := svc.seminarRuns[id]
	if !ok || state.OwnerID != principalID {
		return SeminarRunState{}, false
	}
	return snapshotSeminarRun(state), true
}

// snapshotSeminarRun must be called while svc.mu protects the source state.
func snapshotSeminarRun(state *SeminarRunState) SeminarRunState {
	copyState := *state
	copyState.Roles = make(map[string]string, len(state.Roles))
	for role, status := range state.Roles {
		copyState.Roles[role] = status
	}
	copyState.cancel = nil
	copyState.result = nil
	return copyState
}
