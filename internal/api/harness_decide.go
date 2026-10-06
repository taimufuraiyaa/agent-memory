package api

import (
	"net/http"

	"github.com/taimufuraiyaa/agent-memory/internal/harnessauth"
	"github.com/taimufuraiyaa/agent-memory/internal/harnessdecide"
)

// harnessDecideItem is one thing a decision is about. There is no field for a note, a path or
// any free text, so a question built from it stays opaque by construction.
type harnessDecideItem struct {
	ID    string         `json:"id"`
	Class string         `json:"class"`
	Facts map[string]int `json:"facts"`
}

type harnessDecideRequest struct {
	Workspace string              `json:"workspace"`
	Kind      string              `json:"kind"`
	Items     []harnessDecideItem `json:"items"`
	Keep      int                 `json:"keep"`
	// Capability and Facts describe the one subject of a command-risk or cache question.
	Capability string         `json:"capability"`
	Facts      map[string]int `json:"facts"`
}

// decideKinds are the opaque kinds a client may ask, by the plain names the MCP tool uses.
// The internal-class kinds carry project text and are deliberately absent.
var decideKinds = map[string]harnessdecide.Kind{
	"visibility":   harnessdecide.KindVisibility,
	"model":        harnessdecide.KindModel,
	"tools":        harnessdecide.KindTools,
	"cache":        harnessdecide.KindCache,
	"command_risk": harnessdecide.KindCommandRisk,
}

func decideItems(in []harnessDecideItem) []harnessdecide.Item {
	out := make([]harnessdecide.Item, 0, len(in))
	for _, item := range in {
		out = append(out, harnessdecide.Item{ID: item.ID, Class: item.Class, Facts: item.Facts})
	}
	return out
}

// decide asks one opaque decision for a granted client. The reply is advice only: a status and
// a value bounded by what the client offered or by a fixed vocabulary. It approves nothing.
func (g *HarnessGateway) decide(w http.ResponseWriter, r *http.Request) {
	var request harnessDecideRequest
	if !decodeHarnessBody(w, r, &request) {
		return
	}
	principal, ok := g.authenticate(w, r, request.Workspace, harnessauth.OpDecide)
	if !ok {
		return
	}
	if g.Decide == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "no decisions are composed")
		return
	}
	if request.Keep < 0 || request.Keep > harnessdecide.MaxQuestionItems {
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	service := g.Decide("client:" + principal.ClientID + "|" + principal.Workspace)
	var (
		selected []string
		label    string
		outcome  harnessdecide.Outcome
	)
	switch decideKinds[request.Kind] {
	case harnessdecide.KindVisibility:
		selected, outcome = service.Visibility(r.Context(), decideItems(request.Items))
	case harnessdecide.KindModel:
		var chosen string
		chosen, outcome = service.Model(r.Context(), decideItems(request.Items))
		if chosen != "" {
			selected = []string{chosen}
		}
	case harnessdecide.KindTools:
		selected, outcome = service.Tools(r.Context(), decideItems(request.Items), request.Keep)
	case harnessdecide.KindCache:
		var strategy harnessdecide.CacheStrategy
		strategy, outcome = service.Cache(r.Context(), request.Facts)
		label = string(strategy)
	case harnessdecide.KindCommandRisk:
		var risk harnessdecide.Risk
		risk, outcome = service.CommandRisk(r.Context(), request.Capability, request.Facts)
		label = string(risk)
	default: // an unknown name, or an internal-class kind
		writeErr(w, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	view := map[string]any{"kind": string(outcome.Kind), "status": string(outcome.Status), "confidence": outcome.Confidence, "asked": outcome.Asked, "cautious": outcome.Cautious,
		"advisory": true}
	if outcome.Status == harnessdecide.StatusApplied || outcome.Cautious {
		if selected == nil {
			selected = []string{}
		}
		view["selected"] = selected
		view["label"] = label
	}
	writeOK(w, http.StatusOK, view)
}
