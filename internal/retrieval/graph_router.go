package retrieval

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type GraphQueryMode string

const (
	GraphQueryAuto   GraphQueryMode = "auto"
	GraphQueryBasic  GraphQueryMode = "basic"
	GraphQueryLocal  GraphQueryMode = "local_graph"
	GraphQueryGlobal GraphQueryMode = "global"
)

type GraphIntent string

const (
	GraphIntentDirect     GraphIntent = "direct"
	GraphIntentRelational GraphIntent = "relational"
	GraphIntentGlobal     GraphIntent = "global"
)

type GraphRouteReason string

const (
	GraphReasonBasicDefault      GraphRouteReason = "basic_default"
	GraphReasonCallerBasic       GraphRouteReason = "caller_basic"
	GraphReasonAutoDirect        GraphRouteReason = "auto_direct"
	GraphReasonAutoRelational    GraphRouteReason = "auto_relational"
	GraphReasonAutoGlobal        GraphRouteReason = "auto_global"
	GraphReasonCallerLocal       GraphRouteReason = "caller_local"
	GraphReasonCallerGlobal      GraphRouteReason = "caller_global"
	GraphReasonPolicyDisabled    GraphRouteReason = "graph_policy_disabled"
	GraphReasonModeDisallowed    GraphRouteReason = "graph_mode_disallowed"
	GraphReasonIndexUnavailable  GraphRouteReason = "graph_index_unavailable"
	GraphReasonReadFailed        GraphRouteReason = "graph_read_failed"
	GraphReasonIndexStale        GraphRouteReason = "graph_index_stale"
	GraphReasonIndexStaleAllowed GraphRouteReason = "graph_index_stale_allowed"
)

var (
	ErrGraphRouteRequired = errors.New("required graph route unavailable")
	ErrGraphRouteInvalid  = errors.New("invalid graph route request")
)

type GraphRoutePolicy struct {
	GraphEnabled bool
	AllowLocal   bool
	AllowGlobal  bool
	AllowStale   bool
}

// GraphRouteAvailability describes only Agent Memory-owned normalized index
// state. It has no adapter endpoint, Python runtime, or GraphRAG query handle.
type GraphRouteAvailability struct {
	Readable         bool
	Fresh            bool
	ActiveRevisionID string
}

type GraphRouteRequest struct {
	Mode         GraphQueryMode
	Query        string
	RequireGraph bool
	Policy       GraphRoutePolicy
	Availability GraphRouteAvailability
}

type GraphRouteDecision struct {
	RequestedMode    GraphQueryMode   `json:"requested_mode"`
	SelectedMode     GraphQueryMode   `json:"selected_mode"`
	Intent           GraphIntent      `json:"intent"`
	ReasonCode       GraphRouteReason `json:"reason_code"`
	Fallback         bool             `json:"fallback"`
	Degraded         bool             `json:"degraded"`
	Fresh            bool             `json:"fresh"`
	ActiveRevisionID string           `json:"active_revision_id,omitempty"`
}

type GraphRouteError struct {
	Mode   GraphQueryMode
	Reason GraphRouteReason
}

func (e *GraphRouteError) Error() string {
	return fmt.Sprintf("required graph route %q unavailable: %s", e.Mode, e.Reason)
}

func (e *GraphRouteError) Unwrap() error { return ErrGraphRouteRequired }

type GraphRouter struct{}

func NewGraphRouter() *GraphRouter { return &GraphRouter{} }

var (
	globalIntentPattern = regexp.MustCompile(`\b(across all|all (memories|documents|reports|incidents|sources)|entire (corpus|collection|knowledge base)|most (common|frequent)|frequently|recurring|overall patterns?|corpus-wide|system-wide)\b`)
	localIntentPattern  = regexp.MustCompile(`\b(relat(?:e|ed|es|ionship)|connect(?:ed|ion)?|between|depend(?:s|ency|encies)?|chain|link(?:ed|s)?|caus(?:e|ed|es|al)|influenc(?:e|ed|es)|associated?)\b`)
)

func ClassifyGraphIntent(query string) GraphIntent {
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if globalIntentPattern.MatchString(normalized) {
		return GraphIntentGlobal
	}
	if localIntentPattern.MatchString(normalized) {
		return GraphIntentRelational
	}
	return GraphIntentDirect
}

func (r *GraphRouter) Route(request GraphRouteRequest) (GraphRouteDecision, error) {
	mode := request.Mode
	defaulted := mode == ""
	if defaulted {
		mode = GraphQueryBasic
	}
	if mode != GraphQueryAuto && mode != GraphQueryBasic && mode != GraphQueryLocal && mode != GraphQueryGlobal {
		return GraphRouteDecision{}, fmt.Errorf("%w: unsupported mode", ErrGraphRouteInvalid)
	}
	if request.RequireGraph && mode != GraphQueryLocal && mode != GraphQueryGlobal {
		return GraphRouteDecision{}, fmt.Errorf("%w: graph requirement needs an explicit graph mode", ErrGraphRouteInvalid)
	}
	intent := ClassifyGraphIntent(request.Query)
	decision := GraphRouteDecision{RequestedMode: mode, SelectedMode: GraphQueryBasic, Intent: intent, Fresh: request.Availability.Fresh}
	if mode == GraphQueryBasic {
		if defaulted {
			decision.ReasonCode = GraphReasonBasicDefault
		} else {
			decision.ReasonCode = GraphReasonCallerBasic
		}
		return decision, nil
	}
	target := mode
	if mode == GraphQueryAuto {
		switch intent {
		case GraphIntentGlobal:
			target = GraphQueryGlobal
		case GraphIntentRelational:
			target = GraphQueryLocal
		default:
			decision.ReasonCode = GraphReasonAutoDirect
			return decision, nil
		}
	}
	reason := graphTargetReason(mode, target)
	if !request.Policy.GraphEnabled {
		return graphFallbackOrError(decision, target, GraphReasonPolicyDisabled, request.RequireGraph)
	}
	if (target == GraphQueryLocal && !request.Policy.AllowLocal) || (target == GraphQueryGlobal && !request.Policy.AllowGlobal) {
		return graphFallbackOrError(decision, target, GraphReasonModeDisallowed, request.RequireGraph)
	}
	if !request.Availability.Readable || strings.TrimSpace(request.Availability.ActiveRevisionID) == "" {
		return graphFallbackOrError(decision, target, GraphReasonIndexUnavailable, request.RequireGraph)
	}
	if !request.Availability.Fresh && !request.Policy.AllowStale {
		return graphFallbackOrError(decision, target, GraphReasonIndexStale, request.RequireGraph)
	}
	decision.SelectedMode = target
	decision.ReasonCode = reason
	decision.ActiveRevisionID = request.Availability.ActiveRevisionID
	if !request.Availability.Fresh {
		decision.Degraded = true
		decision.ReasonCode = GraphReasonIndexStaleAllowed
	}
	return decision, nil
}

func graphTargetReason(requested, selected GraphQueryMode) GraphRouteReason {
	if requested == GraphQueryAuto {
		if selected == GraphQueryGlobal {
			return GraphReasonAutoGlobal
		}
		return GraphReasonAutoRelational
	}
	if selected == GraphQueryGlobal {
		return GraphReasonCallerGlobal
	}
	return GraphReasonCallerLocal
}

func graphFallbackOrError(decision GraphRouteDecision, target GraphQueryMode, reason GraphRouteReason, required bool) (GraphRouteDecision, error) {
	if required {
		return GraphRouteDecision{}, &GraphRouteError{Mode: target, Reason: reason}
	}
	decision.SelectedMode = GraphQueryBasic
	decision.ReasonCode = reason
	decision.Fallback = true
	decision.Degraded = true
	return decision, nil
}
